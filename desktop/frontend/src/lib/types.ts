// Shared types mirroring what the Go DataService returns.
// (The generated bindings carry their own copies; these keep the frontend
// readable and independent of generator output.)

export type SpawnItem = {
  kind: "subagent" | "pi";
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
  tool_name: string;
  tool_calls?: { function?: { name?: string; arguments?: string } }[] | null;
  reasoning: string;
  timestamp: number;
  // data URLs of pasted images on locally-echoed user messages (echo only;
  // history re-renders from MEDIA: refs via atlasd's /media relay)
  images?: string[];
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

export type Row = {
  node: HubNode;
  key: string;
  depth: number;
};

export type Focus = "tree" | "chat" | "composer";

// One live-turn update, emitted by the Go side on "atlas:turn".
export type TurnEvent = {
  kind: "started" | "delta" | "tool" | "done" | "error" | string;
  session_id: string;
  profile?: string;
  text?: string;
  tool?: string;
  tool_state?: "running" | "done" | string;
  run_id?: string;
  ok?: boolean;
  stopped?: boolean;
  error?: string;
};

export type LiveSegment =
  | { type: "text"; text: string }
  | { type: "tool"; name: string; state: string };

export type LiveTurn = {
  session: string;
  profile: string;
  segments: LiveSegment[];
  error: string;
};

