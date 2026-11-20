#!/usr/bin/env bash
set -Eeuo pipefail

smoke_args=(--startup-only)
if [[ $# -eq 1 && "$1" == --fuse ]]; then
  test -c /dev/fuse
  smoke_args=()
elif [[ $# -ne 0 ]]; then
  printf 'Usage: %s [--fuse]\n' "$0" >&2
  exit 2
fi

# Keep downloaded archives in the CI cache mounted by the workflow.
rm -f /etc/apt/apt.conf.d/docker-clean
apt-get update -qq
apt-get install -y -qq --no-install-recommends rclone fuse3 python3-gi gir1.2-gtk-3.0 \
  xfce4-terminal dbus-x11 xvfb xauth thunar desktop-file-utils
apt-get autoclean -qq

python3 /source/linux/package.py --output /tmp/preview
dpkg -i /tmp/preview/*.deb
desktop-file-validate /usr/share/applications/com.salanghe.MountBridge.desktop
useradd -m preview
runuser -u preview -- dbus-run-session -- xvfb-run -a env NO_AT_BRIDGE=1 python3 /source/linux/smoke_test.py "${smoke_args[@]}"
