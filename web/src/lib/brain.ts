// Client for the brain plugin's console API (/api/brain/*).
import { API } from "./api";
import { authHeaders } from "./auth";

export type Stats = {
  ready: boolean;
  brains: number;
  memories: number;
  entities: number;
  edges: number;
  agents: number;
  sessions24h: number;
  recalls24h: number;
  openGaps: number;
};

export type ActivityItem = {
  id: number;
  ts: string;
  op: string;
  namespace: string;
  agentId: string;
  outcome: "hit" | "empty" | "error" | "running" | string;
  latencyMs: number;
};

export type NamespaceInfo = { namespace: string; memories: number; lastAt: string };

// Derived hierarchy graph: root -> type nodes -> entity nodes.
// `group` is "root" | "type" | "<typename>" (used to color nodes).
export type GraphNode = { id: string; name: string; group?: string };
export type GraphEdge = { source: string; target: string };
export type GraphData = {
  ready: boolean;
  derived?: boolean;
  nodes: GraphNode[];
  edges: GraphEdge[];
};

// A full memory row, as returned by GET /api/brain/memory?namespace&id.
export type Memory = {
  id: string;
  namespace: string;
  content: string;
  network?: string;
  memoryType?: string;
  sourceKind?: string;
  sourceRef?: string;
  importance?: number;
  visibility?: string;
  validAt?: string;
  metadata?: Record<string, unknown>;
};

export type Recalled = {
  id: string;
  content: string;
  score: number;
  network: string;
  memoryType: string;
  sourceKind: string;
  sourceRef: string;
  importance: number;
  validAt: string;
  viaEntity?: string;
  // Present on cross-brain /search results (which brain the hit came from).
  namespace?: string;
};

export type Gap = {
  id: number;
  namespace: string;
  query: string;
  hits: number;
  status: "open" | "indexed" | "dismissed" | string;
  resolution?: string;
  firstSeen: string;
  lastSeen: string;
};

export type GapStatus = "indexed" | "dismissed" | "open";

// Per-brain access grant attached to an access token's agent.
export type Grant = {
  agentId: string;
  namespace: string;
  canRead: boolean;
  canWrite: boolean;
};

// Access token — the secret an agent puts in CABRAIN_TOKEN. `token` is the raw
// secret and is only fully returned once (on create); the list may mask it.
export type Token = {
  token: string;
  agentId: string;
  label: string;
  isAdmin: boolean;
  createdAt: string;
  lastUsedAt: string;
  revoked: boolean;
  grants: Grant[];
};

// Per-brain secret metadata. The list NEVER carries the decrypted value — `hint`
// is a masked preview (e.g. "sk-…mnop"); the value is fetched lazily via reveal.
export type SecretMeta = {
  namespace: string;
  name: string;
  hint: string;
  kind: string;
  sourceRef?: string;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
};

export type BrainDetail = {
  namespace: string;
  memories: number;
  types: Record<string, number>;
  sources: Record<string, number>;
  openGaps: number;
  recalls: number;
  firstAt: string;
  lastAt: string;
};

// A configured data-source connector bound to a brain. Connects external
// knowledge (GitHub, a website, a SQL DB, a webhook push) into the namespace.
export type DatasourceKind =
  | "webhook" | "text" | "crawler" | "github" | "sql"
  | "pdf" | "image" | "mcp" | string;

export type Datasource = {
  id: string;
  namespace: string;
  kind: DatasourceKind;
  name: string;
  config: Record<string, unknown>;
  status: "idle" | "syncing" | "ok" | "error" | string;
  cursor?: string;
  lastError?: string;
  docCount: number;
  lastSyncAt?: string;
  createdAt: string;
};

// Result of POST /api/brain/datasources/sync — how many docs were ingested.
export type SyncResult = { ingested: number; status: string; error?: string };

export type ApiError = { error: { code: string; message: string } };

