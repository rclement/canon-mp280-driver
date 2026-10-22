{
  lib,
  buildGoModule,
  makeWrapper,
  fetchurl,
  cups,
  ghostscript,
}:

let
  # Canon's data files are not in this repository; fetch the pinned source archive.
  canonSource = fetchurl {
    urls = [
      "https://gdlp01.c-wss.com/gds/0/0100003020/01/cnijfilter-source-3.40-1.tar.gz"
      "https://web.archive.org/web/2/https://gdlp01.c-wss.com/gds/0/0100003020/01/cnijfilter-source-3.40-1.tar.gz"
    ];
    hash = "sha256-qV3UykkanorMPk7jXMhJBGgOgtif8Eoj3aGwzP+7ino=";
  };
in
buildGoModule {
  pname = "canon-mp280-driver";
  version = "0.1.0";

  src = lib.cleanSource ../..;
  vendorHash = null;
  subPackages = [
    "cmd/mp280-cif"
    "cmd/mp280-filter"
    "cmd/mp280-backend"
    "cmd/mp280-status"
  ];
  env.CGO_ENABLED = "0";
  ldflags = [
    "-s"
    "-w"
    "-X canon-mp280-driver/internal/cif.systemDir=${placeholder "out"}/share/canon-mp280/tables"
  ];

  nativeBuildInputs = [ makeWrapper ];

  preBuild = ''
    CANON_TARBALL=${canonSource} sh scripts/fetch-canon-data.sh data/canon
  '';

  postInstall = ''
    mkdir -p $out/lib/cups/filter $out/lib/cups/backend $out/share/cups/model \
             $out/share/canon-mp280/tables $out/lib/udev/rules.d

    mv $out/bin/mp280-filter  $out/lib/cups/filter/mp280-filter
    mv $out/bin/mp280-backend $out/lib/cups/backend/mp280usb

    # Canon's data files (proprietary, see data/canon/README): separate files read at run time.
    install -m 0644 data/canon/cnb_3700.tbl data/canon/cnbpname370.tbl data/canon/cifmp280.conf $out/share/canon-mp280/tables/
    install -D -m 0644 data/canon/README data/canon/LICENSE-cnijfilter-3.40*.txt -t $out/share/doc/canon-mp280/

    install -m 0644 packaging/udev/70-canon-mp280.rules $out/lib/udev/rules.d/
    install -m 0644 ppd/canonmp280-oss.ppd $out/share/cups/model/

    wrapProgram $out/lib/cups/filter/mp280-filter --set MP280_GS ${ghostscript}/bin/gs
    wrapProgram $out/lib/cups/backend/mp280usb --set MP280_REAL_BACKEND ${cups}/lib/cups/backend/usb
  '';

  meta = {
    description = "CUPS driver for the Canon PIXMA MP280 (filter, status backend, PPD)";
    # GPLv2-only code; Canon's data files are fetched from Canon at build time under Canon's own terms.
    license = [
      lib.licenses.gpl2Only
      lib.licenses.unfreeRedistributable
    ];
    platforms = [
      "x86_64-linux"
      "aarch64-linux"
    ];
    mainProgram = "mp280-cif";
  };
}
