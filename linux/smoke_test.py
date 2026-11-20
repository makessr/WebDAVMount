"""Exercise the installed GTK application, optionally with real FUSE mounts."""

import argparse
import errno
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from unittest.mock import patch

sys.path.insert(0, "/usr/lib/salanghe-mountbridge")
from app import Application, GLib


def await_condition(check, seconds=12):
    until = time.monotonic() + seconds
    context = GLib.MainContext.default()
    while time.monotonic() < until:
        while context.pending():
            context.iteration(False)
        if check():
            return
        time.sleep(0.05)
    raise AssertionError("GTK or mount state did not reach the expected condition")


def files_ready(url):
    try:
        with urllib.request.urlopen(url, timeout=1) as response:
            return response.status == 200
    except OSError:
        return False


def buttons(window):
    for row in window.rows.get_children():
        for widget in row.get_child().get_children():
            if hasattr(widget, "get_label"):
                yield widget.get_label(), widget


def click(window, label):
    next(widget for text, widget in buttons(window) if text == label).clicked()


def click_mount(window):
    launch = subprocess.Popen

    def spawn(args, **kwargs):
        if args[:2] == ["/usr/bin/rclone", "mount"]:
            kwargs["stderr"] = None  # The fixture contains loopback configuration only.
        return launch(args, **kwargs)

    with patch("mounts.subprocess.Popen", side_effect=spawn):
        click(window, "挂载")


def loopback_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def main(startup_only):
    assert os.getuid() != 0
    with tempfile.TemporaryDirectory(prefix="mountbridge-smoke-") as temp:
        root = Path(temp)
        os.environ["XDG_DATA_HOME"] = str(root / "data")
        os.environ["RCLONE_CONFIG"] = str(root / "rclone.conf")
        address = f"127.0.0.1:{loopback_port()}"
        sftp_port = loopback_port()
        remote = root / "remote"
        remote.mkdir()
        (remote / "probe.txt").write_bytes(b"MountBridge Linux\n")
        url = f"http://{address}/"
        password = "local-test-only"
        obscure = subprocess.check_output(["/usr/bin/rclone", "obscure", password], text=True).strip()
        Path(os.environ["RCLONE_CONFIG"]).write_text(
            f"[Demo WebDAV]\ntype = webdav\nurl = {url}\nvendor = other\n\n"
            f"[SFTP]\ntype = sftp\nhost = 127.0.0.1\nport = {sftp_port}\nuser = preview\npass = {obscure}\n",
            encoding="utf-8",
        )
        server = subprocess.Popen(["/usr/bin/rclone", "serve", "webdav", str(remote), "--addr", address],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        sftp = subprocess.Popen(["/usr/bin/rclone", "serve", "sftp", str(remote),
                                 "--addr", f"127.0.0.1:{sftp_port}", "--user", "preview", "--pass", password],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        window = None
        try:
            await_condition(lambda: files_ready(url + "probe.txt"))
            app = Application()
            assert app.register(None)
            app.activate()
            window = app.get_active_window()
            assert window.get_title() == "MountBridge · SALANGHE"
            await_condition(lambda: "挂载" in dict(buttons(window)))
            if startup_only:
                assert set(window.remotes) == {"Demo WebDAV", "SFTP"}
                assert sum(label == "挂载" for label, _ in buttons(window)) == 2
                window.close()
                await_condition(lambda: not app.get_windows())
                print("PASS: GTK startup and real WebDAV/SFTP configuration listing")
                return
            click_mount(window)
            await_condition(lambda: "打开目录" in dict(buttons(window)))
            mounted = window.mounts.path("Demo WebDAV")
            assert (mounted / "probe.txt").read_bytes() == b"MountBridge Linux\n"
            try:
                (mounted / "write.txt").write_text("forbidden", encoding="utf-8")
            except OSError as error:
                assert error.errno in {errno.EROFS, errno.EACCES, errno.EPERM}
            else:
                raise AssertionError("read-only mount accepted a write")
            assert not (remote / "write.txt").exists()
            click(window, "打开目录")
            assert window.status.get_text() == ""
            click_mount(window)
            await_condition(lambda: set(window.mounts.owned_from_system()) == {"Demo WebDAV", "SFTP"})
            assert (window.mounts.path("SFTP") / "probe.txt").read_bytes() == b"MountBridge Linux\n"
            click(window, "卸载")
            await_condition(lambda: "Demo WebDAV" not in window.mounts.children)
            assert set(window.mounts.owned_from_system()) == {"SFTP"}
            click_mount(window)
            await_condition(lambda: set(window.mounts.owned_from_system()) == {"Demo WebDAV", "SFTP"})
            await_condition(lambda: not window.mounts.operations)
            window.close()
            await_condition(lambda: not window.mounts.owned_from_system() and not app.get_windows())
            assert not window.mounts.children
            print("PASS: GTK window, simultaneous WebDAV and SFTP reads, write refusal, unmount and close")
        finally:
            if window:
                for process in window.mounts.children.values():
                    if process.poll() is None:
                        process.terminate()
                        process.wait(timeout=5)
            server.terminate()
            server.wait(timeout=5)
            sftp.terminate()
            sftp.wait(timeout=5)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--startup-only", action="store_true")
    main(parser.parse_args().startup_only)
