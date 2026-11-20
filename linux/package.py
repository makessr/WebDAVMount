"""Build the Debian preview with runtime dependencies supplied by apt."""

import argparse
from pathlib import Path
import shutil
import subprocess
import tempfile


VERSION = "0.1.0~preview2"
PACKAGE = "salanghe-mountbridge"
ROOT = Path(__file__).resolve().parent.parent


def place(stage, relative, *, content=None, source=None, mode=0o644):
    destination = stage / relative
    destination.parent.mkdir(parents=True, exist_ok=True)
    if source:
        shutil.copyfile(source, destination)
    else:
        destination.write_text(content, encoding="utf-8")
    destination.chmod(mode)


def build(output):
    output.mkdir(parents=True, exist_ok=True)
    asset_version = VERSION.replace("~", ".")
    artifact = output / f"{PACKAGE}_{asset_version}_all.deb"
    with tempfile.TemporaryDirectory(dir=output, prefix=".mountbridge-") as temp:
        stage = Path(temp) / "package"
        place(stage, "DEBIAN/control", content=f"""Package: {PACKAGE}
Version: {VERSION}
Section: utils
Priority: optional
Architecture: all
Maintainer: SALANGHE <jason@salanghe.com>
Depends: python3 (>= 3.9), python3-gi, gir1.2-gtk-3.0, rclone, fuse3, x-terminal-emulator
Recommends: thunar
Homepage: https://github.com/makessr/WebDAVMount
Description: MountBridge by SALANGHE for Debian and XFCE
 Graphical interface to mount configured rclone remotes as read-only folders.
""")
        for name in ("app.py", "mounts.py"):
            place(stage, f"usr/lib/{PACKAGE}/{name}", source=ROOT / "linux/mountbridge" / name)
        place(stage, f"usr/bin/{PACKAGE}", mode=0o755,
              content=f'#!/bin/sh\nexec /usr/bin/python3 /usr/lib/{PACKAGE}/app.py "$@"\n')
        desktop_entry = (ROOT / "linux/com.salanghe.MountBridge.desktop").read_text(encoding="utf-8")
        place(stage, "usr/share/applications/com.salanghe.MountBridge.desktop", content=desktop_entry)
        place(stage, "usr/share/pixmaps/com.salanghe.MountBridge.png",
              source=ROOT / "internal/branding/logo.png")
        for name in ("LICENSE", "NOTICE"):
            place(stage, f"usr/share/doc/{PACKAGE}/{name}", source=ROOT / name)
        place(stage, f"usr/share/doc/{PACKAGE}/copyright", content="""Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: MountBridge
Source: https://github.com/makessr/WebDAVMount

Files: *
Copyright: 2026 SALANGHE
License: Apache-2.0
 See /usr/share/doc/salanghe-mountbridge/LICENSE.
""")
        place(stage, f"usr/share/doc/{PACKAGE}/README.md", source=ROOT / "linux/README.md")
        temporary_artifact = Path(temp) / "binary.deb"
        subprocess.run(["dpkg-deb", "--root-owner-group", "--build", str(stage),
                        str(temporary_artifact)], check=True)
        # Both paths are on the same filesystem. Linking refuses to overwrite.
        artifact.hardlink_to(temporary_artifact)
    return artifact


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    print(build(parser.parse_args().output.resolve()))
