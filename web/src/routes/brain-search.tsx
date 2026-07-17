import { useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Search as SearchIcon, Pencil, Save, X, Check, Database,
  MessagesSquare, Sparkles, Filter, Star, SlidersHorizontal,
} from "lucide-react";
import { brainApi, type Recalled } from "../lib/brain";
import { SynapseField, NeuralGlyph } from "../components/neural";
import { hueForBrain as hueFor } from "../lib/brain-colors";

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

/** A Tuner chip (Shape-of-AI: Filters) — a toggleable facet that narrows the
 * result set. Colours in the brain hue when active. */
function FacetChip({ label, active, onClick, color }: { label: string; active: boolean; onClick: () => void; color?: string }) {
  const c = color ?? "var(--primary)";
  return (
    <button
      onClick={onClick}
      className="inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium transition"
      style={active
        ? { borderColor: c, background: `${color ? color + "22" : "color-mix(in srgb, var(--primary) 15%, transparent)"}`, color: c }
        : { borderColor: "var(--border)", color: "var(--muted-foreground)" }}
    >
      {active && <Check className="h-3 w-3" />} {label}
    </button>
  );
}

// The three memory "networks" (Shape-of-AI Modes/Tuners) that the console filters on.
const NETWORKS = ["fact", "experience", "belief"];

/** Search surface. When `namespace` is supplied (rendered inside a brain
 * workspace) it locks to that single brain and hides the cross-brain picker;
 * otherwise it's the global cross-brain search engine.
 *
 * Shape-of-AI patterns: a Chat/Recall/Search MODE switch, visible FILTER chips
 * (network · type · source · importance) as Tuners over the result set, and a
 * styled CAVEAT when a query returns nothing. */
