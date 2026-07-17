import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Search as SearchIcon, Pencil, Save, X } from "lucide-react";
import { brainApi, type Recalled } from "../lib/brain";

/** A single recall result with an inline edit affordance. Editing content saves
 * via POST /api/brain/memory/edit and updates the row on success. */
function ResultRow({ ns, r, onSaved }: { ns: string; r: Recalled; onSaved: (content: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(r.content);

  const save = useMutation({
    mutationFn: () => brainApi.editMemory({ namespace: ns, id: r.id, content: draft }),
    onSuccess: (res) => {
      if (res.error) return; // keep editor open; error shown inline
      onSaved(draft);
      setEditing(false);
    },
  });

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      {editing ? (
        <div className="space-y-2">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={Math.min(8, Math.max(3, draft.split("\n").length))}
            className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
            autoFocus
          />
          {save.data?.error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
              {save.data.error.code}: {save.data.error.message}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <button
              onClick={() => { setDraft(r.content); setEditing(false); }}
              className="inline-flex items-center gap-1 rounded-lg border border-border px-2.5 py-1.5 text-xs text-muted-foreground hover:bg-muted"
            >
              <X className="h-3.5 w-3.5" /> Cancel
            </button>
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending || draft.trim() === "" || draft === r.content}
              className="inline-flex items-center gap-1 rounded-lg bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
            >
              <Save className="h-3.5 w-3.5" /> {save.isPending ? "Saving…" : "Save"}
            </button>
          </div>
        </div>
      ) : (
        <>
          <div className="flex items-start gap-2">
            <div className="flex-1 text-sm text-foreground">{r.content}</div>
            <button
              onClick={() => { setDraft(r.content); setEditing(true); }}
              title="Edit memory"
              className="shrink-0 rounded-md p-1 text-muted-foreground transition hover:bg-muted hover:text-foreground"
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          </div>
          <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
            <span className="rounded bg-muted px-1.5 py-0.5">{r.network}·{r.memoryType}</span>
            <span>{r.sourceKind}{r.sourceRef ? ` · ${r.sourceRef}` : ""}</span>
            {r.viaEntity && <span className="text-primary">via {r.viaEntity}</span>}
            <span className="ms-auto tabular-nums">score {r.score.toFixed(3)} · imp {r.importance.toFixed(2)}</span>
          </div>
        </>
      )}
    </div>
  );
}

export function BrainSearch() {
  const [ns, setNs] = useState("");
  const [q, setQ] = useState("");
  const [state, setState] = useState<{ loading: boolean; results?: Recalled[]; error?: string; ns?: string }>({ loading: false });

  const run = async () => {
    if (!ns.trim() || !q.trim()) { setState({ loading: false, error: "Enter a brain (namespace) and a query." }); return; }
    setState({ loading: true });
    try {
      const r = await brainApi.recall({ namespace: ns.trim(), query: q.trim(), limit: 12 });
      if (r.error) setState({ loading: false, error: `${r.error.code}: ${r.error.message}` });
      else setState({ loading: false, results: r.results ?? [], ns: ns.trim() });
    } catch (e: any) { setState({ loading: false, error: String(e?.message ?? e) }); }
  };

  const patch = (id: string, content: string) =>
    setState((s) => ({ ...s, results: s.results?.map((x) => (x.id === id ? { ...x, content } : x)) }));

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
          <ResultRow key={r.id} ns={state.ns ?? ns.trim()} r={r} onSaved={(content) => patch(r.id, content)} />
        ))}
      </div>
    </div>
  );
}
