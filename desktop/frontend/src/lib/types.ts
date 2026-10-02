// Shared types mirroring what the Go DataService returns.
// (The generated bindings carry their own copies; these keep the frontend
// readable and independent of generator output.)

export type SpawnItem = {
  kind: "subagent" | "pi" | "debbie";
  id: string;
  profile?: string;
  parent?: string;
  parent_chat?: string;
  state: "running" | "done" | "failed" | "unknown";
  title: string;
  started?: number;
  completed?: number;
  tasks?: number;
  has_log?: boolean;
  rc?: number | null;
};

export type SpawnList = {
  items: SpawnItem[];
  errors?: string[];
};

export type HubNode = {
  kind: "profile" | "guild" | "category" | "channel" | "post" | "spawn";
  name: string;
  profile?: string;
  session_id?: string;
  chat_type?: string;
  last_active?: number;
  message_count?: number;
  pinned?: boolean;
  hidden?: boolean; // archived in Hermes (hidden flag) — Atlas renders it only in hidden view
  // native (hub-owned) nodes: id + template on categories/channels, source +
  // channel_id on posts — the native channel management UI keys off these
  id?: string;
  native?: boolean;
  template?: string;
  source?: string;
  channel_id?: string;
  category_id?: string | null;
  children?: HubNode[];
  // set only on synthetic "spawn" rows (client-built, nested under a post)
  spawn?: SpawnItem;
};

export type HubTree = {
  ok?: boolean;
  sections: HubNode[];
  errors?: string[];
};

export type Session = {
  id: string;
  source: string;
  title: string;
  preview: string;
  message_count: number;
  last_active: number;
  pinned: boolean;
  hidden: boolean;
  archived: boolean;
};

export type Message = {
  id: number;
  role: string;
  content: string;
  // "hidden" on channel-guideline seed rows: model-facing only, never painted
  display_kind?: string;
  tool_name: string;
  tool_calls?: { function?: { name?: string; arguments?: string } }[] | null;
  reasoning: string;
  timestamp: number;
  // data URLs of pasted images on locally-echoed user messages (echo only;
  // history re-renders from MEDIA: refs via atlasd's /media relay)
  images?: string[];
};

// One upstream's reachability (configured is not the same as up).
export type LinkHealth = {
  configured: boolean;
  up: boolean;
  error?: string;
  checked_at?: number;
};

export type Status = {
  api: boolean;
  hub: boolean;
  serve?: boolean;
  api_url: string;
  hub_url: string;
  serve_url?: string;
  // Live turns the daemon is currently running (attach targets).
  turns?: { session: string; profile: string }[];
  // Real reachability per upstream (api / hub / serve), daemon identity.
  links?: Record<string, LinkHealth>;
  version?: string;
  boot?: string;
  uptime_s?: number;
};

// A chat's live snapshot from atlasd (serve's session.info + usage, or the
// stored route when no runtime is bound). Zero values mean "unknown".
export type SessionInfo = {
  session: string;
  profile?: string;
  live?: boolean;
  bound?: boolean;
  model?: string;
  provider?: string;
  reasoning?: string;
  service_tier?: string;
  fast?: boolean;
  context_used?: number;
  context_max?: number;
  context_percent?: number;
  context_estimated?: boolean;
  tps?: number;
  latency_s?: number;
  cache_hit_pct?: number;
  calls?: number;
  drift?: string; // stored route would resume on the wrong provider
  healed?: string; // atlasd re-pinned the route (human text)
  bind_error?: string;
  updated_at?: number;
};

// A chat's most recent failed turn, as serve explained it.
export type TurnFailure = {
  error: string;
  code?: string;
  at: number;
};

// Live-turn attach (atlasd GET /api/turn): a client opening mid-turn
// hydrates its live view from this so the whole turn so far renders.
export type TurnSegment = {
  type: "text" | "tool";
  text?: string;
  name?: string;
  state?: string;
};
export type TurnState = {
  active: boolean;
  session_id?: string;
  profile?: string;
  segments?: TurnSegment[];
};

