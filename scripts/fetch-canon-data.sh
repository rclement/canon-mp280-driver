#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-only
# Fetch Canon's MP280 data files and licence texts from Canon's cnijfilter 3.40
# source archive. They are Canon's property and are not part of this repository.
#
# usage: fetch-canon-data.sh [DEST]          (default: data/canon next to this script's repository)
# env:   CANON_TARBALL=/path/to/cnijfilter-source-3.40-1.tar.gz   use a local copy instead of downloading
#        CANON_MIRRORS="url ..."                                  extra download locations, tried after Canon's
#
# Any source is fine: the archive and every extracted file must match the pinned
# checksums (the archive's below, the files' in data/canon/SHA256SUMS).
set -eu

URL=https://gdlp01.c-wss.com/gds/0/0100003020/01/cnijfilter-source-3.40-1.tar.gz
SHA256=a95dd4ca491a9e8acc3e4ee35cc84904680e82d89ff04a23dda1b0ccffbb8a7a
TOP=cnijfilter-source-3.40-1
DATA="cnb_3700.tbl cnbpname370.tbl cifmp280.conf"
LICENCES="LICENSE-cnijfilter-3.40EN.txt LICENSE-cnijfilter-3.40FR.txt LICENSE-cnijfilter-3.40JP.txt LICENSE-cnijfilter-3.40SC.txt"

here=$(cd "$(dirname "$0")" && pwd)
dest=${1:-$here/../data/canon}
sums=$here/../data/canon/SHA256SUMS

verify_dest() {
	(cd "$dest" && sha256sum -c "$sums" >/dev/null 2>&1)
}

mkdir -p "$dest"
if verify_dest; then
	echo "Canon data already present in $dest" >&2
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

tarball=${CANON_TARBALL:-}
if [ -z "$tarball" ]; then
	tarball=$tmp/canon.tar.gz
	fetch() {
		if command -v curl >/dev/null 2>&1; then curl -fsSL -m 300 -o "$tarball" "$1"; else wget -q -T 300 -O "$tarball" "$1"; fi
	}
	got=
	for u in "$URL" ${CANON_MIRRORS:-} "https://web.archive.org/web/2/$URL"; do
		echo "downloading $u" >&2
		if fetch "$u" && echo "$SHA256  $tarball" | sha256sum -c - >/dev/null 2>&1; then got=1; break; fi
	done
	[ -n "$got" ] || { echo "could not download a valid copy; obtain cnijfilter-source-3.40-1.tar.gz and set CANON_TARBALL" >&2; exit 1; }
fi

echo "$SHA256  $tarball" | sha256sum -c - >&2 || { echo "checksum mismatch: refusing to use $tarball" >&2; exit 1; }

members=""
for f in $DATA; do members="$members $TOP/370/database/$f"; done
for f in $LICENCES; do members="$members $TOP/$f"; done
# shellcheck disable=SC2086
tar -xzf "$tarball" -C "$tmp" $members

for f in $DATA; do install -m 0644 "$tmp/$TOP/370/database/$f" "$dest/$f"; done
for f in $LICENCES; do install -m 0644 "$tmp/$TOP/$f" "$dest/$f"; done
verify_dest || { echo "extracted files do not match data/canon/SHA256SUMS" >&2; exit 1; }
echo "Canon data installed in $dest" >&2
