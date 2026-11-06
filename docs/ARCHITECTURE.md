# Architecture

A CUPS driver for the Canon PIXMA MP280 written in plain Go (standard library only, no cgo, no libusb).
It consists of a CUPS filter (PostScript to printer stream), a wrapper backend that adds ink levels and printer
state, a static PPD, and a few command line tools.

```
PostScript --> mp280-filter --> Ghostscript (ppmraw) --> cif --> printer stream --> CUPS usb backend --> printer
                  |                                                                       ^
                  | PPD options                                  mp280usb backend -----+ (status query before/after)
```

## Data flow

1. **Filter** (`internal/filter`). Reads the job options and the queue's PPD, resolves them into front end switches
   (`--media`, `--quality`, `--papersize`, ...), starts Ghostscript (`-sDEVICE=ppmraw`) and feeds its PPM output
   in-process to `cif`. The only child process is Ghostscript.
2. **cif** (`internal/cif`). The image-to-printer-stream front end, also available as `mp280-cif`. It parses the switches
   (`options`), opens the page images (`image`), places them on the page (`layout`) and runs the pixel path (`pipeline`).
3. **Pixel path** (`internal/pipeline`). For each row: white-margin check, RGB to ink amounts (`colour`), halftoning to dot
   planes (`halftone`), packing into raster bands (`raster`). The `jobstream` package frames the job and each page with the
   header, page setup commands and footers.
4. **Backend** (`internal/cupsbackend`, `internal/status`, `internal/usbfs`). The `mp280usb` backend wraps CUPS' stock
   `usb` backend and queries the printer for ink levels and state before and after each job.

The printer's behaviour is described by Canon's data tables (see below). The code contains the algorithms; which modes,
look-up tables and command bytes belong to a combination of media, quality and paper size is read from the tables.

## Data tables

Three files supplied by Canon, downloaded unmodified into `data/canon/` by `make` (they are not part of this repository and not under the GPL; see `data/canon/README`). They are only read at run time, never compiled into the programs:

| File | Content |
|---|---|
| `cnb_3700.tbl` | printer database: option/resource tables, page setup records, colour LUTs, halftone and tone-curve trees |
| `cnbpname370.tbl` | model name table |
| `cifmp280.conf` | option word to id mapping for the command line |

Lookup order for the directory: `$MP280_TABLE_DIR`, the compiled-in default (`make` sets it through
`-X canon-mp280-driver/internal/cif.systemDir=...`), then `$XDG_DATA_HOME` or `~/.local/share` `/canon-mp280/tables`.

## Package map

| Package | Responsibility |
|---|---|
| `printdb` | The `.tbl` container and its codecs (LZW, running-sum delta, integrity digest, block trees, LUT blobs); the resource-table queries (option ids, page records); the 3D LUT engine (tetrahedral interpolation). |
| `options` | Command line and option database: popt-style parsing, the option dependency engine driven by table 1020, paper size and margin queries, accept/reject decisions. Produces the `Settings` of a job. |
| `jobstream` | Everything in the printer stream except raster data: IVEC XML, BJL blocks, page setup commands (`ESC ( d t c p l u`), page and job footers. |
| `image` | Readers for PPM (stdin, multi-page), BMP, PNG and TIFF with row-wise access. |
| `layout` | Placement of the image on the page: resolution, fit/full/percent/location/bbox, borderless extension, horizontal resampling, mirroring, white-margin detection. A `layout.Page` is a `pipeline.PageSource`. |
| `colour` | RGB to ink amounts: slider curves in 30-bit fixed point, front and back LUT, tone curves, mode selection (`SelectMode`), gray path. |
| `halftone` | Error diffusion (single ink, cyan/magenta pair, fixed weight), ordered dither, per-page ink buffers and run derivation. |
| `raster` | Dot planes to `ESC ( F` bands: PackBits variant, zero-run prefix, band skipping, colour selection. |
| `pipeline` | Drives the stages row by row, handles copies, collation and reverse order, closes the job on errors. |
| `cif` | Composition of the above for one command line; table directory lookup. |
| `filter` | The CUPS filter: option string, PostScript header scan, PPD parser, mapping to cif switches, Ghostscript driver. |
| `status` | IVEC status session, BSCC decoder, mapping to CUPS `ATTR:`/`STATE:` lines. |
| `usbfs` | Pure Go USB access through `/dev/bus/usb` (bulk transfers, usblp detach). |
| `cupsbackend` | Backend protocol helpers: argv, log lines, device URIs, discovery filter, running the stock backend with signal forwarding. |

