# SPDX-License-Identifier: GPL-2.0-only
# Distro-neutral build and install: plain Go, no cgo, no Nix paths.
#
#   make                      fetch Canon's data files (once) and build into ./build
#   make fetch                only download and verify Canon's data files into data/canon
#   make test                 vet + unit tests + the golden end-to-end suite
#   make perf                 600 dpi A4 timing check (run alone)
#   sudo make install         install filter, backend, PPD, Canon data tables, udev rule (honours PREFIX and DESTDIR)
PREFIX          ?= /usr
DESTDIR         ?=
GO              ?= go
CUPS_CONFIG     ?= cups-config
CUPS_SERVERBIN  ?= $(shell $(CUPS_CONFIG) --serverbin 2>/dev/null || echo $(PREFIX)/lib/cups)
CUPS_DATADIR    ?= $(shell $(CUPS_CONFIG) --datadir 2>/dev/null || echo $(PREFIX)/share/cups)
BINDIR          ?= $(PREFIX)/bin
DATADIR         ?= $(PREFIX)/share/canon-mp280
TABLEDIR        ?= $(DATADIR)/tables
DOCDIR          ?= $(PREFIX)/share/doc/canon-mp280
UDEV_RULESDIR   ?= $(PREFIX)/lib/udev/rules.d
SYSTEMD_UNITDIR ?= $(PREFIX)/lib/systemd/system
PPD_NAME        ?= canonmp280-oss.ppd

export CGO_ENABLED = 0
OUT      := build
LDFLAGS  := -s -w -X canon-mp280-driver/internal/cif.systemDir=$(TABLEDIR)
INSTALL  ?= install

# Canon's data files are not in the repository; scripts/fetch-canon-data.sh
# downloads and verifies them (CANON_TARBALL=<archive> uses a local copy).
CANON_DIR  := data/canon
CANON_DATA := $(addprefix $(CANON_DIR)/,cnb_3700.tbl cnbpname370.tbl cifmp280.conf)

.PHONY: all fetch test perf vet test-cross install uninstall clean

fetch $(CANON_DATA):
	scripts/fetch-canon-data.sh $(CANON_DIR)

all: $(CANON_DATA)
	@mkdir -p $(OUT)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(OUT)/ ./cmd/...

vet:
	$(GO) vet ./...

test: vet $(CANON_DATA)
	$(GO) test ./...

perf: $(CANON_DATA)
	MP280_PERF=1 $(GO) test -count=1 -v ./test/perf

# The binaries are pure Go; check that the supported architectures compile.
test-cross:
	GOARCH=amd64 $(GO) build -o /dev/null ./...
	GOARCH=arm64 $(GO) build -o /dev/null ./...
	GOARCH=riscv64 $(GO) build -o /dev/null ./...

# CUPS wants filters and backends in its serverbin. The status wrapper is
# installed as `mp280usb` (device URI scheme mp280usb:), mode 0755 so
# cupsd runs it as lp; see docs/ARCHITECTURE.md (mp280usb backend) for the USB access it needs.
install: all
	$(INSTALL) -d $(DESTDIR)$(TABLEDIR) $(DESTDIR)$(DOCDIR) $(DESTDIR)$(CUPS_SERVERBIN)/filter $(DESTDIR)$(CUPS_SERVERBIN)/backend \
		$(DESTDIR)$(CUPS_DATADIR)/model $(DESTDIR)$(BINDIR) $(DESTDIR)$(UDEV_RULESDIR) \
		$(DESTDIR)$(SYSTEMD_UNITDIR)
	$(INSTALL) -m 0755 $(OUT)/mp280-filter  $(DESTDIR)$(CUPS_SERVERBIN)/filter/mp280-filter
	$(INSTALL) -m 0755 $(OUT)/mp280-backend $(DESTDIR)$(CUPS_SERVERBIN)/backend/mp280usb
	$(INSTALL) -m 0644 ppd/$(PPD_NAME) $(DESTDIR)$(CUPS_DATADIR)/model/$(PPD_NAME)
	$(INSTALL) -m 0755 $(OUT)/mp280-cif     $(DESTDIR)$(BINDIR)/mp280-cif
	$(INSTALL) -m 0755 $(OUT)/mp280-status  $(DESTDIR)$(BINDIR)/mp280-status
	# Canon's data stays separate files read at run time (never linked into the programs) with their licence.
	$(INSTALL) -m 0644 $(CANON_DATA) $(DESTDIR)$(TABLEDIR)/
	$(INSTALL) -m 0644 data/canon/README data/canon/LICENSE-cnijfilter-3.40*.txt $(DESTDIR)$(DOCDIR)/
	$(INSTALL) -m 0644 packaging/udev/70-canon-mp280.rules $(DESTDIR)$(UDEV_RULESDIR)/70-canon-mp280.rules
	$(INSTALL) -m 0644 packaging/systemd/canon-mp280-status@.service packaging/systemd/canon-mp280-status@.timer \
		$(DESTDIR)$(SYSTEMD_UNITDIR)/
	@echo
	@echo "Installed. Next: udevadm control --reload && udevadm trigger, and restart cups."

uninstall:
	rm -f $(DESTDIR)$(CUPS_SERVERBIN)/filter/mp280-filter $(DESTDIR)$(CUPS_SERVERBIN)/backend/mp280usb \
		$(DESTDIR)$(CUPS_DATADIR)/model/$(PPD_NAME) $(DESTDIR)$(BINDIR)/mp280-cif $(DESTDIR)$(BINDIR)/mp280-status \
		$(DESTDIR)$(UDEV_RULESDIR)/70-canon-mp280.rules \
		$(DESTDIR)$(SYSTEMD_UNITDIR)/canon-mp280-status@.service $(DESTDIR)$(SYSTEMD_UNITDIR)/canon-mp280-status@.timer
	rm -rf $(DESTDIR)$(TABLEDIR) $(DESTDIR)$(DOCDIR)

clean:
	rm -rf $(OUT)
