#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
# second stage of e2e.sh: runs as an unprivileged uid inside the private namespaces
set -u
T=$(mktemp -d "${E2E_TMP:-/mnt}/e2e.XXXXXX")
BUILD=${BUILD:?}
SCEN=${SCENARIOS:?}
fail=0; n=0
ok()  { n=$((n+1)); }
bad() { n=$((n+1)); fail=$((fail+1)); echo "FAIL: $*"; }
check() { if eval "$2"; then ok; else bad "$1  [$2]"; fi; }
pids=""
cleanup() { for p in $pids; do kill "$p" 2>/dev/null; done; sleep 0.2; [ -n "${KEEPDIR:-}" ] && { rm -rf "$KEEPDIR"; cp -r "$T" "$KEEPDIR"; }; rm -rf "$T"; }
trap cleanup EXIT
export LC_ALL=C
SOCK=/mnt/s/c.sock
export CUPS_SERVER=$SOCK
CUPSBIN=$(dirname "$(readlink -f "$(command -v cupsd)")")/..
mkdir -p "$T"/{etc,spool,state,cache,tmp,log,out,bin/backend,bin/filter}
ln -s "$CUPSBIN/lib/cups/daemon" "$T/bin/daemon"      # cups-exec (job sandbox helper) and cups-deviced

