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
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          # ── Desktop app (Wails v3) ───────────────────────────────────────
          # Mirror of the Taskfile dev flow, made hermetic:
          #   1. build the Vite frontend bundle from a hash-pinned npm cache,
          #   2. embed it into the Go binaries via go:embed (frontend/dist),
          #   3. build the desktop binary (cgo + GTK4/WebKitGTK 6.0) and the
          #      server binary (pure Go, -tags server) from the same repo.
          # Bindings under frontend/bindings/ are committed; the Nix build
          # consumes them as-is (regenerate + commit via `task build` in the
          # devShell when Go bindings change).
          frontend = pkgs.buildNpmPackage {
            pname = "atlas-desktop-frontend";
            version = "1.0.0-dev";
            src = ./desktop/frontend;
            nodejs = pkgs.nodejs_22;
            npmDepsHash = "sha256-AZ/w0U6JtipUMy4KHtgzez7YgWw40P6NOII4T0wUosg=";
            installPhase = ''
              runHook preInstall
              mkdir -p $out
              cp -r dist/. $out/
              runHook postInstall
            '';
          };

          # Seed frontend/dist (relative to modRoot=desktop) before go:embed.
          seedFrontend = ''
            rm -rf frontend/dist
            mkdir -p frontend/dist
            cp -r ${frontend}/. frontend/dist/
          '';

          desktopItem = pkgs.makeDesktopItem {
            name = "atlas";
            exec = "atlas-desktop";
            desktopName = "Atlas";
            comment = "Keyboard-driven workstream client for Hermes";
            categories = [
              "Development"
              "Utility"
            ];
            startupWMClass = "atlas-desktop";
          };

          # Shared inputs for both Go builds.
          common = {
            src = ./.;
            modRoot = "desktop";
            # Only the app package itself; ./build/{android,ios} are wails3
            # mobile build scripts that don't compile on linux.
            subPackages = [ "." ];
            vendorHash = "sha256-vfetaz0eczRyX/ZqrkceyxyFW3G8ZSp2ZdoSowg0Dso=";
            ldflags = [
              "-s"
              "-w"
            ];
            preBuild = seedFrontend;
          };
        in
        {
          default = pkgs.buildGoModule {
            pname = "atlas";
            version = "0.12.0";
            src = ./.;

            vendorHash = "sha256-uwBJAqN4sIepiiJf9lCDumLqfKJEowQO2tOiSWD3Fig=";

            meta.mainProgram = "atlas";
          };

          atlas-desktop = pkgs.buildGoModule (
            common
            // {
              pname = "atlas-desktop";
              version = "1.0.0-dev";

              tags = [ "production" ];

              nativeBuildInputs = [
                pkgs.pkg-config
                pkgs.wrapGAppsHook4
              ];
              buildInputs = [
                pkgs.gtk4
                pkgs.webkitgtk_6_0
              ];

              postInstall = ''
                mv $out/bin/desktop $out/bin/atlas-desktop
                mkdir -p $out/share/applications
                cp ${desktopItem}/share/applications/*.desktop $out/share/applications/
              '';

              meta = {
                description = "Atlas desktop app (Wails v3 + Svelte): keyboard-driven workstream client for Hermes";
                mainProgram = "atlas-desktop";
                platforms = pkgs.lib.platforms.linux;
              };
            }
          );

          atlas-desktop-server = pkgs.buildGoModule (
            common
            // {
              pname = "atlas-desktop-server";
              version = "1.0.0-dev";

              tags = [
                "server"
                "production"
              ];
              env.CGO_ENABLED = 0;

              postInstall = "mv $out/bin/desktop $out/bin/atlas-desktop-server";

              meta = {
                description = "Atlas desktop app in server mode: the same UI served over HTTP (phone / any browser)";
                mainProgram = "atlas-desktop-server";
                platforms = pkgs.lib.platforms.linux;
              };
            }
          );
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/atlas";
        };
        atlas-desktop = {
          type = "app";
          program = "${self.packages.${system}.atlas-desktop}/bin/atlas-desktop";
        };
        atlas-desktop-server = {
          type = "app";
          program = "${self.packages.${system}.atlas-desktop-server}/bin/atlas-desktop-server";
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
