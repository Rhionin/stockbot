#!/usr/bin/env sh
# Stage deploy files that pantry-update.sh installs on the Pi after a
# successful image deploy. The pantry image build copies this tree to
# /usr/share/stockbot/host-bundle.

set -euo pipefail

dest=${1:?usage: build-host-bundle.sh DEST}
root=$(cd "$(dirname "$0")" && pwd)

mkdir -p "$dest/systemd"
for item in publish-mode.sh runtime-setup.sh vapid-env.sh; do
  cp "$root/$item" "$dest/$item"
done
for item in \
  pantry-update.sh \
  pantry-backup.sh \
  pantry-update.service \
  pantry-update.timer \
  pantry-update.path \
  pantry-backup.service \
  pantry-backup.timer
do
  cp "$root/systemd/$item" "$dest/systemd/$item"
done
chmod +x "$dest/systemd/pantry-update.sh" "$dest/systemd/pantry-backup.sh"
