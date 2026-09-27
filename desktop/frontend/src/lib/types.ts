// Shared types mirroring what the Go DataService returns.
// (The generated bindings carry their own copies; these keep the frontend
// readable and independent of generator output.)

export type HubNode = {
  kind: "profile" | "guild" | "category" | "channel" | "post";
  name: string;
  profile?: string;
  session_id?: string;
  chat_type?: string;
  last_active?: number;
  message_count?: number;
  pinned?: boolean;
  children?: HubNode[];
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
};

export type Status = {
  api: boolean;
  hub: boolean;
  api_url: string;
  hub_url: string;
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

