// Find runtime seam. The Transcript component owns the DOM range engine
// (CSS Custom Highlight painting, scroll-to-match); state.svelte owns the
// grammar (query, cursor, n/N). They meet here so neither has to import
// the other.
export type FindRuntime = {
  recompute: (() => void) | null;
  goto: ((i: number) => void) | null;
};

export const findRuntime: FindRuntime = { recompute: null, goto: null };
