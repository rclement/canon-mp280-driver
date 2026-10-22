# Printer protocol and data formats

Everything here is little endian unless stated.

## Job stream

```
[IVEC StartJob XML 289 B]  [IVEC ModeShift XML 391 B]         only when the job carries IVEC (cif does, unless --bidi)
SSR definition      ESC [ K 0b 00 | 00 1e 00 09 "SSR=DF;"                      16 B
BJL block           ESC [ K 02 00 00 1f "BJLSTART\nControlMode=Common\nSetTime=YYYYMMDDhhmmss\nBJLEND\n" ESC [ K 02 00 00 0f   72 B
per page:
  page 1 prefix     ESC ( a 01 | ESC ( b 01 | ESC ( q 01                        18 B (first page of a job only)
  setup commands    ESC ( d (x,y dpi) | ESC ( t (42 B print mode) | ESC ( c (3 B) | ESC ( p (46 B geometry) | ESC ( l | ESC ( u
  raster layer      ESC ( b ... ESC ( F bands (a white page only has ESC ( I / ESC ( J)
  page footer       ESC ( e 00 01 , 0x0c form feed                                8 B
job footer          ESC ( b 00 | ESC ( a 00 | ESC @                              14 B
[IVEC EndJob XML 263 B]
```

Framing: `ESC ( X <len:u16le> payload` for printer commands, `ESC [ K <len:u16le> payload` for language-monitor (BJL) commands,
plain text for the XML documents. `SetTime` is the local time at job start (year printed modulo 10000, negative years
refused); it is the only part of a stream that differs between runs, and the golden tests mask it.

A copy is a replay of the stored bytes of the page, header (including `ESC ( a b q`) and all; it is not a second page of the
job.

### IVEC documents

All start with `<?xml version="1.0" encoding="utf-8" ?><cmd xmlns:ivec="http://www.canon.com/ns/cmd/2008/07/common/"`; the
ModeShift document also declares the `vcn` namespace (`.../canon/`). The job id is eight characters; the default is
`00000001`, and the USB language monitor substitutes the id the printer returns.

* `StartJob`: `<ivec:operation>StartJob</ivec:operation>`, param set `servicetype="print"` with `jobID` and `bidi` (0 from the
  front end, 1 when the host will read status back).
* `ModeShift`: operation `VendorCmd`, `<vcn:ijoperation>ModeShift</vcn:ijoperation><vcn:ijmode>1</vcn:ijmode>` switches the
  printer into print mode after StartJob.
* `EndJob`: operation `EndJob` with the job id.

### Raster layer

Input is one scanline per ink plane of halftoned dots (pixels MSB first, `bits per pixel` each); output is the part of the
stream from `ESC ( b` (page raster follows) to the last `ESC ( F`.

Mode and bit depth. The parameters (media code, quality code 5 = quality 2, 10 = quality 3, 15 = quality 4, grayscale flag,
banner flag, head type, borderless flag, one extra flag) select a plane mode (1 = K, 2/3/4 = CMY, 5 = CMYK) and bits per
pixel per plane:

| setting | C M Y K bits |
|---|---|
| plain/postcard/envelope quality 3 | 2 2 1 1 |
| quality 2 | 2 2 2 2 |
| quality 4 (300 dpi) | 1 1 1 1 |
| photo media (CMY, mode 2) q2/q3 | 2 2 2 |
| borderless plain (mode 2) | 2 2 1 |
| grayscale (mode 1, K only) q2 / q3 / q4 | 2 / 1 / 1 |

Banner mode, interleaved planes and plane modes 6-9 do not occur on the MP280 and are not implemented.

Per page:

1. First row: `ESC ( b 01 00 01`, `ESC ( I 01 00 01`, `ESC ( J 01 00 08` (8 rows per band). A page with no rows only emits
   `ESC ( I` and `ESC ( J` at flush.
2. Rows are grouped in bands of 8 (`band = row/8`, `slot = row%8`). Per plane a band buffer is `ESC ( F len16` followed by
   8 lines. A line is `[zero-run of L bytes][packbits(bytes L..R)] 0x80`; a blank row is a lone `0x80`; missing rows of a
   band are padded with `0x80`. L and R are the first and last non-zero byte of the row inside the valid window; trailing
   zeros are not sent.
3. When a row of a later band arrives, the previous band is emitted. A plane is blank if its payload is empty or exactly
   8 bytes (eight `0x80`). If every plane is blank nothing is emitted and the pending skip counter grows. Otherwise:
   `[ESC ( e 02 00 hi lo]` for pending blank bands (at most 0x7fff per command), `ESC ( L` colour select when the state
   changed (K only `4b`; mode 2 `CMY`; mode 3/4 `c3 cd d9`; mode 5 `CMYK`), then the `ESC ( F` buffer of every plane (state
   "K only": only the K plane, which happens when only K has dots in mode 5/1). Planes without dots in an emitted band are sent
   as bare `ESC ( F 00 00`.
4. A jump of more than one band emits a separate `ESC ( e` for the blank bands in between; the first row at band `k`
   yields a skip of `k` before the first emitted band.
5. Flush pads and emits the last band.

