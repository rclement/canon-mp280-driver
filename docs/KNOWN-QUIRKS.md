# Known quirks

Behaviours that are kept on purpose. They are visible in the printer stream or in which jobs are accepted, so the golden
fixtures depend on them. Change one only with a reason, and expect fixtures to change with it.

## Refused combinations

The accept/reject matrix lives in the printer database (see the option dependency engine in ARCHITECTURE.md), so the list
below is what the MP280 data says, not code:

* `--duplex` and `--stapleside`, `--papergap` and `--tone` are refused.
* `--renderintent graphics` is refused (only photo and vivid).
* `--fit` together with `--full` is refused; `--extension` is only valid with borderless.
* Quality 4 with glossy media, and other media/quality/paper size pairs the tables mark as unavailable (for example
  `highres`, `tshirt` and envelope media with borderless sizes), are refused.
* A custom size outside 5500..21590 x 9100..67600 (1/100 mm) is refused with a range error.
* `mp280-cif` writes nothing and exits 0 when the options are refused (no job is started). The filter turns a refusal into an
  `ERROR:` line and exit status 1.
* The PPD offers the same three `CNCartridge` choices as the original (Both, Black Only, Color Only), but Black Only and Color Only do not become `--cartridge bk|color`: cif refuses that for the MP280 ("inappropriate cartridge selection"), so the original driver silently prints nothing. The filter maps them to `--cartridge bkcolor --inkcartridgesettings bk|color`, which prints with just that ink (deliberate deviation).
* A saved queue value that is not a PPD choice (`CNQuality=9`) never overrides the PPD default.

## Raster and halftone

* Quality 1 on Photo Paper Pro Platinum (`--media proplatinum --quality 1`, the 1200 dpi mode) uses 16-row bands instead of 8
  (`ESC ( J` carries 16) and doubles every pixel in both directions.
* The halftone error state and the ink row buffers are cleared at the start of every page, including pages of one job.
* The error state is cleared when input rows were skipped (the top skip); a delivered white row neither clears it nor touches
  the error buffers.
* The cyan/magenta pair reads stale buffer cells outside one ink's own run; those cells affect the other ink's dots. Do not
  "optimise" them away.
* A non-white row always reaches the raster stage, even when every ink amount is zero.
* The slider curves use 30-bit fixed point with the original's truncations; they differ from a floating point evaluation in
  about 1 of 11,000 entries.
* Grayscale with a black-only cartridge: gray value 2 ("forced") does not count as a gray job for mode selection.

## Page layout

* The y resolution is replaced by the x resolution whenever the image reports a y value. No resolution means 120 dpi.
* The borderless top shift is scaled with the x printer resolution and the left shift with the y resolution.
* Pixel sizes other than 1, 3, 4, 6 and 7 bytes are not resampled (the row stays white).
* A plan with no output rows or columns is refused.
* Back print film and T-shirt media print mirrored.

## Image readers

* PPM: header parsed with C `atol`-style tokenising (`P6`, one arbitrary byte, optional `#` comment line, `W H` line, `maxval`).
  A `0xFF` byte ends a loop like EOF. `maxval` must be 255. Missing pixel data reads as 0 (at least one complete row must
  exist). Dimensions are kept in 16 bits: an image wider or higher than 32767 opens but every row request fails. Data after
  a page is read as the next page's signature; anything but `P6` fails that page and the earlier pages stay. A later page of a
  stream that is cut short reads the missing rows as zero (black).
* PPM on standard input is the only multi-page source; a PPM file delivers its first page. Standard input accepts only PPM.
* BMP: 24 bit, pixel data offset 54, positive height (bottom-up). The row length comes from `(file size field - 54) / height`,
  not from the width; compression and header size are not checked. Truncated data reads as zero.
* PNG: non-interlaced only; 16 bit becomes the high byte; alpha and tRNS are dropped without compositing; palette and
  low-depth images are expanded; pHYs and gAMA are ignored. Fatal: bad IHDR or size above 1,000,000, unknown critical chunk, bad
  CRC of a critical chunk, missing PLTE, damaged zlib data, bad filter byte. Damage in ancillary chunks is ignored.
* TIFF: classic TIFF only (no BigTIFF), first directory, uncompressed, photometric 0, 1 or 2 (photometric 3 without a
  ColorMap is treated as RGB), one byte per sample, 16-bit samples of big-endian files byte-swapped. Planar configuration 2 is
  read properly. Unreadable scanlines are not errors: the row is zero. Tag validation covers only the critical tags
  (256, 257, 258, 259, 262, 277, 278, 284).
* Pages that print as a single raster row (height times printer dpi not above the image resolution) print that one row.

## Filter

* `argv[4]` (copies) is ignored; CUPS repeats pages (`cupsManualCopies: True`); `CNCopies` goes to cif.
* The first occurrence of a switch wins. `--percent` is dropped. Values are passed as single arguments (no shell splitting).
* The `setpagedevice` header parser stops at the end of the line.
* `PageSize=Foo.bl` that is not a PPD choice still adds `--borderless`.

## Status

* `marker-types=ink`, one colour per marker (the tri-colour cartridge is a single marker), black first.
* Offline is never reported (a failed query is ambiguous). Printing and cleaning states are logged only.
* A stalled job cannot be observed while it runs (the stock backend owns the interface); states appear after the job or on a
  probe job.