export function BrainSearch({ namespace }: { namespace?: string } = {}) {
  const scoped = !!namespace;
  const nav = useNavigate();
  const accent = scoped ? hueFor(namespace!) : "var(--primary)";
  const [q, setQ] = useState("");
  // Recall (graph-aware, single brain) vs Search (hybrid, cross-brain).
  const [mode, setMode] = useState<"recall" | "search">(scoped ? "recall" : "search");
  // Selected brains; empty set = ALL brains (the default). Ignored when scoped.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [state, setState] = useState<{ results?: Recalled[]; error?: string; ran?: boolean; q?: string }>({});

  // Active tuners (client-side facets over the returned results).
  const [fNet, setFNet] = useState<Set<string>>(new Set());
  const [fType, setFType] = useState<Set<string>>(new Set());
  const [fSrc, setFSrc] = useState<Set<string>>(new Set());
  const [highImp, setHighImp] = useState(false);

  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, enabled: !scoped });
  const brains = namespaces.data?.brains ?? [];

  const toggleIn = (set: Set<string>, setter: (s: Set<string>) => void, v: string) => {
    const next = new Set(set);
    if (next.has(v)) next.delete(v); else next.add(v);
    setter(next);
  };
  const toggle = (ns: string) => toggleIn(selected, setSelected, ns);

  const search = useMutation({
    mutationFn: () => {
      const query = q.trim();
      if (scoped && mode === "recall") return brainApi.recall({ namespace: namespace!, query, limit: 20 });
      return brainApi.search({
        query,
        namespaces: scoped ? [namespace!] : selected.size ? [...selected] : undefined,
        limit: 20,
      });
    },
    onSuccess: (r) => {
      if (r.error) setState({ error: `${r.error.code}: ${r.error.message}`, ran: true, q: q.trim() });
      else setState({ results: r.results ?? [], ran: true, q: q.trim() });
    },
    onError: (e: any) => setState({ error: String(e?.message ?? e), ran: true, q: q.trim() }),
  });

  const run = () => { if (q.trim()) { setFNet(new Set()); setFType(new Set()); setFSrc(new Set()); setHighImp(false); search.mutate(); } };

  const patch = (id: string, content: string) =>
    setState((s) => ({ ...s, results: s.results?.map((x) => (x.id === id ? { ...x, content } : x)) }));

  const allActive = selected.size === 0;
  const raw = state.results ?? [];

  // Facet vocabularies derived from the current result set.
  const typeFacets = useMemo(() => Array.from(new Set(raw.map((r) => r.memoryType).filter(Boolean))).sort(), [raw]);
  const srcFacets = useMemo(() => Array.from(new Set(raw.map((r) => r.sourceKind).filter(Boolean))).sort(), [raw]);
  const netFacets = useMemo(() => NETWORKS.filter((n) => raw.some((r) => r.network === n)), [raw]);

  // Apply the active tuners.
  const results = useMemo(() => raw.filter((r) =>
    (fNet.size === 0 || fNet.has(r.network)) &&
    (fType.size === 0 || fType.has(r.memoryType)) &&
    (fSrc.size === 0 || fSrc.has(r.sourceKind)) &&
    (!highImp || (r.importance ?? 0) >= 0.7),
  ), [raw, fNet, fType, fSrc, highImp]);

  const hasFilters = fNet.size || fType.size || fSrc.size || highImp;
  const modes: { key: "chat" | "recall" | "search"; label: string; icon: any }[] = [
    ...(scoped ? [{ key: "chat" as const, label: "Chat", icon: MessagesSquare }] : []),
    ...(scoped ? [{ key: "recall" as const, label: "Recall", icon: Sparkles }] : []),
    { key: "search", label: "Search", icon: SearchIcon },
  ];

  return (
    <div className="mx-auto max-w-4xl space-y-5 p-6">
      {/* Hero search — the "search engine" moment. */}
      <div
        className="relative overflow-hidden rounded-2xl border p-6"
        style={{ borderColor: scoped ? `${accent}33` : "var(--border)", background: scoped
          ? `radial-gradient(120% 100% at 0% 0%, ${accent}1f, transparent 55%), color-mix(in srgb, var(--card) 85%, transparent)`
          : "linear-gradient(135deg, color-mix(in srgb, #6366f1 10%, transparent), color-mix(in srgb, #14b8a6 8%, transparent))" }}
      >
        <SynapseField className="opacity-40" />
        <div className="relative">
          {/* Mode switch (Tuner) */}
          <div className="mb-3 inline-flex rounded-xl border border-border bg-background/70 p-0.5 backdrop-blur">
            {modes.map((m) => {
              const on = m.key === mode;
              return (
                <button
                  key={m.key}
                  onClick={() => { if (m.key === "chat") nav({ to: "/b/$namespace/chat", params: { namespace: namespace! } }); else setMode(m.key); }}
                  className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                    on ? "bg-primary text-primary-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  <m.icon className="h-3.5 w-3.5" /> {m.label}
                </button>
              );
            })}
          </div>

          <h1 className="text-2xl font-semibold text-foreground">
            {scoped ? (mode === "recall" ? "Recall from this brain" : "Search this brain") : "Search across every brain"}
          </h1>
          <p className="mb-4 text-sm text-muted-foreground">
            {scoped
              ? (mode === "recall"
                ? <>Graph-aware recall scoped to <span className="font-medium text-foreground">{namespace}</span> — walks entities to surface related memories.</>
                : <>Hybrid search — dense vector + BM25 (RRF) + rerank — scoped to <span className="font-medium text-foreground">{namespace}</span>.</>)
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
              className="inline-flex items-center justify-center gap-2 rounded-xl px-5 py-3 text-sm font-medium text-white shadow-sm transition hover:brightness-110 disabled:opacity-60"
              style={{ background: scoped ? `linear-gradient(135deg, ${accent}, ${accent}bb)` : "var(--primary)", color: scoped ? "#fff" : "var(--primary-foreground)" }}
            >
              <SearchIcon className="h-4 w-4" />{search.isPending ? "Searching…" : mode === "recall" && scoped ? "Recall" : "Search"}
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

      {/* Tuners — visible filter chips over the results (Shape-of-AI: Filters). */}
      {raw.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-border bg-card/70 px-3 py-2.5 backdrop-blur">
          <span className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground"><SlidersHorizontal className="h-3.5 w-3.5" /> Tune</span>
          {netFacets.map((n) => <FacetChip key={n} label={n} active={fNet.has(n)} onClick={() => toggleIn(fNet, setFNet, n)} color={hueFor(n)} />)}
          {typeFacets.length > 0 && <span className="text-border">·</span>}
          {typeFacets.map((t) => <FacetChip key={t} label={t} active={fType.has(t)} onClick={() => toggleIn(fType, setFType, t)} color={hueFor(t)} />)}
          {srcFacets.length > 0 && <span className="text-border">·</span>}
          {srcFacets.map((s) => <FacetChip key={s} label={s} active={fSrc.has(s)} onClick={() => toggleIn(fSrc, setFSrc, s)} />)}
          <button
            onClick={() => setHighImp((v) => !v)}
            className="inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium transition"
            style={highImp ? { borderColor: "#f59e0b", background: "#f59e0b22", color: "#f59e0b" } : { borderColor: "var(--border)", color: "var(--muted-foreground)" }}
          >
            <Star className="h-3 w-3" /> high importance
          </button>
          {hasFilters ? (
            <button onClick={() => { setFNet(new Set()); setFType(new Set()); setFSrc(new Set()); setHighImp(false); }} className="ms-auto inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
              <X className="h-3 w-3" /> clear
            </button>
          ) : (
            <span className="ms-auto inline-flex items-center gap-1 text-xs text-muted-foreground"><Filter className="h-3 w-3" /> {results.length} shown</span>
          )}
        </div>
      )}

      {state.ran && !search.isPending && results.length > 0 && (
        <div className="text-xs text-muted-foreground">
          {results.length}{hasFilters ? ` of ${raw.length}` : ""} result{results.length === 1 ? "" : "s"} · {scoped ? namespace : allActive ? "all brains" : [...selected].join(", ")}
        </div>
      )}

      {/* Caveat — thin/empty recall shown as a styled state, not a raw error. */}
      {state.ran && !search.isPending && raw.length === 0 && !state.error && (
        <div className="relative overflow-hidden rounded-2xl border border-amber-500/25 bg-amber-500/5 px-4 py-10 text-center">
          <SynapseField className="opacity-20" />
          <div className="relative mx-auto max-w-sm">
            <span className="mx-auto mb-3 flex h-11 w-11 items-center justify-center rounded-xl bg-amber-500/15 text-amber-500"><NeuralGlyph className="h-6 w-6" /></span>
            <div className="text-sm font-medium text-foreground">
              {scoped ? <>This brain has no memory of “{state.q}”.</> : <>No brain remembers “{state.q}”.</>}
            </div>
            <p className="mt-1 text-sm text-muted-foreground">Try a shorter, keyword-forward query — or this may be a genuine knowledge gap.</p>
          </div>
        </div>
      )}
      {/* Filtered everything out. */}
      {state.ran && !search.isPending && raw.length > 0 && results.length === 0 && (
        <div className="rounded-xl border border-border bg-card px-4 py-8 text-center text-sm text-muted-foreground">
          All {raw.length} results are filtered out — loosen the tuners above.
        </div>
      )}

      <div className="space-y-2">
        {results.map((r) => (
          <ResultRow key={`${r.namespace}:${r.id}`} r={r} onSaved={(content) => patch(r.id, content)} />
        ))}
      </div>
    </div>
  );
}
