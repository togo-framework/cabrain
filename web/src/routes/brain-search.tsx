import { useState } from "react";
import { Search as SearchIcon } from "lucide-react";
import { brainApi, type Recalled } from "../lib/brain";

export function BrainSearch() {
  const [ns, setNs] = useState("");
  const [q, setQ] = useState("");
  const [state, setState] = useState<{ loading: boolean; results?: Recalled[]; error?: string }>({ loading: false });

  const run = async () => {
    if (!ns.trim() || !q.trim()) { setState({ loading: false, error: "Enter a brain (namespace) and a query." }); return; }
    setState({ loading: true });
    try {
      const r = await brainApi.recall({ namespace: ns.trim(), query: q.trim(), limit: 12 });
      if (r.error) setState({ loading: false, error: `${r.error.code}: ${r.error.message}` });
      else setState({ loading: false, results: r.results ?? [] });
    } catch (e: any) { setState({ loading: false, error: String(e?.message ?? e) }); }
  };

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Search</h1>
        <p className="text-sm text-muted-foreground">Hybrid recall — dense vector + BM25 (RRF) + rerank, scoped to a brain.</p>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input value={ns} onChange={(e) => setNs(e.target.value)} placeholder="brain (namespace)"
          className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary sm:max-w-[220px]" />
        <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === "Enter" && run()}
          placeholder="what did we decide about…"
          className="w-full flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary" />
        <button onClick={run} disabled={state.loading}
          className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60">
          <SearchIcon className="h-4 w-4" />{state.loading ? "Recalling…" : "Recall"}
        </button>
      </div>
      {state.error && <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-400">{state.error}</div>}
      {state.results && state.results.length === 0 && <div className="text-sm text-muted-foreground">No results.</div>}
      <div className="space-y-2">
        {state.results?.map((r) => (
          <div key={r.id} className="rounded-xl border border-border bg-card p-4">
            <div className="text-sm text-foreground">{r.content}</div>
            <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
              <span className="rounded bg-muted px-1.5 py-0.5">{r.network}·{r.memoryType}</span>
              <span>{r.sourceKind}{r.sourceRef ? ` · ${r.sourceRef}` : ""}</span>
              {r.viaEntity && <span className="text-primary">via {r.viaEntity}</span>}
              <span className="ms-auto tabular-nums">score {r.score.toFixed(3)} · imp {r.importance.toFixed(2)}</span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
