#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
# End-to-end: private cupsd (user+mount namespace, nothing outside is touched) + a queue whose backend is the wrapper
# mp280usb over a SOFTWARE MOCK printer. Asserts, through IPP Get-Printer-Attributes / lpstat, that marker-names,
# marker-levels, marker-colors, marker-types, marker-low/high-levels and printer-state-reasons appear and change when the
# mock printer's state changes. No real printer or USB device is involved.
# env: BUILD (dir with mp280-backend and mock-printer), SCENARIOS (dir with the *.bscc scenario files).
# Needs cupsd, cups-deviced (optional), lpadmin, lp, lpstat, lpinfo, ipptool and unshare in PATH.
set -u
: "${BUILD:?set BUILD}"
: "${SCENARIOS:?set SCENARIOS}"
HERE=$(cd "$(dirname "$0")" && pwd)
if [ -z "${E2E_INNER:-}" ]; then
  export E2E_INNER=1 E2E_UID=$(id -u) E2E_GID=$(id -g)
  exec unshare -rm bash "$0"
fi
# --- inside the user+mount namespace (fake root, private mounts) ---
mount -t tmpfs tmpfs /mnt || exit 1
mkdir -p /mnt/s
exec unshare -U --map-user="$E2E_UID" --map-group="$E2E_GID" bash "$HERE/e2e_inner.sh"
