# Canon PIXMA MP280 driver for CUPS

A self-contained Go implementation of everything a CUPS queue needs for the
Canon PIXMA MP280 (USB, `04a9:1746`): the print filter (PostScript to the
printer's job stream), the colour / halftone / raster pipeline, a USB backend
wrapper that reports ink levels and printer state, and the PPD. Static binaries,
standard library only, no cgo, no libusb.

Status: print output is regression-tested byte for byte against recorded printer
streams; the status path is tested against a mock printer and a private cupsd.
On real hardware only an idle, full-ink status read has been confirmed so far;
reports from other states (low ink, cover open, jams) are welcome.

Provenance: this is a reimplementation of Canon's cnijfilter 3.40 driver, written
with Anthropic Claude Code from an analysis of Canon's source and binaries and
validated byte for byte against Canon's output. It is not a clean-room
implementation; see `NOTICE`.

## Install

```sh
make && sudo make install
```

You can skip the build and install the prebuilt release instead:

```sh
curl -fsSL https://github.com/rclement/canon-mp280-driver/releases/latest/download/get.sh | sh
```

Per-distro notes, NixOS, udev and the optional ink-level timer: `docs/INSTALL.md`.
`make` downloads Canon's printer data files into `data/canon/` (Canon's licence, not the GPL; see `NOTICE`).

## Layout

| Path | Contents |
|---|---|
| `cmd/mp280-filter` | CUPS filter (Ghostscript in, printer stream out) |
| `cmd/mp280-backend` | status-reporting wrapper around CUPS' `usb` backend (installed as `mp280usb`) |
| `cmd/mp280-cif` | image to printer stream (debugging, scripting) |
| `cmd/mp280-status` | query ink levels and state over USB, JSON output |
| `internal/` | pipeline packages, see `docs/ARCHITECTURE.md` |
| `ppd/` | `canonmp280-oss.ppd` |
| `test/` | golden end-to-end suite, mock printer, private-cupsd e2e, performance check |
| `data/canon/` | download target and pinned checksums for Canon's data files (the files themselves are not in the repository) |
| `scripts/` | `fetch-canon-data.sh` |
| `packaging/` | Nix package, udev rule, systemd timer |

## Develop

```sh
nix develop                         # or any Go >= the version in go.mod
make test                           # vet + unit tests + the golden suite (about 25 s)
make perf                           # A4 600 dpi timing check, run alone
```

`docs/ARCHITECTURE.md`, `docs/PRINTER-PROTOCOL.md`, `docs/TESTING.md`,
`docs/KNOWN-QUIRKS.md`.

## Licence

The code is GPL-2.0-only (`LICENSE`). The Canon data files downloaded into `data/canon/` are
Canon's proprietary property, used unmodified under Canon's own licence; see
`NOTICE` and `data/canon/README`.
