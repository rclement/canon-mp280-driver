# Testing

```
make test                                                      # go vet + go test ./...
go test ./...      # includes the golden suite (about 20 s); needs data/canon (make fetch), MP280_TABLE_DIR overrides
make perf         # 600 dpi A4 timing check, opt-in, run alone
MP280_GOLDEN_BIG=1 go test ./test/golden     # plus three full A4 300 dpi pages
```

The tests read the Canon tables in `data/canon` (`make fetch` downloads them; `make test` does it for you). Filter cases also need Ghostscript
(`gs` in `PATH` or `$MP280_GS`) and are skipped without it.

## Golden suite (`test/golden`)

200 curated cases (`cases.go`) run in-process and compare the length and sha256 of the printer stream, with the `SetTime=`
digits masked, against `testdata/expected.tsv`. An empty expected stream means the job is refused. Eleven representative
streams are stored whole (`testdata/full/*.bin.gz`) so a failure can report the first differing byte. `MP280_GOLDEN_DUMP=<dir>`
writes the streams the code produced.

| group | cases | content |
|---|---|---|
| media | 39 | plain, glossy, matte, semigloss, glossygold, otherphoto, proplatinum, hagaki variants, envelopes, highres, tshirt; quality 1..4; A4, Letter, 4X6, 5X7, L, hagaki |
| gray | 10 | `--grayscale` across media and quality |
| intent | 12 | `--renderintent photo/vivid` |
| borderless | 17 | borderless on/off, `--extension 0..3`, with `--fit` |
| sliders | 18 | density, contrast, balance C/M/Y, gamma 1.8/2.2, halftoning |
| cartridge | 10 | `--inkcartridgesettings bk/color` |
| pages | 10 | multi-image PPM streams, `--copies`, `--collate`, `--revprint` |
| content | 14 | text-like, colour bars, gray ramp, mostly-white with margins |
| size | 11 | A5, Legal, 5X7, L, 8X10, custom sizes |
| layout | 14 | `--fit`, `--full`, `--location`, `--bbox`, 600 dpi |
| options | 28 | accepted and rejected option lists |
| filter | 14 | PostScript through `internal/filter` with the shipped PPD and Ghostscript |
| big | 3 | A4 at 300 dpi (hash only, `MP280_GOLDEN_BIG=1`) |

Images are generated in `images.go` with integer arithmetic and a fixed LCG, so the bytes are identical on every platform.
The PostScript for the filter cases uses only filled rectangles and a colorimage (no fonts).

The fixtures record the behaviour the driver is expected to keep. A change in output needs a stated reason, and the fixtures
are then updated in the same commit as the code.

Caveat: filter expectations depend on Ghostscript's rasteriser. They were recorded with the Ghostscript from the Nix dev shell (`nix develop`); a
very different version could change pixels.

## Unit tests

Small, self-contained tests per package: `printdb` (LZW round trip, digest, delta undo, LUT interpolation and validation,
resource-table matching), `colour` (fixed-point known values, slider table hashes, mode selection, tone curve), `halftone`
(quantiser tables, pair cells, fixed-weight diffuser, page buffer rules), `raster` (PackBits, zero runs, bands, 16-row bands),
`jobstream` (IVEC documents, BJL blocks, settings translation), `options` (resource file, option parser, value maps; default
selection and rejections with the data tables), `image` (PPM, BMP, PNG, TIFF samples and malformed inputs), `layout`,
`pipeline` (copies, failing sources, bad geometry), `filter` (option string, PostScript header, PPD marking),
`status` (BSCC decoding and mapping of 15 recorded or synthetic states, session against the mock printer, failure
injection), `usbfs` (ioctl numbers, descriptor parsing, structure sizes), `cupsbackend` (argv, log lines, discovery filter).

## Mock printer and end-to-end tests

* `test/mockprinter`: a software printer that speaks the IVEC status session over a unix socket; scenario files with BSCC
  status lines in `test/status/testdata/scenarios` (idle, low ink, empty, cover open, jam, ...). `cmd/mock-printer` runs it
  standalone.
* `go test ./test/status` builds the binaries and runs: backend discovery (filtering, widening, no backend), 1 MiB stdin
  pass-through, argument and `DEVICE_URI` rewrite, status session before and after the child, stderr order, exit codes, an
  unreachable device (no delay, DEBUG only), a silent printer (bounded), the kill switch, the probe job, SIGTERM forwarding.
* `test/status/e2e/e2e.sh` (also run by `TestE2EPrivateCupsd`): a private cupsd under `unshare -rm` (own socket and
  directories), a queue on `mp280usb://...`, the mock printer and a fake stock backend; 27 checks through `lpinfo -v`,
  `lp`, `lpstat -l -p` and `ipptool` Get-Printer-Attributes: marker names, levels, colours, types, low/high levels,
  message and `printer-state-reasons` across idle, colour low, black empty plus cover open, paper out (probe), idle again,
  and an unreachable printer where the job still prints.
  `BUILD=<dir with mp280-backend and mock-printer> SCENARIOS=test/status/testdata/scenarios bash test/status/e2e/e2e.sh`.
* `test/cupsd/e2e_filter.sh`: a private cupsd with a queue using the shipped PPD and a file device; submits PostScript and
  checks that `mp280-filter` produced a printer stream (uses `data/canon`).
  `BUILD=<dir with mp280-filter> bash test/cupsd/e2e_filter.sh`.

The e2e scripts need `cupsd`, `lpadmin`, `lp`, `lpstat`, `lpinfo`, `ipptool` and `unshare`; the Go wrapper test skips when they
are unavailable. `nix develop` provides them.

## Performance

`go test ./test/perf` (not under `-race`, which slows the pipeline about tenfold) times a full A4 colour page at 600 dpi
(photo and pure noise, 18 to 22 MB of stream). The target is 15 seconds; the measured time is about 9 seconds.

## Hardware

The automated suite never touches a real printer. The status path is exercised against the mock printer only.
