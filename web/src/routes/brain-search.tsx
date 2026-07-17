import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Search as SearchIcon, Pencil, Save, X, Check, Database } from "lucide-react";
import { brainApi, type Recalled } from "../lib/brain";
import { SynapseField } from "../components/neural";

// Stable hue per brain namespace — matches the mindmap/graph palette family so a
// brain reads the same colour everywhere in the console.
const PALETTE = [
  "#6366f1", "#ec4899", "#14b8a6", "#f59e0b", "#8b5cf6",
  "#ef4444", "#10b981", "#3b82f6", "#f97316", "#06b6d4",
];
function hueFor(key: string): string {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

/** Which brain a result came from — a coloured chip. */
function BrainChip({ ns }: { ns: string }) {
  const c = hueFor(ns);
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
      style={{ background: `${c}22`, color: c }}
    >
      <Database className="h-3 w-3" /> {ns}
    </span>
  );
}

/** A single recall result with an inline edit affordance. Editing content saves
 * via POST /api/brain/memory/edit and updates the row on success. Namespace comes
 * from the result itself (cross-brain search). */
function ResultRow({ r, onSaved }: { r: Recalled; onSaved: (content: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(r.content);
  const ns = r.namespace ?? "";

  const save = useMutation({
    mutationFn: () => brainApi.editMemory({ namespace: ns, id: r.id, content: draft }),
    onSuccess: (res) => {
      if (res.error) return; // keep editor open; error shown inline
      onSaved(draft);
      setEditing(false);
    },
  });

  return (
    <div className="rounded-xl border border-border bg-card p-4 transition hover:border-primary/40">
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
            <div className="flex-1 whitespace-pre-wrap text-sm text-foreground">{r.content}</div>
            <button
              onClick={() => { setDraft(r.content); setEditing(true); }}
              title="Edit memory"
              className="shrink-0 rounded-md p-1 text-muted-foreground transition hover:bg-muted hover:text-foreground"
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            {ns && <BrainChip ns={ns} />}
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

/** Search surface. When `namespace` is supplied (rendered inside a brain
 * workspace) it locks to that single brain and hides the cross-brain picker;
 * otherwise it's the global cross-brain search engine. */
export function BrainSearch({ namespace }: { namespace?: string } = {}) {
  const scoped = !!namespace;
  const [q, setQ] = useState("");
  // Selected brains; empty set = ALL brains (the default). Ignored when scoped.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [state, setState] = useState<{ results?: Recalled[]; error?: string; ran?: boolean }>({});

  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, enabled: !scoped });
  const brains = namespaces.data?.brains ?? [];

  const toggle = (ns: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(ns)) next.delete(ns); else next.add(ns);
      return next;
    });

  const search = useMutation({
    mutationFn: () =>
      brainApi.search({
        query: q.trim(),
        namespaces: scoped ? [namespace!] : selected.size ? [...selected] : undefined,
        limit: 20,
      }),
    onSuccess: (r) => {
      if (r.error) setState({ error: `${r.error.code}: ${r.error.message}`, ran: true });
      else setState({ results: r.results ?? [], ran: true });
    },
    onError: (e: any) => setState({ error: String(e?.message ?? e), ran: true }),
  });

  const run = () => { if (q.trim()) search.mutate(); };

  const patch = (id: string, content: string) =>
    setState((s) => ({ ...s, results: s.results?.map((x) => (x.id === id ? { ...x, content } : x)) }));

  const allActive = selected.size === 0;

  return (
    <div className="mx-auto max-w-4xl space-y-5 p-6">
      {/* Hero search — the "search engine" moment. */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-indigo-500/10 via-violet-500/5 to-teal-400/10 p-6">
        <SynapseField className="opacity-40" />
        <div className="relative">
          <h1 className="text-2xl font-semibold text-foreground">
            {scoped ? "Search this brain" : "Search across every brain"}
          </h1>
          <p className="mb-4 text-sm text-muted-foreground">
            {scoped
              ? <>Hybrid recall — dense vector + BM25 (RRF) + rerank — scoped to <span className="font-medium text-foreground">{namespace}</span>.</>
              : "Hybrid recall — dense vector + BM25 (RRF) + rerank — over all brains at once, or scope to a few."}
          </p>

          <div className="flex flex-col gap-2 sm:flex-row">
            <div className="relative flex-1">
              <SearchIcon className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && run()}
                placeholder="what did we decide about…"
                autoFocus
                className="w-full rounded-xl border border-border bg-background/80 py-3 pl-10 pr-3 text-sm shadow-sm outline-none backdrop-blur focus:border-primary"
              />
            </div>
            <button
              onClick={run}
              disabled={search.isPending || !q.trim()}
              className="inline-flex items-center justify-center gap-2 rounded-xl bg-primary px-5 py-3 text-sm font-medium text-primary-foreground shadow-sm transition hover:opacity-90 disabled:opacity-60"
            >
              <SearchIcon className="h-4 w-4" />{search.isPending ? "Searching…" : "Search"}
            </button>
          </div>

          {/* Brain multi-select — hidden when locked to a single brain workspace. */}
          {!scoped && (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <button
              onClick={() => setSelected(new Set())}
              className={`inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs font-medium transition ${
                allActive ? "border-primary bg-primary text-primary-foreground" : "border-border text-muted-foreground hover:bg-muted"
              }`}
            >
              {allActive && <Check className="h-3 w-3" />} All brains
            </button>
            {brains.map((b) => {
              const on = selected.has(b.namespace);
              const c = hueFor(b.namespace);
              return (
                <button
                  key={b.namespace}
                  onClick={() => toggle(b.namespace)}
                  className="inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition"
                  style={
                    on
                      ? { borderColor: c, background: `${c}22`, color: c }
                      : { borderColor: "var(--border)", color: "var(--muted-foreground)" }
                  }
                >
                  {on && <Check className="h-3 w-3" />}
                  {b.namespace}
                  <span className="opacity-60">{b.memories.toLocaleString()}</span>
                </button>
              );
            })}
          </div>
          )}
        </div>
      </div>

      {state.error && (
        <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-400">{state.error}</div>
      )}

      {state.ran && !search.isPending && (state.results?.length ?? 0) > 0 && (
        <div className="text-xs text-muted-foreground">
          {state.results!.length} result{state.results!.length === 1 ? "" : "s"} · {scoped ? namespace : allActive ? "all brains" : [...selected].join(", ")}
        </div>
      )}
      {state.ran && !search.isPending && state.results && state.results.length === 0 && !state.error && (
        <div className="rounded-xl border border-border bg-card px-4 py-10 text-center text-sm text-muted-foreground">
          No memories matched. Try a shorter, keyword-forward query.
        </div>
      )}

      <div className="space-y-2">
        {state.results?.map((r) => (
          <ResultRow key={`${r.namespace}:${r.id}`} r={r} onSaved={(content) => patch(r.id, content)} />
        ))}
      </div>
    </div>
  );
}
