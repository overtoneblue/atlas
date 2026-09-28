// Build-time stub for @wailsio/runtime in non-Wails builds (web, Electron).
//
// The real package defines window._wails and pokes /wails/custom.js the
// moment its module loads — pointless (and a 404) outside the Wails shell.
// vite.config.ts aliases the import here unless the build mode is "wails*";
// only the dead-at-runtime Wails transport and the generated bindings
// reference it, and none of their module code executes in those builds.
// Type-checking still targets the real package (the alias is bundler-only).

export const Events = {
  On: (_name: string, _cb: (e: { data: unknown }) => void): (() => void) => () => {},
  Off: (_name: string): void => {},
  Emit: (_name: string, _data?: unknown): void => {},
};

// Generated bindings reference these at module scope ($Call.ByID,
// CancellablePromise, $Create.Events); they must exist for the bundler to
// link, but they are never called outside the Wails shell.
export const Call = {
  ByID: (_id: number, ..._args: unknown[]): Promise<never> =>
    Promise.resolve(undefined as never),
  ByName: (_name: string, ..._args: unknown[]): Promise<never> =>
    Promise.resolve(undefined as never),
};

export class CancellablePromise<T = unknown> extends Promise<T> {
  cancel(_reason?: unknown): void {}
}

export const Create = {
  Events: [] as unknown[],
  Array: (_el: unknown): unknown[] => [],
  Map: (_el: unknown): Map<unknown, unknown> => new Map(),
  Nullable: (_el: unknown): null => null,
  Struct: (fields: Record<string, unknown> | unknown[]): unknown => fields,
  Any: (el: unknown): unknown => el,
};
