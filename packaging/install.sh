#!/bin/sh
# SPDX-License-Identifier: GPL-2.0-only
# Installer shipped inside the prebuilt release tarballs (see `make dist`).
# Installs the prebuilt binaries next to this script, then downloads Canon's
# data files (they are Canon's property and are not part of the tarball).
#
#   sudo ./install.sh              install
#   sudo ./install.sh --uninstall  remove everything it installed
#
# env: PREFIX (default /usr; the binaries look for Canon's tables in
#      /usr/share/canon-mp280/tables, set MP280_TABLE_DIR if you change it),
#      DESTDIR, CUPS_SERVERBIN, CUPS_DATADIR, CANON_TARBALL (local copy of
#      cnijfilter-source-3.40-1.tar.gz instead of downloading it).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-/usr}
DESTDIR=${DESTDIR:-}
TABLEDIR=$PREFIX/share/canon-mp280/tables
DOCDIR=$PREFIX/share/doc/canon-mp280
UDEV_RULESDIR=$PREFIX/lib/udev/rules.d
SYSTEMD_UNITDIR=$PREFIX/lib/systemd/system
PPD=canonmp280-oss.ppd

if command -v cups-config >/dev/null 2>&1; then
	serverbin=$(cups-config --serverbin)
	datadir=$(cups-config --datadir)
elif [ -d "$PREFIX/libexec/cups" ]; then
	serverbin=$PREFIX/libexec/cups
	datadir=$PREFIX/share/cups
else
	serverbin=$PREFIX/lib/cups
	datadir=$PREFIX/share/cups
fi
CUPS_SERVERBIN=${CUPS_SERVERBIN:-$serverbin}
CUPS_DATADIR=${CUPS_DATADIR:-$datadir}

if [ -z "$DESTDIR" ] && [ "$(id -u)" -ne 0 ]; then
	echo "install.sh: must run as root (try: sudo $0 $*)" >&2
	exit 1
fi

d() { printf '%s%s' "$DESTDIR" "$1"; }

if [ "${1:-}" = "--uninstall" ]; then
	rm -f "$(d "$CUPS_SERVERBIN")/filter/mp280-filter" "$(d "$CUPS_SERVERBIN")/backend/mp280usb" \
		"$(d "$CUPS_DATADIR")/model/$PPD" "$(d "$PREFIX")/bin/mp280-cif" "$(d "$PREFIX")/bin/mp280-status" \
		"$(d "$UDEV_RULESDIR")/70-canon-mp280.rules" \
		"$(d "$SYSTEMD_UNITDIR")/canon-mp280-status@.service" "$(d "$SYSTEMD_UNITDIR")/canon-mp280-status@.timer"
	rm -rf "$(d "$TABLEDIR")" "$(d "$DOCDIR")"
	echo "Removed. Restart cups to drop the driver."
	exit 0
fi

[ -x "$here/bin/mp280-filter" ] || { echo "install.sh: $here/bin is missing; run this from the extracted release" >&2; exit 1; }

# Fetch Canon's data first so a failed download leaves nothing half installed.
echo "Canon's data files are under Canon's own licence (see data/canon/README); downloading them accepts it." >&2
"$here/scripts/fetch-canon-data.sh" "$here/data/canon"

install -d "$(d "$TABLEDIR")" "$(d "$DOCDIR")" "$(d "$CUPS_SERVERBIN")/filter" "$(d "$CUPS_SERVERBIN")/backend" \
	"$(d "$CUPS_DATADIR")/model" "$(d "$PREFIX")/bin" "$(d "$UDEV_RULESDIR")" "$(d "$SYSTEMD_UNITDIR")"
install -m 0755 "$here/bin/mp280-filter"  "$(d "$CUPS_SERVERBIN")/filter/mp280-filter"
install -m 0755 "$here/bin/mp280-backend" "$(d "$CUPS_SERVERBIN")/backend/mp280usb"
install -m 0644 "$here/ppd/$PPD" "$(d "$CUPS_DATADIR")/model/$PPD"
install -m 0755 "$here/bin/mp280-cif"    "$(d "$PREFIX")/bin/mp280-cif"
install -m 0755 "$here/bin/mp280-status" "$(d "$PREFIX")/bin/mp280-status"
install -m 0644 "$here"/data/canon/*.tbl "$here"/data/canon/*.conf "$(d "$TABLEDIR")/"
install -m 0644 "$here"/data/canon/README "$here"/data/canon/LICENSE-cnijfilter-3.40*.txt "$(d "$DOCDIR")/"
install -m 0644 "$here/udev/70-canon-mp280.rules" "$(d "$UDEV_RULESDIR")/70-canon-mp280.rules"
install -m 0644 "$here"/systemd/canon-mp280-status@.service "$here"/systemd/canon-mp280-status@.timer "$(d "$SYSTEMD_UNITDIR")/"

if [ -z "$DESTDIR" ]; then
	command -v udevadm >/dev/null 2>&1 && { udevadm control --reload; udevadm trigger; } || true
	if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet cups 2>/dev/null; then
		systemctl restart cups
	fi
	command -v gs >/dev/null 2>&1 || echo "warning: ghostscript (gs) is not installed; it is required (apt install ghostscript cups-filters)" >&2
	command -v cupsd >/dev/null 2>&1 || [ -x /usr/sbin/cupsd ] || echo "warning: CUPS is not installed (apt install cups)" >&2
fi

echo
echo "Installed $(cat "$here/VERSION" 2>/dev/null || echo driver)."
echo "Next: plug in the printer and add it in CUPS: device \"Canon MP280 series (mp280usb)\", driver $PPD"
