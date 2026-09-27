// Find runtime seam. The Transcript component owns the DOM range engine
// (CSS Custom Highlight painting, scroll-to-match); state.svelte owns the
// grammar (query, cursor, n/N). They meet here so neither has to import
// the other. msgAt(i) reports which message row a match lives in — that's
// what lets `v` start visual selection at the match (vim behavior).
export type FindRuntime = {
  recompute: (() => void) | null;
  goto: ((i: number) => void) | null;
  msgAt: ((i: number) => number) | null;
};

export const findRuntime: FindRuntime = { recompute: null, goto: null, msgAt: null };
