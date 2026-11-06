# Installing

The driver is plain Go (no cgo, no libusb) and needs Canon's three MP280 data
files at run time. `make` downloads them from Canon's cnijfilter 3.40 source
archive, checks them against pinned checksums (`data/canon/SHA256SUMS`) and
`make install` puts them in place as separate files. They are under Canon's own
licence, not the GPL (see `data/canon/README`). With the archive already on disk:
`CANON_TARBALL=/path/to/cnijfilter-source-3.40-1.tar.gz make`.

Runtime dependencies on every distro: `cups`, `ghostscript` (`gs`), and
`cups-filters` (typical, for PDF/PNG/text to PostScript).

## Prebuilt releases

Each tagged release on GitHub carries prebuilt tarballs for `armv6`, `arm64` and `amd64`, plus `SHA256SUMS` and `get.sh`. The tarballs contain only
this project's files; Canon's data files are downloaded from Canon by the installer (see `data/canon/README`).

```sh
sudo apt install cups ghostscript cups-filters
curl -fsSL https://github.com/rclement/canon-mp280-driver/releases/latest/download/get.sh | sh
```

`get.sh` picks the build for the machine, verifies it against `SHA256SUMS`, then runs the tarball's `install.sh`
with sudo (which fetches Canon's data, installs everything under `/usr`, reloads udev and restarts CUPS).
`VERSION=v0.1.0` installs a specific release. To inspect first, download `get.sh` and read it, or download
`canon-mp280-driver-linux-armv6.tar.gz` from the release page, unpack it and run `sudo ./install.sh`
(`sudo ./install.sh --uninstall` removes it again; `CANON_TARBALL=` works as for `make`).

Then add the printer as described below. Releases are cut by pushing a `vX.Y.Z` tag; `make dist` builds the same
tarballs locally into `./dist`. (GitHub Actions artifacts need a GitHub login to download even on a public
repository, which is why releases are used.)

## Any Linux (Debian/Ubuntu, Fedora/RHEL, Arch, openSUSE, Alpine...)

```sh
make                                   # needs Go (see go.mod) and network access once, for Canon's data
sudo make install                      # PREFIX=/usr by default; honours DESTDIR
sudo udevadm control --reload && sudo udevadm trigger
sudo systemctl restart cups
```

`make install` asks `cups-config` for the CUPS directories (`/usr/lib/cups` on
Debian/Ubuntu/Alpine, `/usr/libexec/cups` on Fedora/Arch); override with
`CUPS_SERVERBIN=` and `CUPS_DATADIR=`. It installs:

| File | Where |
|---|---|
| `mp280-filter` | `<serverbin>/filter/` |
| `mp280usb` (status wrapper backend) | `<serverbin>/backend/` |
| `canonmp280-oss.ppd` (from `ppd/`) | `<datadir>/model/` |
| `mp280-cif`, `mp280-status` (debug/CLI) | `$PREFIX/bin` |
| `70-canon-mp280.rules` | `$PREFIX/lib/udev/rules.d` |
| systemd probe timer (optional) | `$PREFIX/lib/systemd/system` |

Canon's data files and licence texts go to `$PREFIX/share/canon-mp280/tables` and `$PREFIX/share/doc/canon-mp280`. The tables directory is also baked into the binaries as the
default; `MP280_TABLE_DIR` overrides it.

Add the printer (also after a prebuilt install): pick **Canon MP280 series (mp280usb)** as the device and
`canonmp280-oss.ppd` as the driver (GNOME Settings, `lpadmin`, or the CUPS web UI).

### Optional: refresh ink levels while idle

```sh
sudo systemctl enable --now canon-mp280-status@YOUR_QUEUE.timer
```

It submits a probe job (title `canon-status-probe`) only when the queue is idle;
the probe queries the printer and sends no data.

### Security modules

- **SELinux** (Fedora/RHEL): filters and backends normally inherit the right file
  context from the serverbin directory. If `cupsd_t` is denied usbfs access, check
  `ausearch -m avc` and add a local policy module.
- **AppArmor** (Debian/Ubuntu): the `cupsd` profile restricts what filters may exec
  (Ghostscript, the stock `usb` backend). Check `journalctl -k | grep DENIED`.

## NixOS

Build the package from `packaging/nix/package.nix` (for example through a `path:`
flake input) and enable it:

```nix
{ pkgs, ... }:
let
  mp280 = pkgs.callPackage /path/to/canon-mp280-driver/packaging/nix/package.nix { };
in
{
  nixpkgs.config.allowUnfree = true;   # Canon's data files are proprietary (fetched from Canon at build time)
  services.printing.enable = true;
  services.printing.drivers = [ mp280 ];
  services.udev.packages = [ mp280 ];
}
```

Test without a boot entry: `sudo nixos-rebuild test`.

## Architectures

amd64, arm, arm64 and riscv64 (the usbfs ioctl layout is checked per architecture); `make test-cross` verifies they compile.

## Removal

`sudo make uninstall` removes everything it installed, tables included.
