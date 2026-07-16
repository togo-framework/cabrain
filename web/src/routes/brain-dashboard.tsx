import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Database, Boxes, Waypoints, Share2, Users, Search as SearchIcon,
  Terminal, Upload, Plug, Code2, CircleAlert,
} from "lucide-react";
import { brainApi, type ActivityItem, type Recalled } from "../lib/brain";

function greeting() {
  const h = new Date().getHours();
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}

/** Metric tile — dashes while loading, muted at zero (Cognee behavior). */
function Metric({ label, value, loading, icon: Icon }: { label: string; value: number; loading: boolean; icon: any }) {
  const zero = !loading && value === 0;
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <Icon className={`h-4 w-4 ${zero ? "text-muted-foreground/40" : "text-primary"}`} />
      </div>
      <div className={`text-2xl font-semibold tabular-nums ${zero ? "text-muted-foreground/50" : "text-foreground"}`}>
        {loading ? "—" : value.toLocaleString()}
      </div>
    </div>
  );
}

function OutcomeBadge({ outcome }: { outcome: string }) {
  const map: Record<string, string> = {
    hit: "bg-emerald-500/15 text-emerald-500",
    empty: "bg-amber-500/15 text-amber-500",
    error: "bg-rose-500/15 text-rose-500",
    running: "bg-sky-500/15 text-sky-500",
  };
  const glyph: Record<string, string> = { hit: "✓", empty: "∅", error: "✗", running: "…" };
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${map[outcome] ?? "bg-muted text-muted-foreground"}`}>
      {glyph[outcome] ?? "•"} {outcome}
    </span>
  );
}

/** Stable colored dot per actor (Cognee: agents easy to tell apart). */
function ActorDot({ id }: { id: string }) {
  const hues = ["#6366f1", "#ec4899", "#14b8a6", "#f59e0b", "#8b5cf6", "#ef4444", "#10b981", "#3b82f6"];
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return <span className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: hues[h % hues.length] }} />;
}

const GET_STARTED = [
  { icon: Code2, title: "Claude Code", body: "Session memory via the Memory Tool backend + MCP." },
  { icon: Plug, title: "API / MCP", body: "memory_retain · memory_recall over MCP or REST." },
  { icon: Terminal, title: "Capture mode", body: "Passively record every session into episodic memory." },
  { icon: Upload, title: "Company Brain", body: "Upload docs into a namespace to cognify a graph." },
];

const FILTERS = ["all", "mine", "agents", "searches", "errors"] as const;

export function BrainDashboard() {
  const [ns, setNs] = useState("");
  const [q, setQ] = useState("");
  const [recallState, setRecallState] = useState<{ loading: boolean; results?: Recalled[]; error?: string }>({ loading: false });
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("all");

  const stats = useQuery({ queryKey: ["brain", "stats"], queryFn: brainApi.stats, refetchInterval: 10_000 });
  const activity = useQuery({ queryKey: ["brain", "activity"], queryFn: () => brainApi.activity(50), refetchInterval: 8_000 });

  const items = activity.data?.items ?? [];
  const filtered = useMemo(() => {
    switch (filter) {
      case "searches": return items.filter((i) => i.op === "recall");
      case "errors": return items.filter((i) => i.outcome === "error");
      case "agents": return items.filter((i) => i.agentId && i.agentId !== "me");
      default: return items;
    }
  }, [items, filter]);

  const runSearch = async () => {
    if (!ns.trim() || !q.trim()) { setRecallState({ loading: false, error: "Enter a brain (namespace) and a query." }); return; }
    setRecallState({ loading: true });
    try {
      const r = await brainApi.recall({ namespace: ns.trim(), query: q.trim(), limit: 8 });
      if (r.error) setRecallState({ loading: false, error: `${r.error.code}: ${r.error.message}` });
      else setRecallState({ loading: false, results: r.results ?? [] });
    } catch (e: any) {
      setRecallState({ loading: false, error: String(e?.message ?? e) });
    }
  };

  const loading = stats.isLoading;
  const notReady = stats.data && !stats.data.ready;

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      {/* Greeting */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground">{greeting()}</h1>
        <p className="text-sm text-muted-foreground">Your organization's shared memory — one brain, many mouths.</p>
      </div>

      {notReady && (
        <div className="flex items-start gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm">
          <CircleAlert className="mt-0.5 h-5 w-5 shrink-0 text-amber-500" />
          <div>
            <div className="font-medium text-foreground">Brain not connected</div>
            <div className="text-muted-foreground">
              The <code>cabrain</code> database (VectorChord stack) and the <code>brain-tei</code> embeddings provider
              aren't live yet, so counters read zero and <code>retain</code>/<code>recall</code> are inert. Everything
              lights up the moment the infra bundle lands.
            </div>
          </div>
        </div>
      )}

      {/* Metrics strip */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <Metric label="Brains" value={stats.data?.brains ?? 0} loading={loading} icon={Database} />
        <Metric label="Memories" value={stats.data?.memories ?? 0} loading={loading} icon={Boxes} />
        <Metric label="Graph nodes" value={stats.data?.entities ?? 0} loading={loading} icon={Waypoints} />
        <Metric label="Graph edges" value={stats.data?.edges ?? 0} loading={loading} icon={Share2} />
        <Metric label="Agents" value={stats.data?.agents ?? 0} loading={loading} icon={Users} />
        <Metric label="Recalls 24h" value={stats.data?.recalls24h ?? 0} loading={loading} icon={SearchIcon} />
      </div>

      {/* Search your memory terminal */}
      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <Terminal className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Search your memory</span>
        </div>
        <div className="space-y-3 p-4">
          <div className="flex flex-col gap-2 sm:flex-row">
            <input
              value={ns} onChange={(e) => setNs(e.target.value)} placeholder="brain (namespace) e.g. sentra"
              className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary sm:max-w-[220px]"
            />
            <input
              value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === "Enter" && runSearch()}
              placeholder="what did we decide about…"
              className="w-full flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
            />
            <button onClick={runSearch} disabled={recallState.loading}
              className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60">
              {recallState.loading ? "Recalling…" : "Recall"}
            </button>
          </div>
          {recallState.error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">{recallState.error}</div>
          )}
          {recallState.results && recallState.results.length === 0 && (
            <div className="text-xs text-muted-foreground">No memories yet — retain some first.</div>
          )}
          {recallState.results?.map((r) => (
            <div key={r.id} className="rounded-lg border border-border bg-background p-3">
              <div className="text-sm text-foreground">{r.content}</div>
              <div className="mt-1 flex flex-wrap gap-2 text-xs text-muted-foreground">
                <span>{r.network}·{r.memoryType}</span>
                <span>{r.sourceKind}{r.sourceRef ? ` · ${r.sourceRef}` : ""}</span>
                <span>score {r.score.toFixed(3)}</span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Get started */}
      <div>
        <h2 className="mb-3 text-sm font-medium text-muted-foreground">Get started</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {GET_STARTED.map((c) => (
            <div key={c.title} className="rounded-xl border border-border bg-card p-4 transition hover:border-primary/50">
              <c.icon className="mb-2 h-5 w-5 text-primary" />
              <div className="text-sm font-medium text-foreground">{c.title}</div>
              <div className="mt-1 text-xs text-muted-foreground">{c.body}</div>
            </div>
          ))}
        </div>
      </div>

      {/* Memory activity log */}
      <div className="rounded-xl border border-border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
          <span className="text-sm font-medium">Memory activity</span>
          <div className="flex gap-1">
            {FILTERS.map((f) => (
              <button key={f} onClick={() => setFilter(f)}
                className={`rounded-md px-2 py-1 text-xs capitalize ${filter === f ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted"}`}>
                {f}
              </button>
            ))}
          </div>
        </div>
        <div className="divide-y divide-border">
          {filtered.length === 0 && (
            <div className="px-4 py-8 text-center text-sm text-muted-foreground">
              No activity yet. Every retain/recall shows up here, newest first.
            </div>
          )}
          {filtered.map((a: ActivityItem) => (
            <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <ActorDot id={a.agentId || a.namespace || "?"} />
              <span className="font-medium text-foreground">{a.op}</span>
              <span className="text-muted-foreground">{a.namespace || "—"}</span>
              <span className="ms-auto text-xs text-muted-foreground tabular-nums">{a.latencyMs ? `${a.latencyMs}ms` : ""}</span>
              <OutcomeBadge outcome={a.outcome} />
              <span className="text-xs text-muted-foreground">{a.ts ? new Date(a.ts).toLocaleTimeString() : ""}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
