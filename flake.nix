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

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [ pkgs.go pkgs.gnumake ];
          };

          # Desktop (Wails v3) build environment: GTK4 + WebKitGTK 6.0,
          # pkg-config wiring, node for the frontend, go-task for wails3.
          desktop = pkgs.mkShell {
            nativeBuildInputs = [ pkgs.pkg-config ];
            buildInputs = [ pkgs.gtk4 pkgs.webkitgtk_6_0 ];
            packages = [ pkgs.go pkgs.gcc pkgs.nodejs_22 pkgs.go-task ];
          };
        });
    };
}
