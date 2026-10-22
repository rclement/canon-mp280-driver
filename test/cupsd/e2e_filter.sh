#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
# Smoke test: private cupsd (user+mount namespace; nothing outside is touched) with a queue whose
# PPD is the shipped canonmp280-oss.ppd and whose device is a plain file. Submits PostScript as
# application/vnd.cups-postscript and checks that mp280-filter produced a printer stream.
# env: BUILD (dir with mp280-filter; default ./build), MP280_TABLE_DIR (Canon tables; default data/canon),
#      MP280_GS (optional). Run inside `nix develop`:  test/cupsd/e2e_filter.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
export BUILD=${BUILD:-$HERE/../../build}
BUILD=$(cd "$BUILD" && pwd); export BUILD
MP280_TABLE_DIR=${MP280_TABLE_DIR:-$(cd "$HERE/../../data/canon" && pwd)}; export MP280_TABLE_DIR
if [ -z "${E2E_INNER:-}" ]; then
  export E2E_INNER=1 E2E_UID=$(id -u) E2E_GID=$(id -g)
  exec unshare -rm bash "$0"
fi
if [ -z "${E2E_INNER2:-}" ]; then
  mount -t tmpfs tmpfs /mnt || exit 1
  mkdir -p /mnt/s
  export E2E_INNER2=1
  exec unshare -U --map-user="$E2E_UID" --map-group="$E2E_GID" bash "$0"
fi
T=$(mktemp -d /mnt/e2e.XXXXXX); pids=""
trap 'for p in $pids; do kill $p 2>/dev/null; done; rm -rf "$T"' EXIT
export LC_ALL=C CUPS_SERVER=/mnt/s/c.sock
SOCK=$CUPS_SERVER
CUPSBIN=$(dirname "$(readlink -f "$(command -v cupsd)")")/..
mkdir -p "$T"/{etc,spool,state,cache,tmp,log,bin/filter,bin/backend}
ln -s "$CUPSBIN/lib/cups/daemon" "$T/bin/daemon"
ln -s "$BUILD/mp280-filter" "$T/bin/filter/mp280-filter"
cp "$HERE/../../ppd/canonmp280-oss.ppd" "$T/canonmp280-oss.ppd"
: > "$T/out.prn"
cat > "$T/etc/cupsd.conf" <<EOC
Listen $SOCK
LogLevel debug
Browsing No
WebInterface No
DefaultAuthType None
PreserveJobHistory No
<Location />
  Order allow,deny
  Allow all
</Location>
<Location /admin>
  Order allow,deny
  Allow all
</Location>
<Policy default>
  <Limit All>
    Order deny,allow
  </Limit>
</Policy>
EOC
cat > "$T/etc/cups-files.conf" <<EOC
SystemGroup $(id -gn)
User $(id -un)
Group $(id -gn)
FileDevice Yes
ServerRoot $T/etc
RequestRoot $T/spool
StateDir $T/state
CacheDir $T/cache
TempDir $T/tmp
ServerBin $T/bin
DataDir $CUPSBIN/share/cups
ErrorLog $T/log/error_log
AccessLog $T/log/access_log
PageLog $T/log/page_log
Printcap /dev/null
SetEnv MP280_TABLE_DIR $MP280_TABLE_DIR
SetEnv MP280_GS ${MP280_GS:-$(command -v gs)}
LogFilePerm 0644
EOC
cupsd -f -c "$T/etc/cupsd.conf" -s "$T/etc/cups-files.conf" > "$T/log/cupsd.out" 2>&1 & pids="$pids $!"
for i in $(seq 100); do [ -S $SOCK ] && break; sleep 0.1; done
[ -S $SOCK ] || { echo "cupsd did not start"; cat "$T/log/cupsd.out"; exit 1; }
fail=0
lpadmin -p mp280 -E -v "file://$T/out.prn" -P "$T/canonmp280-oss.ppd" || { echo "FAIL: lpadmin"; exit 1; }
lpstat -p mp280 | grep -q . || fail=1

cat > "$T/doc.ps" <<'EOP'
%!PS-Adobe-3.0
%%Pages: 1
%%EndComments
%%Page: 1 1
1 0 0 setrgbcolor 100 100 200 200 rectfill
showpage
%%EOF
EOP
lp -d mp280 -t smoke -o document-format=application/vnd.cups-postscript -o CNQuality=2 -o MediaType=plain "$T/doc.ps" >/dev/null || { echo "FAIL: lp"; fail=1; }
for i in $(seq 300); do
  [ -z "$(lpstat -W not-completed -o mp280 2>/dev/null)" ] && break
  sleep 0.2
done
size=$(stat -c %s "$T/out.prn")
if [ "$size" -lt 100000 ]; then echo "FAIL: printer stream too small ($size bytes)"; tail -30 "$T/log/error_log"; fail=1; fi
head -c 600 "$T/out.prn" | grep -q 'StartJob' || { echo "FAIL: no IVEC StartJob in stream"; fail=1; }
grep -q 'mp280-filter: cif' "$T/log/error_log" || { echo "FAIL: filter did not run"; fail=1; }
[ $fail = 0 ] && echo "e2e_filter: ok ($size bytes)"
exit $fail
