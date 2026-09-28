import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// The transport is an explicit, build-time contract — never runtime-sniffed
// (importing @wailsio/runtime shims window._wails anywhere, which makes
// sniffing lie). The Wails shell builds with mode "wails*" or serves via
// `wails3 dev`; everything else — atlasd, Electron, browsers — uses
// "build:web".
//
// Non-Wails builds alias @wailsio/runtime to a local stub and skip the Wails
// vite plugin: the real package pokes /wails/custom.js when its module loads
// (a 404, and a lying window._wails) outside the Wails shell.
const proc = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
const wailsPort = proc?.env?.WAILS_VITE_PORT;

export default defineConfig(async ({ command, mode }) => {
  const isWails = mode.startsWith("wails") || (command === "serve" && !!wailsPort);
  const plugins = isWails
    ? [svelte(), (await import("@wailsio/runtime/plugins/vite")).default("./bindings")]
    : [svelte()];
  return {
    server: {
      host: "127.0.0.1",
      port: Number(wailsPort) || 9245,
      strictPort: true,
    },
    define: { __ATLAS_WAILS__: JSON.stringify(isWails) },
    resolve: isWails
      ? {}
      : {
          alias: {
            // bundler-only stub; type-checking still sees the real package
            "@wailsio/runtime": new URL("./src/lib/wails-runtime.stub.ts", import.meta.url).pathname,
          },
        },
    plugins,
  };
});
