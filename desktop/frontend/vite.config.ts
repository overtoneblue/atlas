import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";

// The transport is an explicit, build-time contract — never runtime-sniffed
// (importing @wailsio/runtime shims window._wails anywhere, which makes
// sniffing lie). The Wails shell builds with mode "wails*" or serves via
// `wails3 dev`; everything else — atlasd, Electron, browsers — uses
// "build:web". The flag also lets the bundler drop the unused transport.
const proc = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process;
const wailsPort = proc?.env?.WAILS_VITE_PORT;

export default defineConfig(({ command, mode }) => {
  const isWails = mode.startsWith("wails") || (command === "serve" && !!wailsPort);
  return {
    server: {
      host: "127.0.0.1",
      port: Number(wailsPort) || 9245,
      strictPort: true,
    },
    define: { __ATLAS_WAILS__: JSON.stringify(isWails) },
    plugins: [svelte(), wails("./bindings")],
  };
});