Commands: `mp280-filter`, `mp280-backend` (installed as `mp280usb`), `mp280-cif`, `mp280-status`.

Dependencies between packages are strictly layered: `printdb` at the bottom; `colour`, `halftone` and `raster` depend only
on it (or nothing); `options` and `jobstream` on `printdb`; `pipeline` on the pixel stages and `jobstream`; `layout` and
`cif` on top of them; `filter` on `cif`.

## Pixel path details

```
PageSource.Row(y) -> white check -> colour.Converter (RGB -> inks, 16 bit) -> halftone.Page (dot planes + runs)
                  -> raster.Encoder (ESC ( F bands) -> io.Writer, framed by jobstream.Job
```

* A `PageSource` delivers a page whose image is already placed, cropped and scaled to the printer's input grid:
  `Geometry{Width, Height, TopSkip}` and, per row, `Row{Pixels, White, LeftWhite, RightWhite}` (RGB, `0xFF` outside the image).
* `halftone.Page.BeginInputRow(TopSkip+y)` clears the error state if input rows were skipped since the last delivered row
  (the top skip, or the first row of a page). A delivered white row does not clear it.
* A white row ends there. A non-white row is converted only between `LeftWhite` and `Width-RightWhite`; the raster stage is
  invoked for every such row even if all inks come out zero.
* The colour converter has a direct-mapped 64K-entry cache for repeated colours; a row identical to the previous one is not
  converted again. A `Converter` is not safe for concurrent use.
* Pro Platinum at quality 1 (1200 dpi mode) replicates every pixel 2x2: the page is twice as wide and input row `v` becomes
  output rows `2v` and `2v+1`. Elsewhere output row equals input row.
* Every page starts with cleared halftone state and ink buffers.
* Copies: a page is written once as it is produced and its other copies immediately after (not collated); collated copies
  replay the whole document after the last page; reverse printing writes the last page first. A copy is a replay of the
  stored page bytes, page header included. Pages are only held in memory when copies or reverse order need them.
* The job is always closed once opened: if the page source fails, the bytes produced so far stay in the stream, the job
  footer and EndJob are written, and the error is returned. A failure before the first page gives a zero-page job
  (header and trailer only).

### Colour stage

```
RGB -> [slider tables] -> front LUT (8->12 bit) -> back LUT (12->14 bit) -> tone curve (14 -> 16 bit, 0..65280)
gray: gray byte -> [balance, luma, contrast+density] -> 12-bit gray -> front LUT (12->13) -> back LUT (13->14) | luma -> K -> tone curve
```

* Slider (balance, contrast, density) curves are computed in 30-bit fixed point (1.0 = 2^30) with an integer power routine
  (`fixPow`: square and multiply for the integer exponent part, repeated square roots for fractional bits). The intermediate
  truncations are part of the specification: the tables differ from an IEEE double evaluation in about 1 of 11,000 entries.
  Do not "clean up" this arithmetic.
* The front LUT's gamma node set comes from the gamma code (10/14/18/22 select node sets 1/2/3/4; the job default is 18;
  code 0 selects set 0).
* `SelectMode` maps (media, quality, gray, vivid, pattern, borderless) to LUT ids, tone tree and the per-ink halftone
  assignment (algorithm, bits per pixel, levels, tree). Borderless plain/photo media use a CMY-only mode; gray on
  postcard-class media reuses the plain quality-3 gray front LUT.
* Not implemented: head-calibration tone-curve renewal.

### Halftone stage

| Type | Role | Used for |
|---|---|---|
| `SingleInkDiffuser` | serpentine error diffusion, intensity-dependent weights and threshold | yellow, black (and gray K) |
| `PairDiffuser` | joint cyan+magenta diffusion over a 2-D quantiser grid | C and M in every mode |
| `OrderedDither` | 104x104 threshold matrix, stateless | `--halftoning pattern` (plain q3/q4) |
| `FixedWeightDiffuser` | weights 7/5/3/1 of 16, 512-entry table | yellow of Photo Paper Pro Platinum q1 |
| `Engine` | the entities of one mode and their error state | one call per output row |
| `Page` | engine plus persistent per-ink row buffers, run derivation, reset rules | called by the pipeline |

Rules the code implements:

* **Row index.** Every call gets the absolute output row from the page top (skipped and blank rows counted). It selects the
  noise/threshold row (`line mod H`) and the scan direction: single-ink diffusion runs right to left on even rows; the C/M
  pair uses right, right, left, left by `line & 3`; the fixed-weight entity uses a 4x4 table row chosen by ink id.
