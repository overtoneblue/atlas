{
  description = "Atlas — keyboard-driven workstream client for Hermes";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "x86_64-darwin"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system);
    in
    {
      packages = forAllSystems (system: {
        default = nixpkgs.legacyPackages.${system}.buildGoModule {
          pname = "atlas";
          version = "0.12.0";
          src = ./.;

          vendorHash = "sha256-uwBJAqN4sIepiiJf9lCDumLqfKJEowQO2tOiSWD3Fig=";

          meta.mainProgram = "atlas";
        };
      });

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/atlas";
        };
      });

      devShells = forAllSystems (system: {
        default = nixpkgs.legacyPackages.${system}.mkShell {
          packages = [ nixpkgs.legacyPackages.${system}.go nixpkgs.legacyPackages.${system}.gnumake ];
        };
      });
    };
}