// All console calls carry the session cookie (credentials) and — when a JWT was
// captured at login — the Authorization: Bearer header, so the backend console
// auth gate (CABRAIN_REQUIRE_AUTH) accepts either transport.
async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(`${API}${path}`, { credentials: "include", headers: { ...authHeaders() } });
  return r.json() as Promise<T>;
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(`${API}${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "content-type": "application/json", ...authHeaders() },
    body: JSON.stringify(body),
  });
  return r.json() as Promise<T>;
}

const qs = (params: Record<string, string | number | undefined>) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const brainApi = {
  // `authRequired` reflects the backend CABRAIN_REQUIRE_AUTH flag — the SPA uses
  // it to decide whether to show the login gate before hitting a gated endpoint.
  ping: () => getJSON<{ plugin: string; status: string; authRequired?: boolean }>("/api/brain/ping"),
  stats: () => getJSON<Stats>("/api/brain/stats"),
  activity: (limit = 50) => getJSON<{ items: ActivityItem[] }>(`/api/brain/activity?limit=${limit}`),
  namespaces: () => getJSON<{ brains: NamespaceInfo[] }>("/api/brain/namespaces"),
  graph: (namespace = "", limit = 200) =>
    getJSON<GraphData>(`/api/brain/graph${qs({ namespace, limit })}`),
  // Full memory row for a graph entity node (strip the `ent:` prefix off the
  // node id to get the bare UUID before calling this).
  getMemory: (namespace: string, id: string) =>
    getJSON<Memory & Partial<ApiError>>(`/api/brain/memory${qs({ namespace, id })}`),
  recall: (body: { namespace: string; query: string; limit?: number }) =>
    postJSON<{ results?: Recalled[] } & Partial<ApiError>>("/api/brain/recall", body),

  // Cross-brain search engine. Empty/omitted `namespaces` searches ALL brains;
  // each result carries the `namespace` it came from.
  search: (body: { query: string; namespaces?: string[]; limit?: number }) =>
    postJSON<{ results?: Recalled[] } & Partial<ApiError>>("/api/brain/search", body),
  retain: (body: { namespace: string; content: string; sourceKind?: string; sourceRef?: string }) =>
    postJSON<Record<string, unknown> & Partial<ApiError>>("/api/brain/retain", body),

  // --- Knowledge gaps ---
  // Default (no status) returns open+indexed, not dismissed.
  gaps: (opts: { namespace?: string; status?: string; limit?: number } = {}) =>
    getJSON<{ gaps: Gap[] }>(`/api/brain/gaps${qs(opts)}`),
  resolveGap: (body: { id: number; status: GapStatus; resolution?: string }) =>
    postJSON<{ id: number; status: string } & Partial<ApiError>>("/api/brain/gaps/resolve", body),

  // --- Brain details + admin ---
  brainDetail: (namespace: string) =>
    getJSON<BrainDetail & Partial<ApiError>>(`/api/brain/brain${qs({ namespace })}`),
  deleteBrain: (body: { namespace: string; confirm: string }) =>
    postJSON<{ namespace: string; deleted: number } & Partial<ApiError>>("/api/brain/brain/delete", body),
  editMemory: (body: {
    namespace: string;
    id: string;
    content?: string;
    importance?: number;
    metadata?: Record<string, unknown>;
  }) => postJSON<{ id: string; updated: boolean } & Partial<ApiError>>("/api/brain/memory/edit", body),

  // Streamed NDJSON download (Content-Disposition attachment) — use as a plain <a href>.
  exportUrl: (namespace: string) => `${API}/api/brain/export${qs({ namespace })}`,

  // --- Access tokens + per-brain grants (admin) ---
  tokens: () => getJSON<{ tokens: Token[] }>("/api/brain/tokens"),
  // Returns the freshly-minted token (raw secret shown ONCE).
  createToken: (body: { agentId: string; label: string; isAdmin: boolean }) =>
    postJSON<Token & Partial<ApiError>>("/api/brain/tokens", body),
  revokeToken: (body: { token: string }) =>
    postJSON<{ revoked: boolean } & Partial<ApiError>>("/api/brain/tokens/revoke", body),
  // Upsert a per-brain grant for an agent.
  grant: (body: { agentId: string; namespace: string; canRead: boolean; canWrite: boolean }) =>
    postJSON<Grant & Partial<ApiError>>("/api/brain/grant", body),
  revokeGrant: (body: { agentId: string; namespace: string }) =>
    postJSON<{ revoked: boolean } & Partial<ApiError>>("/api/brain/grant/revoke", body),

  // --- Session launcher: mint a scoped token + Claude Code MCP config for a brain ---
  launchSession: (body: { namespace: string; write: boolean; label?: string }) =>
    postJSON<SessionResult & Partial<ApiError>>("/api/brain/session", body),

  // --- Per-brain secrets vault (namespace-scoped) ---
  // The list is metadata-only (masked `hint`, never values).
  secrets: (namespace: string) =>
    getJSON<{ secrets: SecretMeta[] }>(`/api/brain/secrets${qs({ namespace })}`),
  // Store/update a secret (write/admin).
  putSecret: (body: { namespace: string; name: string; value: string; kind?: string }) =>
    postJSON<{ stored: boolean } & Partial<ApiError>>("/api/brain/secrets", body),
  // Decrypt a single secret — requires write/admin (stricter than read).
  revealSecret: (body: { namespace: string; name: string }) =>
    postJSON<{ value: string } & Partial<ApiError>>("/api/brain/secrets/reveal", body),
  deleteSecret: (body: { namespace: string; name: string }) =>
    postJSON<{ deleted: boolean } & Partial<ApiError>>("/api/brain/secrets/delete", body),

  // --- Live agent: chat with a selected brain (RAG grounded in its memories) ---
  chat: (body: { namespace: string; message: string; history?: ChatTurn[]; topK?: number }) =>
    postJSON<ChatAnswer & Partial<ApiError>>("/api/brain/chat", body),

  // --- Data sources: connectors that ingest external knowledge into a brain ---
  // List every source configured for a brain.
  datasources: (namespace: string) =>
    getJSON<{ datasources: Datasource[] }>(`/api/brain/datasources${qs({ namespace })}`),
  // Create a new source (webhook auto-generates config.secret on the backend).
  createDatasource: (body: { namespace: string; kind: string; name: string; config: Record<string, unknown> }) =>
    postJSON<Datasource & Partial<ApiError>>("/api/brain/datasources", body),
  // Pull/sync a source now — returns how many documents were ingested.
  syncDatasource: (body: { id: string }) =>
    postJSON<SyncResult & Partial<ApiError>>("/api/brain/datasources/sync", body),
  deleteDatasource: (body: { id: string }) =>
    postJSON<{ deleted: boolean } & Partial<ApiError>>("/api/brain/datasources/delete", body),
  // Push (webhook) ingest endpoint for a source — the URL to hand out. Callers
  // POST documents here with header `X-Webhook-Secret: <config.secret>`.
  ingestUrl: (id: string) => `${(typeof window !== "undefined" && window.location.origin) || API}/api/brain/ingest/${id}`,
};

export type ChatTurn = { role: "user" | "assistant"; content: string };
export type ChatFootprint = {
  namespace: string;
  query: string;
  recalled: number;
  model: string;
  grounded: boolean;
  latencyMs: number;
};
export type ChatAnswer = {
  answer: string;
  citations: Recalled[];
  footprint: ChatFootprint;
};

export type SessionResult = {
  agentId: string;
  namespace: string;
  write: boolean;
  token: string;
  mcpConfig: { mcpServers: Record<string, { command: string; env: Record<string, string> }> };
  howto: string;
};

// --- Realtime -------------------------------------------------------------
// Server-Sent Events from the brain plugin. Named events (name -> data):
//   retain {namespace,decision} · recall {namespace,count} · search {count}
//   gap {namespace,query} | {resolved,status} · grant {agentId,namespace}
//   brain {deleted} · secret {namespace,name,op}
export type BrainEventName = "retain" | "recall" | "search" | "gap" | "grant" | "brain" | "secret";

/**
 * Open the brain SSE stream and dispatch parsed events to `onEvent`. Returns a
 * cleanup fn that closes the stream. `onOpen`/`onError` track connection state
 * for the "live" indicator.
 */
export function subscribeBrainEvents(opts: {
  onEvent: (name: BrainEventName, data: any) => void;
  onOpen?: () => void;
  onError?: () => void;
}): () => void {
  const names: BrainEventName[] = ["retain", "recall", "search", "gap", "grant", "brain", "secret"];
  const es = new EventSource(`${API}/api/brain/events`);
  es.onopen = () => opts.onOpen?.();
  es.onerror = () => opts.onError?.();
  const handlers = names.map((name) => {
    const h = (ev: MessageEvent) => {
      let data: any = null;
      try { data = ev.data ? JSON.parse(ev.data) : null; } catch { data = ev.data; }
      opts.onEvent(name, data);
    };
    es.addEventListener(name, h as EventListener);
    return { name, h };
  });
  return () => {
    for (const { name, h } of handlers) es.removeEventListener(name, h as EventListener);
    es.close();
  };
}
