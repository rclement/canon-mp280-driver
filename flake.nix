{
  description = "Dev shell for the Canon PIXMA MP280 CUPS driver";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (s: f nixpkgs.legacyPackages.${s});
    in
    {
      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          # cups and ghostscript are needed by the filter tests and the
          # private-cupsd end-to-end scripts.
          packages = with pkgs; [
            go
            gnumake
            cups
            cups-filters
            ghostscript
          ];
        };
      });
    };
}
