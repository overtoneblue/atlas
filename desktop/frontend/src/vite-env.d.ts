/// <reference types="svelte" />
/// <reference types="vite/client" />

// Injected by vite.config.ts: true exactly when the bundle is built for the
// Wails shell (mode "wails*" or a wails3 dev session).
declare const __ATLAS_WAILS__: boolean;
