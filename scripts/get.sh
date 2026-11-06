#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-only
# Download and install the prebuilt driver from the project's GitHub release.
#
#   curl -fsSL https://github.com/rclement/canon-mp280-driver/releases/latest/download/get.sh | sh
#
# env: VERSION=v1.2.3   install that release instead of the latest
#      REPO=owner/name  fetch from another fork
# The tarball is checked against the release's SHA256SUMS, then its install.sh
# runs as root (through sudo if needed); that step also downloads Canon's data.
set -eu

REPO=${REPO:-rclement/canon-mp280-driver}
VERSION=${VERSION:-latest}

case $(uname -m) in
armv6l | armv7l | armhf) arch=armv6 ;; # one ARMv6 build runs on every 32-bit ARM from ARMv6 up
aarch64 | arm64)
	# a 64-bit kernel can run a 32-bit userland (armhf userland on an aarch64 kernel)
	if [ "$(getconf LONG_BIT 2>/dev/null || echo 64)" = 32 ]; then arch=armv6; else arch=arm64; fi ;;
x86_64 | amd64) arch=amd64 ;;
*) echo "get.sh: no prebuilt driver for $(uname -m); build from source (make && sudo make install)" >&2; exit 1 ;;
esac
[ "$(uname -s)" = Linux ] || { echo "get.sh: Linux only" >&2; exit 1; }

if [ "$VERSION" = latest ]; then
	base=https://github.com/$REPO/releases/latest/download
else
	base=https://github.com/$REPO/releases/download/$VERSION
fi
name=canon-mp280-driver-linux-$arch

fetch() {
	if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"; else wget -q -O "$2" "$1"; fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $name ($VERSION) from $base" >&2
fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"
(cd "$tmp" && grep " $name.tar.gz\$" SHA256SUMS | sha256sum -c -) >&2 || { echo "get.sh: checksum mismatch, refusing to install" >&2; exit 1; }
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"

if [ "$(id -u)" -eq 0 ]; then
	"$tmp/$name/install.sh" </dev/null
else
	sudo "$tmp/$name/install.sh" </dev/null
fi