* **Runs.** An ink's run is `[first non-zero & ~7, min((last non-zero + 8) & ~7, W8))` with `W8` the width rounded up to 8.
  Single-ink entities process only their run and leave the error state of an ink with an empty run alone.
* **Persistent ink buffers.** Each ink has one row buffer of `W8` cells for the whole page. A row writes only its span
  `[lo,hi)`; an ink without dots clears the cells left of the span; an ink with dots clears just its rounding pads. Everything
  else stays stale. Buffers are zeroed at page start. Rows with an empty span change nothing.
* **Why it matters.** The pair entity runs over the union of the C and M runs and reads both buffers there, including stale
  cells outside one ink's own run; those cells influence the other ink's dots. Its output for an ink is kept only inside that
  ink's own run.
* **Padding.** Error and input buffers are the width rounded up to a multiple of 8 plus 8 spare cells.

### Layout stage

* Resolution: `--imageres`, else the image's x dpi; the y resolution is replaced by the x resolution whenever the image gives
  a y value; 0 means 120 dpi.
* Borderless margins come from the over-margin table or extension/3 scaling; the top shift is scaled with the x and the left
  shift with the y printer resolution. `--bbox` is ignored if empty or outside the image. `--location center` of an oversize
  image moves the offsets instead of the skips.
* Rounding: round up for sizes, truncation for offsets, skips and page width; `OutWidth` is clamped to `PageWidth-LeftSkip`.
* Rows are read from the source only when the source row changes (vertical remainder accumulator), resampled horizontally into a
  `0xFF` page row at `LeftSkip`, mirrored for back print film and T-shirt media, and checked for white.
* A plan with no output rows or columns is refused (`ErrDegenerate`).

## Filter

```
mp280-filter job user title copies options [file]      env: PPD, DEVICE_URI, optional MP280_GS, MP280_TABLE_DIR
```

Ghostscript is resolved from `$MP280_GS`, then `PATH`, then `/usr/bin`, `/usr/local/bin`, `/opt/homebrew/bin`,
`/opt/local/bin`, `/run/current-system/sw/bin`. It runs as `gs -r<res> -g<W>x<H> -q -dNOPROMPT -dSAFER -sDEVICE=ppmraw
-sOutputFile=- -` with W,H the page size in points times res/72 (single precision). SIGTERM/SIGINT cancel it. `argv[4]`
(copies) is ignored: the PPD says `cupsManualCopies: True`, CUPS repeats pages; `CNCopies` goes to cif.

Option mapping:

1. The PostScript header is scanned up to the end of the first page setup (`%%EndPageSetup`, or a line starting `gsave` or a
   digit after `%%Page:`), collecting `%%BeginFeature: *Key Value` and `/Key(Value)` items of `setpagedevice` lines
   (`CNPageSizeName` becomes `PageSize`). The scanned bytes and the rest go unchanged to Ghostscript.
2. The PPD defaults are marked, then the header settings, then the job options, with CUPS' marking rules: `media` is split on
   commas; unknown options and choices are ignored, so the default stays marked.
3. Every option in the PPD's `*%CNPpdToOptKey` table becomes a cif switch from its marked choice, lower-cased except
   `4X6`-style sizes. A `.bl` suffix on a PageSize gives `--borderless`; a `Custom` size gives
   `--papersize user --paperwidth/--paperheight` in 1/100 mm. The first occurrence of a switch wins.
4. The job options are converted again (this is how `CNHalftoning`, `CNGamma`, `CNInkCartridgeSettings`, `CNBalance*` reach
   cif; the PPD does not list them). An unknown choice (`CNQuality=9`) never overrides the PPD default. `CNGrayscale` is a bare
   flag, emitted only for True. `PageSize=Foo.bl` that is not a PPD choice still adds `--borderless`.
