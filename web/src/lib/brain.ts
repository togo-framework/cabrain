// Client for the brain plugin's console API (/api/brain/*).
import { API } from "./api";

export type Stats = {
  ready: boolean;
  brains: number;
  memories: number;
  entities: number;
  edges: number;
  agents: number;
  sessions24h: number;
  recalls24h: number;
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

export type GraphNode = { id: string; name: string };
export type GraphEdge = { source: string; target: string };
export type GraphData = { ready: boolean; nodes: GraphNode[]; edges: GraphEdge[] };

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
};

export type ApiError = { error: { code: string; message: string } };

async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(`${API}${path}`);
  return r.json() as Promise<T>;
}

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(`${API}${path}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  return r.json() as Promise<T>;
}

export const brainApi = {
  ping: () => getJSON<{ plugin: string; status: string }>("/api/brain/ping"),
  stats: () => getJSON<Stats>("/api/brain/stats"),
  activity: (limit = 50) => getJSON<{ items: ActivityItem[] }>(`/api/brain/activity?limit=${limit}`),
  namespaces: () => getJSON<{ brains: NamespaceInfo[] }>("/api/brain/namespaces"),
  graph: (namespace = "", limit = 200) =>
    getJSON<GraphData>(`/api/brain/graph?namespace=${encodeURIComponent(namespace)}&limit=${limit}`),
  recall: (body: { namespace: string; query: string; limit?: number }) =>
    postJSON<{ results?: Recalled[] } & Partial<ApiError>>("/api/brain/recall", body),
  retain: (body: { namespace: string; content: string; sourceKind?: string; sourceRef?: string }) =>
    postJSON<Record<string, unknown> & Partial<ApiError>>("/api/brain/retain", body),
};
