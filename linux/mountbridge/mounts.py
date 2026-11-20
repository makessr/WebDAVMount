"""Read-only rclone mounts owned by this Linux desktop app."""

from dataclasses import dataclass
import fcntl
import hashlib
import os
from pathlib import Path
import re
import stat
import subprocess
import time


APP_ID = "com.salanghe.MountBridge"
RCLONE = "/usr/bin/rclone"
UNMOUNT = "/usr/bin/fusermount3"
ESCAPED = re.compile(r"\\([0-7]{3})")


def unescape_field(value):
    return ESCAPED.sub(lambda match: chr(int(match[1], 8)), value)


def fuse_mounts():
    mounts = {}
    with open("/proc/self/mountinfo", encoding="utf-8", errors="surrogateescape") as table:
        for line in table:
            left, separator, right = line.rstrip("\n").partition(" - ")
            fields, filesystem = left.split(), right.split()
            if separator and len(fields) >= 6 and len(filesystem) >= 2:
                path = Path(unescape_field(fields[4]))
                mounts[path] = (filesystem[0], unescape_field(filesystem[1]), fields[0])
    return mounts


def valid_remote(name):
    return (
        name and not name.startswith("-") and len(name) <= 160
        and not any(character in name for character in ":/\\")
        and all(character.isprintable() for character in name)
    )


def list_remotes():
    result = subprocess.run(
        [RCLONE, "listremotes", "--ask-password=false"],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        text=True, encoding="utf-8", timeout=12, check=True,
    )
    return sorted({
        line[:-1] for line in result.stdout.splitlines()
        if line.endswith(":") and valid_remote(line[:-1])
    }, key=str.casefold)


def root_dir():
    xdg = os.environ.get("XDG_DATA_HOME")
    base = Path(xdg) if xdg and Path(xdg).is_absolute() else Path.home() / ".local/share"
    return base.resolve() / APP_ID


def mount_name(remote):
    slug = re.sub(r"[^\w.-]+", "-", remote, flags=re.UNICODE).strip(".-")[:30]
    digest = hashlib.sha256(remote.encode()).hexdigest()[:16]
    return f"{slug or 'drive'}-{digest}"


def private_directory(path):
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise PermissionError("MountBridge requires a private data directory")


@dataclass
class Operation:
    process: subprocess.Popen
    started: float
    kind: str


class Mounts:
    def __init__(self, directory=None):
        self.root = directory or root_dir()
        private_directory(self.root)
        self.directory = self.root / "mounts"
        private_directory(self.directory)
        fd = os.open(self.root / "session.lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        self.lock = os.fdopen(fd, "rb")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            self.lock.close()
            raise
        self.operations = {}
        self.children = {}
        self.errors = {}

    def path(self, remote):
        if not valid_remote(remote):
            raise ValueError("Invalid remote name")
        return self.directory / mount_name(remote)

    def owned(self, table):
        remotes = {}
        prefix = APP_ID + ":"
        for path, (filesystem, source, mount_id) in table.items():
            if path.parent != self.directory or filesystem != "fuse.rclone" or not source.startswith(prefix):
                continue
            remote = source[len(prefix):]
            if valid_remote(remote) and self.path(remote) == path:
                remotes[remote] = mount_id
        return remotes

    def refresh(self):
        active = self.owned_from_system()
        for remote, process in list(self.children.items()):
            if process.poll() is not None:
                del self.children[remote]
        for remote, op in list(self.operations.items()):
            code = op.process.poll()
            expired = time.monotonic() - op.started > 40
            if op.kind == "mount" and remote in active:
                del self.operations[remote]
            elif op.kind == "unmount" and code == 0 and remote not in active and remote in self.children:
                if expired:
                    self.children[remote].kill()
            elif code is not None:
                del self.operations[remote]
                if op.kind == "mount":
                    self.errors[remote] = "挂载失败，请检查连接、凭据及 FUSE。"
                elif op.kind == "unmount" and (code != 0 or remote in active):
                    self.errors[remote] = "卸载失败，请先关闭正在使用此目录的程序。"
            elif expired:
                op.process.kill()
        return active

    def owned_from_system(self):
        return self.owned(fuse_mounts())

    def mount(self, remote):
        directory = self.path(remote)
        if remote in self.operations or remote in self.children or directory in fuse_mounts():
            raise ValueError("该挂载目录正在使用。")
        private_directory(directory)
        if any(directory.iterdir()):
            raise ValueError("挂载目录里有本地文件，请先移出。")
        process = subprocess.Popen(
            [RCLONE, "mount", remote + ":", str(directory), "--read-only", "--devname", APP_ID + ":" + remote,
             "--daemon=false", "--rc=false", "--allow-other=false", "--allow-root=false",
             "--allow-non-empty=false", "--vfs-cache-mode=off", "--ask-password=false"],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        self.errors.pop(remote, None)
        self.children[remote] = process
        self.operations[remote] = Operation(process, time.monotonic(), "mount")

    def unmount(self, remote):
        table = fuse_mounts()
        if remote in self.operations or remote not in self.owned(table):
            raise ValueError("该网盘当前无法卸载。")
        self.errors.pop(remote, None)
        process = subprocess.Popen(
            [UNMOUNT, "-u", "--", str(self.path(remote))],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        self.operations[remote] = Operation(process, time.monotonic(), "unmount")