// Slash-command surface (hermes-serve via atlasd): the catalog powers the
// palette, complete.slash the live fuzzy matches, slash.exec the runs.
export type CatalogMeta = { argument_mode?: string | null; desktop?: string | null };

export type Catalog = {
  pairs: [string, string][];
  categories: { name: string; pairs: [string, string][] }[];
  commands: Record<string, CatalogMeta>;
  // first-argument words per command ("/reasoning" -> ["none", …, "high"])
  sub?: Record<string, string[]>;
  // alias -> canonical command ("/bp" -> "/blueprint")
  canon?: Record<string, string>;
  skills: Record<string, { usage: number; origin: string }>;
  skill_count?: number;
};

export type Completion = {
  text: string;
  display?: string;
  meta?: string;
  kind?: string; // command | skill
};

export type ExecResult = {
  output?: string;
  warning?: string;
  // command.dispatch directive fields (present when the command was rerouted)
  type?: string;
  target?: string;
  message?: string;
  notice?: string;
  display?: string;
  name?: string;
  status?: string;
};

// model.options payload (hermes-serve `model.options`, proxied by atlasd).
// `model`/`provider` are the session's current selection; `providers` rows
// carry their model ids. Extra serve fields (pricing, capabilities, …) are
// preserved for forward compatibility.
export type ModelProviderRow = {
  slug: string;
  name?: string;
  is_current?: boolean;
  models?: string[];
  total_models?: number;
  [k: string]: unknown;
};

export type ModelOptions = {
  model?: string;
  provider?: string;
  providers?: ModelProviderRow[];
  [k: string]: unknown;
};

// One flattened picker row: a model id plus its owning provider.
export type ModelRow = {
  name: string;
  slug: string;
  meta: string;
  current: boolean;
  recent?: boolean; // one of the last few picks (floats to the top)
};

export type Row = {
  node: HubNode;
  key: string;
  depth: number;
};

export type Focus = "tree" | "chat" | "composer";

// Native shape store (atlas-hub channels.json) — categories, channels with
// their guidelines, and chat -> channel assignments.
export type ChannelCategory = { id: string; name: string; order?: number };
export type ChannelInfo = {
  id: string;
  category_id: string | null;
  name: string;
  template: string;
  order?: number;
};
export type ChannelStore = {
  profile: string;
  categories: ChannelCategory[];
  channels: ChannelInfo[];
  assign: Record<string, string>;
};

// One modal slot: the channel/category form or the move-to-channel picker.
export type ModalState = {
  kind: "channel" | "category" | "move";
  mode: "new" | "edit";
  profile: string;
  id?: string;
  name?: string;
  template?: string;
  categoryId?: string;
  newCategory?: string;
  session?: string;
  title?: string;
  current?: string | null;
  error?: string; // last save/delete failure, shown inside the form
};

// One live-turn update, emitted by the Go side on "atlas:turn".
// kinds: started | delta | reasoning | tool | done | error | stats | info |
// note | link | resync | hello — plus the client-side "stream" (the event
// stream itself went down / came back).
export type TurnEvent = {
  kind: string;
  session_id: string;
  profile?: string;
  text?: string;
  tool?: string;
  tool_state?: "running" | "done" | string;
  run_id?: string;
  ok?: boolean;
  stopped?: boolean;
  error?: string;
  code?: string;
  tps?: number;
  latency_s?: number;
  info?: SessionInfo;
  link?: "up" | "down" | string;
  boot?: string;
};

export type LiveSegment =
  | { type: "text"; text: string }
  | { type: "reasoning"; text: string }
  | { type: "tool"; name: string; state: string };

export type LiveTurn = {
  session: string;
  profile: string;
  segments: LiveSegment[];
  error: string;
  tps?: number; // rolling output tokens/sec from serve session.info
};

