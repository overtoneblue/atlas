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
          # ONE version for every output (bundle chip, atlasd ldflags, pkgs)
          atlasVersion = "0.21.0";

          # ── Web UI bundle ────────────────────────────────────────────────
          # The transport is a BUILD-TIME contract (vite define
          # __ATLAS_WAILS__): only the retired Wails shell used the "wails*"
          # modes; atlasd, Electron and browsers all build with "build:web"
          # (mode web).
          frontend = pkgs.buildNpmPackage {
            pname = "atlas-web";
            version = atlasVersion;
            src = ./desktop/frontend;
            nodejs = pkgs.nodejs_22;
            # baked into the bundle (status bar version) via vite define
            env.ATLAS_VERSION = atlasVersion;
            npmDepsHash = "sha256-AZ/w0U6JtipUMy4KHtgzez7YgWw40P6NOII4T0wUosg=";
            npmBuildScript = "build:web";
            installPhase = ''
              runHook preInstall
              mkdir -p $out
              cp -r dist/. $out/
              runHook postInstall
            '';
          };

          # ── atlasd: the bridge daemon (pure Go) ──────────────────────────
          atlasd = pkgs.buildGoModule {
            pname = "atlasd";
            version = atlasVersion;
            src = ./.;
            subPackages = [ "./cmd/atlasd" ];
            ldflags = [ "-X main.version=${atlasVersion}" ];
            vendorHash = "sha256-uwBJAqN4sIepiiJf9lCDumLqfKJEowQO2tOiSWD3Fig=";
            env.CGO_ENABLED = 0;
            meta.mainProgram = "atlasd";
          };

          desktopItem = pkgs.makeDesktopItem {
            name = "atlas";
            exec = "atlas-electron";
            icon = "atlas";
            desktopName = "Atlas";
            comment = "Keyboard-driven workstream client for Hermes";
            categories = [
              "Development"
            ];
            startupWMClass = "Atlas";
          };
        in
        {
          # Static web bundle — served by head for the phone (PWA surface).
          atlas-web = frontend;

          # TUI + daemon: what head runs (historical shape, unchanged).
          default = pkgs.buildGoModule {
            pname = "atlas";
            version = atlasVersion;
            src = ./.;
            subPackages = [ "." "./cmd/atlasd" ];
            ldflags = [ "-X main.version=${atlasVersion}" ];
            vendorHash = "sha256-uwBJAqN4sIepiiJf9lCDumLqfKJEowQO2tOiSWD3Fig=";
            meta.mainProgram = "atlas";
          };

          # ── atlas-electron: the desktop app ─────────────────────────────
          # Electron shell + atlasd + web UI in one store path, with a
          # wrapper and a desktop entry. The wrapper sources
          # ~/.config/atlas/env (the per-user API URL/key boundary) and pins
          # the store paths main.js discovers via ATLASD_BIN / ATLAS_WEB_DIR.
          atlas-electron = pkgs.stdenv.mkDerivation {
            pname = "atlas-electron";
            version = atlasVersion;
            src = ./.;

            # Pure assembly derivation: everything is built by the deps.
            dontConfigure = true;
            dontBuild = true;

            nativeBuildInputs = [
              pkgs.makeWrapper
              pkgs.copyDesktopItems
            ];
            desktopItems = [ desktopItem ];

            installPhase = ''
              runHook preInstall

              mkdir -p $out/bin $out/share/atlas-electron/web $out/share/atlas-electron/electron
              mkdir -p $out/share/icons/hicolor/1024x1024/apps

              install -m 0755 ${atlasd}/bin/atlasd $out/bin/atlasd
              cp -r electron/. $out/share/atlas-electron/electron/
              cp -r ${frontend}/. $out/share/atlas-electron/web/
              cp desktop/build/appicon.png $out/share/icons/hicolor/1024x1024/apps/atlas.png

              makeWrapper ${pkgs.electron}/bin/electron $out/bin/atlas-electron \
                --add-flags "$out/share/atlas-electron/electron" \
                --set ATLASD_BIN "$out/bin/atlasd" \
                --set ATLAS_WEB_DIR "$out/share/atlas-electron/web" \
                --set-default ATLAS_UPSTREAM "http://127.0.0.1:8645" \
                --run '[ -f "$HOME/.config/atlas/env" ] && { set -a; . "$HOME/.config/atlas/env"; set +a; } || true'

              runHook postInstall
            '';

            meta = {
              description = "Atlas desktop app (Electron): keyboard-driven workstream client for Hermes";
              mainProgram = "atlas-electron";
              platforms = pkgs.lib.platforms.linux;
            };
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/atlas";
        };
        atlas-electron = {
          type = "app";
          program = "${self.packages.${system}.atlas-electron}/bin/atlas-electron";
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

          # Frontend/atlasd work: node for the vite build, go for the daemon.
          desktop = pkgs.mkShell {
            packages = [ pkgs.go pkgs.gcc pkgs.nodejs_22 ];
          };
        });
    };
}