5. Without borderless: `--bbox` from the imageable area; always `--fit`; `position=center|top-left` becomes `--location`;
   `--bidi` when `DEVICE_URI` starts with `cnijusb:` or `cnijnet:` (Canon's bi-directional backends). `mp280usb:` and `usb:` get no `--bidi`, because the stock `usb` backend behind the wrapper is uni-directional.
6. Final command line order: `--imageres`, `--extension` (borderless only), then the switches in collection order;
   `--percent` is dropped. Values are passed as single arguments.

A job refused by the front end (an unprintable combination) gives an `ERROR:` line and exit 1. Other failures (no PPD,
Ghostscript missing or failing, tables not found) likewise.

## PPD

`ppd/canonmp280-oss.ppd` is a static asset. Option keywords, defaults and choices (CNQuality default `3` with `2/Fine`,
`3/Normal`; CNDensity and CNContrast -50..50 step 10; CNRenderIntent Photo/Vivid default Vivid; CNGrayscale Boolean; CNExtension
0..3 default 2; media types and sizes including `.bl` borderless sizes and imageable areas; UI constraints for
highres/tshirt/envelope with borderless) are the ones a saved queue configuration expects. `CNCartridge` offers Both, Black Only
and Color Only; the last two become `--inkcartridgesettings bk|color` (see KNOWN-QUIRKS). `CNBalance*` are job options only
(`-o CNBalanceC=10`). The file ends with the `*%CNPpdToOptKey` table, which the filter reads at run time: it is the contract
between PPD and filter. `*cupsModelNumber: 370` (the number only has to be at least 356 for `--bidi`).

## Option database (`options`)

`options.Configure(dir, "MP280", args)` reads `cifmp280.conf`, parses the command line, opens the model's printer database and
applies every option, returning the `Database` (selection state) and `Settings`. A refused command line returns a `*Rejection`;
`--help`, `--internalversion` and `--supportnetwork` return `ErrNoJob`.

* The database holds one entry `(object, value, default, current, pending, disabled)` per selectable option value, built from
  table 1001, defaults from table 1002.
* Two id spaces: the front end's option objects and values (media 0..17, paper size 0..40 or 1000+n, quality -6..-2, ...) and
  the tables' own ids. Values of 1000 and more pass through; table-only values come back as +1000.
* `Select(obj, value)` requires the value to exist and be enabled, then runs the dependency engine (table 1020): it builds
  the current selections (12 triples; the changed one has kind 3), evaluates the sub-tables it names and applies kind 0 disable,
  1/4 select, 2/5 enable, then repairs the paper source and the halftoning selection. The accept/reject matrix of the MP280 is
  therefore data in the tables, not code.
* Command line: long options `--name value` / `--name=value`, clustered short options (`-c -m -q -g -p`), `--`, integer
  arguments with `strtol(base 0)` semantics, last duplicate wins. Option words resolve through `cifmp280.conf`
  (first match; three tokens on a line end the search).
* Duplex/stapleside, papergap and `--tone` are refused for the MP280. Only the "separated database" format (page setup
  records in group 8 subs 2000/2001) is implemented.

## Job stream setup (`jobstream`)

`Settings` (media 0..17, paper size, quality -6..-2, gray, ...; values of 1000 and more pass through minus 1000) are resolved
by `Resolve` into a `PageSetup`:

1. Settings become database values through the option maps. A black-only cartridge (1002) forces the gray input value 2.
2. The common parameters fill a query (option values by id), look up the dither class (table 1019), apply the substitutions
   of table 1037, read the group-8 settings record (sub 2000: cartridge words and two resolution indices) and map each
   to dpi through table 1018.
3. The group-8 command record (sub 2001) holds the literal `ESC ( d`, `t`, `c` bytes. Both records are keyed by
   `key1 = quality | class<<5 | gray<<14 | cartridge<<16 | margin<<21 | duplex<<23 | nibble<<25`, `key2 = media`; a missing
   key is an unprintable combination.
4. Page geometry (table 1003 to 1007/1023/1024, keyed by paper size) builds `ESC ( p`. For a custom size (code 0x100) the
   record is recomputed from width and height (1/100 mm, limits 5500..21590 x 9100..67600) with a round-up division.
5. Table 1027 lists the string tables (1028 gives `ESC ( l`, 1032 gives `ESC ( u`).

`NewJob` validates before any output exists, so a refused job leaves the stream untouched.

## Status and CUPS integration

### usbfs

Pure Go over `/dev/bus/usb/BBB/DDD`. Devices are listed from sysfs (nothing is sent); the node is opened, its descriptors
parsed, the printer-class interface with bulk IN/OUT claimed. A bound `usblp` kernel driver is detached with
`USBDEVFS_DISCONNECT_CLAIM` restricted to `usblp` and re-attached on close; an interface owned by anyone else (a running job)
gives `ErrBusy` and is never taken over. Bulk reads use whole 512-byte packets; a timeout is `(0, nil)`. Supported: Linux on
amd64, arm (32-bit, ARMv6+), arm64 and riscv64; elsewhere every call returns `ErrUnsupported`.

### mp280usb backend

A wrapper around CUPS' stock `usb` backend. The data path stays the stock backend; the Go code only runs before and after it.

* Discovery (no arguments): runs the stock backend, keeps `direct usb://Canon/...` lines containing `MP280`
  (`MP280_MODELS=MP280,MG3600` widens) and prints them with the `mp280usb` scheme and "(with ink status)" appended to the
  info string. Exit 0 always.
* Job (5 or 6 arguments): status query; the stock backend as a child with the same arguments (argv[0] `usb:...`), environment
  (`DEVICE_URI` rewritten), stdin, file argument and back/side channel descriptors 3/4; status query again; the child's exit
  status (death by signal is re-raised). SIGTERM/SIGINT/SIGHUP are forwarded to the child and the post-job query is then
  skipped; the child gets SIGTERM if the wrapper dies (Linux `Pdeathsig`).
* Queries and child are strictly sequential (the USB interface is claimed exclusively).
* A failing query never blocks printing: every failure (no device, permission, busy, timeout, bad reply) is a `DEBUG:` line.
  A missing stock backend is the only wrapper-made failure (exit 1, `ERROR:`).
* Probe job: title `canon-status-probe` is one query, no stock backend, exit 0:
  `echo probe | lp -d QUEUE -t canon-status-probe -o raw -o job-sheets=none` (only when the queue is idle).
* Bounded delays: pre-job 5 s, post-job up to 12 s (retried once a second while the printer reports printing or busy; stops
  at once if the device is not found), probe 20 s; after StartJob was accepted up to 3 s more for EndJobResponse.
* Stock backend lookup: `$MP280_REAL_BACKEND`, `usb` next to the wrapper, `cups-config --serverbin`, `/usr/lib/cups`,
  `/usr/libexec/cups`, `/usr/local/lib/cups`.
* Environment: `MP280_REAL_BACKEND`, `MP280_MODELS`, `MP280_PRE_TIMEOUT`, `MP280_POST_TIMEOUT`, `MP280_PROBE_TIMEOUT` (seconds),
  `MP280_STATUS_DISABLE=1` (pure pass-through), `MP280_STATUS_TRANSPORT=unix:PATH|unix:@name` (software mock instead of USB).
  cupsd passes only variables set with `SetEnv`.
* Limit: a stalled job (paper out, jam, cover open) cannot be seen while it happens, because the stock backend owns the
  interface for the whole job. States appear after the job or through a probe job.
* Permissions: installed world-readable the backend runs as `lp`, which needs access to `/dev/bus/usb/BBB/DDD` (the udev rule
  for 04a9:1746 gives group lp 0660 and uaccess).

### mp280-status

`mp280-status [-uri URI] [-timeout S] [-raw|-cups|-report] [-v]`, offline `-parse FILE|-` (captured GetStatusResponse XML) and
`-parse-bscc TEXT`. Default output is JSON (`bst, printing, busy, operator_call, busy_state, job_state, cartridges,
ink[{color, level, level_raw, tank}], warnings, operator_calls, service_call, raw`). Exit: 0 ok, 1 failed, 2 usage, 3 busy,
4 not found. Never run it while the queue prints.

### Mapping to CUPS attributes

`marker-types=ink,ink` (GNOME Settings ignores `ink-cartridge`), colours `#000000` and `#00AEEF` (the tri-colour cartridge as
one colour), names `Black PG-510` and `Tri-colour CL-511` (EU PG-512/CL-513 is inferred from the region), black first, levels
straight from CIR (unknown or absent cartridge gives -1), low 10, high 100.

Managed `printer-state-reasons` (added and removed on every report, nothing else is touched): marker-supply-low-warning (DWS
1499/1500, tank LOW/LLOW, level <= 10), marker-supply-empty-error (tank EMP, DOC 1688), media-empty-error (1000),
cover-open-error (1200), media-jam-error (1300), output-tray-missing-error (1251), marker-waste-almost-full-warning
(1700/1701), marker-waste-full-error (DSC 5B00/5B01), other-error (cartridge missing or unrecognised, 1401 1403 1485 1682 1684
1686 1687, unknown DOC, other DSC). Not mapped: offline (a failed query is ambiguous), printing/cleaning states (DEBUG only),
DWS 1900, HCF. Output: one `ATTR:` line, then `STATE: +x` lines, then `STATE: -y` lines.

Evidence: the idle state with full cartridges was recorded from a real MP280. Everything about other states (empty, cover open,
jam, ...) is built from the code tables and has not been seen on hardware.