# --- the mock printer and the fake stock usb backend (a shell script that stores what it is given) ---
MSOCK="@mp280-e2e-$$"
cp "$SCEN/idle.bscc" "$T/scen"
"$BUILD/mock-printer" -log "$T/mock.log" "$MSOCK" "$T/scen" 2>"$T/mock.err" &
MOCKPID=$!; pids="$pids $MOCKPID"
cat > "$T/bin/fakeusb" <<'EOS'
#!/bin/sh
PATH=$E2E_PATH   # the job environment of cupsd has no /bin/cat on NixOS
if [ $# = 0 ]; then
  echo 'direct usb://Canon/MP280%20series?serial=TEST0001 "Canon MP280 series" "Canon MP280 series USB #1" "MFG:Canon;CMD:BJL,BJRaster3,BSCCe,IVEC,IVECPLI;MDL:MP280 series;CLS:PRINTER;DES:Canon MP280 series;"'
  echo 'direct usb://Other/Printer?serial=X "Other Printer" "Other Printer USB #1" "MFG:Other;MDL:Printer;"'
  exit 0
fi
n=0; while [ -e "$E2E_OUT/job$n.prn" ]; do n=$((n+1)); done
if [ $# -ge 6 ]; then cat "$6" > "$E2E_OUT/job$n.prn"; else cat > "$E2E_OUT/job$n.prn"; fi
echo "INFO: fake usb backend finished job $1" >&2
exit 0
EOS
chmod +x "$T/bin/fakeusb"
cp "$BUILD/mp280-backend" "$T/bin/backend/mp280usb"
for i in $(seq 50); do grep -q listening "$T/mock.err" && break; sleep 0.1; done

cat > "$T/etc/cupsd.conf" <<EOC
Listen $SOCK
LogLevel debug
MaxLogSize 0
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
SetEnv MP280_STATUS_TRANSPORT unix:$MSOCK
SetEnv MP280_REAL_BACKEND $T/bin/fakeusb
SetEnv E2E_OUT $T/out
SetEnv E2E_PATH $(dirname "$(command -v cat)")
SetEnv MP280_PRE_TIMEOUT 3
SetEnv MP280_POST_TIMEOUT 4
LogFilePerm 0644
EOC
${CUPSD_PREFIX:-} cupsd -f -c "$T/etc/cupsd.conf" -s "$T/etc/cups-files.conf" > "$T/log/cupsd.out" 2>&1 & pids="$pids $!"
for i in $(seq 100); do [ -S $SOCK ] && break; sleep 0.1; done
[ -S $SOCK ] || { echo "cupsd did not start"; cat "$T/log/cupsd.out"; exit 1; }

# --- discovery through cups-deviced (lists the wrapper's output only: the fake usb backend is not in ServerBin) ---
if [ -x "$T/bin/daemon/cups-deviced" ]; then
  lpinfo -v > "$T/lpinfo.out" 2>"$T/lpinfo.err"
  check "lpinfo -v lists the MP280 under mp280usb:" "grep -q '^direct mp280usb://Canon/MP280%20series?serial=TEST0001' $T/lpinfo.out"
  check "lpinfo -v does not list the other vendor" "! grep -q Other $T/lpinfo.out"
fi

# --- queue with the wrapper as backend ---
URI='mp280usb://Canon/MP280%20series?serial=TEST0001'
lpadmin -p MP280 -E -v "$URI" -m raw || { echo "lpadmin failed"; cat "$T/log/error_log" | tail -20; exit 1; }

attrs() { # print the marker-* and state attributes through IPP Get-Printer-Attributes
  cat > "$T/get.test" <<'EOT'
{
  NAME "get marker attributes"
  OPERATION Get-Printer-Attributes
  GROUP operation-attributes-tag
  ATTR charset attributes-charset utf-8
  ATTR language attributes-natural-language en
  ATTR uri printer-uri $uri
  ATTR keyword requested-attributes marker-names,marker-levels,marker-colors,marker-types,marker-low-levels,marker-high-levels,marker-message,printer-state-reasons,printer-state,printer-state-message
}
EOT
  ipptool -tv "ipp://%2Fmnt%2Fs%2Fc.sock/printers/MP280" "$T/get.test"
}
val() { attrs | sed -n "s/^ *$1 ([^)]*) = //p"; }
waitjobs() { for i in $(seq 100); do [ -z "$(lpstat -o MP280 2>/dev/null)" ] && break; sleep 0.1; done; sleep 0.3; }

echo "== job 1: idle printer"
cp "$SCEN/idle.bscc" "$T/scen"
head -c 65536 /dev/urandom > "$T/data1"
lp -d MP280 -o raw "$T/data1" >/dev/null; waitjobs
check "job data reached the stock backend unchanged" "cmp -s $T/data1 $T/out/job0.prn"
check "marker-names" "[ \"\$(val marker-names)\" = 'Black PG-510,Tri-colour CL-511' ]"
check "marker-levels" "[ \"\$(val marker-levels)\" = '100,100' ]"
check "marker-colors" "[ \"\$(val marker-colors)\" = '#000000,#00AEEF' ]"
check "marker-types" "[ \"\$(val marker-types)\" = 'ink,ink' ]"
check "marker-low-levels / high-levels" "[ \"\$(val marker-low-levels)\" = '10,10' ] && [ \"\$(val marker-high-levels)\" = '100,100' ]"
attrs > "$T/attrs.job1"
check "no problem in printer-state-reasons" "! val printer-state-reasons | grep -q 'error\\|warning'"

echo "== job 2: colour low"
cp "$SCEN/low_ink_colour.bscc" "$T/scen"
lp -d MP280 -o raw "$T/data1" >/dev/null; waitjobs
check "marker-levels changed to 70,10" "[ \"\$(val marker-levels)\" = '70,10' ]"
check "printer-state-reasons has marker-supply-low-warning" "val printer-state-reasons | grep -q marker-supply-low-warning"
check "marker-message mentions the colour cartridge" "val marker-message | grep -q 'Tri-colour CL-511 is low'"
lpstat -l -p MP280 > "$T/lpstat.out" 2>&1
check "lpstat -l -p shows the alert" "grep -q 'marker-supply-low-warning' $T/lpstat.out"
check "post-job status ran after the stock backend" "[ \$(grep -c 'REQ StartJob' $T/mock.log) = 4 ]"

echo "== job 3: black empty + cover open (two reasons), low warning must be cleared"
cat "$SCEN/empty_black.bscc" | grep -v '^#' | sed 's/DOC:4,01,1688/DOC:4,01,1688,\/,4,01,1200/' > "$T/scen"
lp -d MP280 -o raw "$T/data1" >/dev/null; waitjobs
attrs > "$T/attrs.job3"
check "marker-levels 0,70" "[ \"\$(val marker-levels)\" = '0,70' ]"
check "empty-error and cover-open-error set" "val printer-state-reasons | grep -q marker-supply-empty-error && val printer-state-reasons | grep -q cover-open-error"
check "low warning removed again" "! val printer-state-reasons | grep -q marker-supply-low-warning"

echo probe > "$T/probe.txt"
echo "== probe job (no data to the printer, refreshes the attributes)"
n_before=$(ls "$T/out" | wc -l)
cp "$SCEN/paper_out.bscc" "$T/scen"
lp -d MP280 -t canon-status-probe -o raw $T/probe.txt >/dev/null; waitjobs
check "probe: stock backend not run (no new job file)" "[ \$(ls $T/out | wc -l) = $n_before ]"
check "probe: media-empty-error set, previous errors cleared" "val printer-state-reasons | grep -q media-empty-error && ! val printer-state-reasons | grep -q 'cover-open-error\\|marker-supply-empty-error'"
check "probe: levels follow the printer" "[ \"\$(val marker-levels)\" = '100,70' ]"

echo "== back to normal"
cp "$SCEN/idle.bscc" "$T/scen"
lp -d MP280 -t canon-status-probe -o raw $T/probe.txt >/dev/null; waitjobs
check "all reasons cleared" "! val printer-state-reasons | grep -q 'error\\|warning'"
check "levels back to 100,100" "[ \"\$(val marker-levels)\" = '100,100' ]"

echo "== printer unreachable: the job still prints, queue stays enabled"
kill $MOCKPID 2>/dev/null; wait $MOCKPID 2>/dev/null
head -c 1000 /dev/urandom > "$T/data2"
s=$(date +%s)
lp -d MP280 -o raw "$T/data2" >/dev/null; waitjobs
e=$(date +%s)
check "job printed although the status query failed" "cmp -s $T/data2 $T/out/job$(( $(ls $T/out | grep -c ^job) - 1 )).prn"
check "the queue is still enabled (printer-state not stopped)" "lpstat -p MP280 | grep -q 'enabled'"
check "no delay from the dead status channel ($((e-s))s)" "[ $((e-s)) -lt 6 ]"
check "state reasons stay as they were (no spurious offline)" "! val printer-state-reasons | grep -q offline"
grep -q "mp280usb: " "$T/log/error_log"; check "failure only logged at DEBUG" "[ $? = 0 ]"

echo "-- $n checks, $fail failures"
[ "$fail" = 0 ]