PackBits variant: control byte `n < 0x80` is `n+1` literals, `n > 0x80` repeats the next byte `257-n` times; greedy runs;
runs over 128 are split into `81 c` chunks; a run of two inside a literal is folded in while the literal stays under 128
bytes; a run of three or more closes the literal; a single byte after a repeat opens a new literal. Zero-run prefix: `81 00`
per 128 bytes, then `(1-n) 00`.

Quality code 0 (`--quality 1`) with media 0x33 (`--media proplatinum`) selects 16 rows per band: `ESC ( J` carries 16 and
band number and slot use `row/16` and `row%16`. Every other combination uses 8.

Errors in raster input (negative start, plane data shorter than the window, tail longer than the window) return an error
and discard the call's output.

### Page setup commands

Derived as described under "Job stream setup" in ARCHITECTURE.md: `ESC ( d` resolution, `ESC ( t` print mode (42 bytes),
`ESC ( c` (3 bytes) and the cartridge words come from the database's command record; `ESC ( p` (46 bytes) is the page
geometry for the paper size; `ESC ( l` and `ESC ( u` are byte strings from the setup string tables.

## Status session (IVEC over USB bulk)

```
StartJob (bidi=1) -> StartJobResponse -> ModeShift -> VendorCmdResponse(ModeShiftResponse) -> GetStatusResponse (valid)
  -> EndJob (always sent once StartJob was accepted) -> EndJobResponse (grace 5 s, in the wrapper 3 s)
```

The request documents are those of the job stream. The printer echoes the job id (`00000001`) and streams one status document
per read (hundreds per session, so examined documents are dropped); no wake-up bytes are needed. The whole session is bounded
by a deadline.

Responses are complete `<?xml ... </cmd>` documents, parsed with `encoding/xml`. Any namespace prefix, CDATA or plain
`ReadData`, and junk before `<?xml` are accepted. A status is valid when the operation is `GetStatusResponse`, the response is
`OK`, `NumberOfBytes` is all digits, non-zero and equal to `len(ReadData)`. Only direct children of `param_set` count (the
nested `jobstatus/jobID` of a running job is ignored).

`ReadData` carries the BSCC text, `KEY:value;...`. Used keys: `BST` (hex; 0x80 printing, 0x20 busy, 0x08 operator call),
`DBS`, `DJS`, `CHD` (colour cartridge first), `CIR` (`CL=100,BK=070`; steps 100/070/040/010/000 or UK), `CTK`
(`CL,SET,/,BK,SET`), `DWS`, `DOC` (`4,01,1000,/,4,01,1200`: every 4th token from index 2), `DSC`, `HRI`, `HCF`. Field lengths
are clipped to the printer-side buffer sizes.

## Printer database (`cnb_3700.tbl`)

Container: u32 values at file offsets 8, 12, 16 and 20 point at the directories of groups 8, 12, 16 and 20. A directory is
`u32 count` followed by `count x {group, sub, file_offset}`; an entry is `u32 len` plus payload.

| Group | Content |
|---|---|
| 8 | settings and command records (subs 2000, 2001) |
| 12 | resource tables (ids 1000 and up) |
| 16 | colour LUT blobs |
| 20 | block trees: halftone parameters and tone curves |

Codecs:

* **LZW**: TIFF-style, MSB first, 9..12 bits, `0x100` clear, `0x101` end, early width change, end of input is treated as
  the end code, the first code after a clear adds no entry.
* **Digest**: 32-bit integrity hash (`Digest(p, seed)`); delta LUTs store the digest of their base.
* **Block trees** (group 20): deobfuscate (self-keyed XOR; the key is derived from a byte-lane checksum and the trailing
  word), LZW expand, undo the running-sum array coding, recurse into nested trees (re-laying out the parent when a nested tree
  changes size). Tree entries are a 16-byte header followed by 8-byte `{tag, value}` entries. The tone-curve tree holds as
  entry 1 the number of curves and from entry 2 one nested tree per curve: `{name, ink id, 0, 0, max, samples}` with 256
  samples.
* **LUT blobs** (group 16): plain and delta entries (the base is found through the sub-1 directory, the digest checked);
  header 2x running sum, optional 3D prefix sum of each N^3 plane. Decoded blob types: 0 built-in nodes, 1 own nodes, 2 front
  with node sets, 3 back with ink channel ids.

Resource tables (group 12): a 48-byte header of u16 words (`+0` type: 0 indirection, 1 raw blob, 2 first matching record, 3 all
matching records; `+2` status, 0 or 2 normal; `+4` key columns; `+6` elements per record; `+8` element size; `+10` record
count; `+16` key slots, one u16 per column, naming which entry of the query is compared; for blobs a u32 at +16 holds the blob
size), followed from offset 48 by the records: key values (s16, `0x8000` matches anything) and data. An indirection table's
single matching record holds the id of the table to continue with. The query is an array of option values indexed by option id.

3D LUT engine: an N^3 grid with possibly non-uniform node positions, up to 24 output channels, a cell index per top-8-bit
input, a 16-bit fraction per input value (linear in the input inside a cell, so non-uniform nodes are exact), tetrahedral
interpolation with weights summing to 0x10000 and a rounding shift of `24 - bitsOut`. The database stores LUTs with the first
(red) channel as the slowest axis.
