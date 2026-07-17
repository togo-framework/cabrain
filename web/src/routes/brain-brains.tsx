import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Database, Download, Trash2, AlertTriangle, HelpCircle, Search, Rocket,
  ArrowRight, Boxes, Waypoints, Lock,
} from "lucide-react";
import { brainApi, type BrainDetail, type NamespaceInfo } from "../lib/brain";
import { SynapseField, NeuralGlyph } from "../components/neural";
import { LaunchSessionModal } from "../components/launch-session-modal";

// Stable hue per type name — matches the mindmap palette family.
const PALETTE = [
  "#6366f1", "#ec4899", "#14b8a6", "#f59e0b", "#8b5cf6",
  "#ef4444", "#10b981", "#3b82f6", "#f97316", "#06b6d4",
  "#a855f7", "#84cc16", "#e11d48", "#0ea5e9", "#d946ef",
];
function hueFor(key: string): string {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length];
}

/** Typed-confirm delete modal — the button only enables once the user types the
 * exact namespace, then POSTs { namespace, confirm } (confirm must equal namespace). */
function DeleteBrainModal({ namespace, onClose }: { namespace: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [typed, setTyped] = useState("");
  const del = useMutation({
    mutationFn: () => brainApi.deleteBrain({ namespace, confirm: namespace }),
    onSuccess: (res) => {
      if (res.error) return; // keep the modal open; error shown below
      qc.invalidateQueries({ queryKey: ["brain", "namespaces"] });
      qc.invalidateQueries({ queryKey: ["brain", "stats"] });
      onClose();
    },
  });
  const match = typed === namespace;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div className="w-full max-w-md rounded-xl border border-border bg-card p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center gap-2">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-rose-500/15 text-rose-500"><AlertTriangle className="h-5 w-5" /></span>
          <div>
            <div className="font-semibold text-foreground">Delete brain</div>
            <div className="text-xs text-muted-foreground">This permanently removes every memory in <code>{namespace}</code>.</div>
          </div>
        </div>
        <p className="mb-2 text-sm text-muted-foreground">
          Type <span className="font-mono font-medium text-foreground">{namespace}</span> to confirm.
        </p>
        <input
          autoFocus
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder={namespace}
          className="mb-3 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-rose-500"
        />
        {del.data?.error && (
          <div className="mb-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
            {del.data.error.code}: {del.data.error.message}
          </div>
        )}
        <div className="flex justify-end gap-2">
          <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground hover:bg-muted">Cancel</button>
          <button
            onClick={() => del.mutate()}
            disabled={!match || del.isPending}
            className="inline-flex items-center gap-1.5 rounded-lg bg-rose-500 px-3 py-2 text-sm font-medium text-white transition hover:bg-rose-600 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Trash2 className="h-4 w-4" /> {del.isPending ? "Deleting…" : "Delete brain"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** A brain's visual identity (Shape-of-AI: Avatar + Color) — a neural glyph in the
 * brain's own stable hue with a soft synaptic glow. Reads as a *brain*, not a DB. */
export function BrainAvatar({ namespace, size = 8 }: { namespace: string; size?: number }) {
  const c = hueFor(namespace);
  return (
    <span
      className="relative flex shrink-0 items-center justify-center rounded-full"
      style={{
        height: `${size * 0.25}rem`, width: `${size * 0.25}rem`,
        background: `radial-gradient(circle at 30% 30%, ${c}33, ${c}14)`,
        boxShadow: `0 0 14px -4px ${c}`, color: c, border: `1px solid ${c}55`,
      }}
      title={namespace}
    >
      <NeuralGlyph className="h-1/2 w-1/2" />
    </span>
  );
}

function TypeBar({ label, count, max }: { label: string; count: number; max: number }) {
  const pct = max > 0 ? Math.max(4, (count / max) * 100) : 0;
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 truncate text-xs text-muted-foreground" title={label}>{label}</span>
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: hueFor(label) }} />
      </div>
      <span className="w-10 shrink-0 text-right text-xs tabular-nums text-muted-foreground">{count.toLocaleString()}</span>
    </div>
  );
}

/** One brain, as a card. The title and the prominent Enter action both open the
 * brain workspace — "open this brain" is the primary gesture (the brain is the
 * entry point). Launch / Export / Delete stay as secondary admin actions. */
function BrainCard({ b, onDelete, onLaunch }: { b: NamespaceInfo; onDelete: () => void; onLaunch: () => void }) {
  const detail = useQuery({
    queryKey: ["brain", "detail", b.namespace],
    queryFn: () => brainApi.brainDetail(b.namespace),
  });
  const d: BrainDetail | undefined = detail.data && !detail.data.error ? detail.data : undefined;

  const types = d ? Object.entries(d.types).sort((a, c) => c[1] - a[1]) : [];
  const maxType = types.length ? types[0][1] : 0;
  const topTypes = types.slice(0, 6);
  const sources = d ? Object.entries(d.sources).sort((a, c) => c[1] - a[1]) : [];

  return (
    <div className="group flex flex-col rounded-xl border border-border bg-card p-4 transition hover:border-primary/50 hover:shadow-sm">
      <Link to="/b/$namespace" params={{ namespace: b.namespace }} className="flex items-center gap-2">
        <BrainAvatar namespace={b.namespace} />
        <span className="font-medium text-foreground group-hover:text-primary">{b.namespace}</span>
        {d && d.openGaps > 0 && (
          <span className="ms-auto inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-500">
            <HelpCircle className="h-3 w-3" /> {d.openGaps} {d.openGaps === 1 ? "gap" : "gaps"}
          </span>
        )}
      </Link>

      <div className="mt-3 text-2xl font-semibold tabular-nums text-foreground">{b.memories.toLocaleString()}</div>
      <div className="text-xs text-muted-foreground">memories · last {b.lastAt ? new Date(b.lastAt).toLocaleDateString() : "—"}</div>

      {/* Type breakdown */}
      <div className="mt-4 space-y-1.5">
        {detail.isLoading ? (
          <div className="text-xs text-muted-foreground">Loading breakdown…</div>
        ) : topTypes.length > 0 ? (
          topTypes.map(([t, c]) => <TypeBar key={t} label={t} count={c} max={maxType} />)
        ) : (
          <div className="text-xs text-muted-foreground">No type breakdown.</div>
        )}
      </div>

      {/* Sources + recalls */}
      {d && (
        <div className="mt-3 flex flex-wrap gap-1.5">
          {sources.map(([k, c]) => (
            <span key={k} className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{k} · {c.toLocaleString()}</span>
          ))}
          <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
            <Search className="h-3 w-3" /> {d.recalls.toLocaleString()} recalls
          </span>
        </div>
      )}

      {d && (
        <div className="mt-2 text-xs text-muted-foreground">
          first {d.firstAt ? new Date(d.firstAt).toLocaleDateString() : "—"} · last {d.lastAt ? new Date(d.lastAt).toLocaleString() : "—"}
        </div>
      )}

      {/* Primary: Enter the brain workspace. */}
      <Link
        to="/b/$namespace"
        params={{ namespace: b.namespace }}
        className="mt-4 inline-flex items-center justify-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition hover:opacity-90"
      >
        Enter brain <ArrowRight className="h-4 w-4" />
      </Link>

      {/* Secondary admin actions */}
      <div className="mt-3 flex items-center gap-2 border-t border-border pt-3">
        <button
          onClick={onLaunch}
          className="inline-flex items-center gap-1.5 rounded-lg border border-primary/40 px-2.5 py-1.5 text-xs font-medium text-primary transition hover:bg-primary/10"
        >
          <Rocket className="h-3.5 w-3.5" /> Launch
        </button>
        <a
          href={brainApi.exportUrl(b.namespace)}
          className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
        >
          <Download className="h-3.5 w-3.5" /> Export
        </a>
        <button
          onClick={onDelete}
          className="ms-auto inline-flex items-center gap-1.5 rounded-lg border border-rose-500/40 px-2.5 py-1.5 text-xs font-medium text-rose-500 transition hover:bg-rose-500/10"
        >
          <Trash2 className="h-3.5 w-3.5" /> Delete
        </button>
      </div>
    </div>
  );
}

/** Small global stat chip for the hub hero. */
function HeroStat({ icon: Icon, label, value, loading }: { icon: any; label: string; value: number; loading: boolean }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-border bg-card/70 px-3 py-2 backdrop-blur">
      <Icon className="h-4 w-4 text-primary" />
      <div>
        <div className="text-sm font-semibold tabular-nums text-foreground">{loading ? "—" : value.toLocaleString()}</div>
        <div className="text-[11px] text-muted-foreground">{label}</div>
      </div>
    </div>
  );
}

/** Brains hub — the main entry point of the console. Everything is wired over the
 * brain: this is the landing that lists every brain, and each card opens that
 * brain's scoped workspace. Brains = datasets/namespaces (Cognee calls them "brains"). */
export function BrainsHub() {
  const q = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, refetchInterval: 15_000 });
  const stats = useQuery({ queryKey: ["brain", "stats"], queryFn: brainApi.stats, refetchInterval: 10_000 });
  const brains = q.data?.brains ?? [];
  const [deleteNs, setDeleteNs] = useState<string | null>(null);
  const [launchNs, setLaunchNs] = useState<string | null>(null);
  const loading = stats.isLoading;

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      {/* Neural hero — the brain is the entry point. */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-indigo-500/10 via-violet-500/5 to-teal-400/10 p-6">
        <SynapseField className="opacity-40" />
        <div className="relative">
          <h1 className="text-2xl font-semibold text-foreground">Brains</h1>
          <p className="mb-4 text-sm text-muted-foreground">
            Your organization's shared memory — one store, many brains. Open a brain to explore its graph, sessions, gaps and secrets.
          </p>
          <div className="flex flex-wrap gap-2">
            <HeroStat icon={NeuralGlyph} label="brains" value={stats.data?.brains ?? brains.length} loading={loading} />
            <HeroStat icon={Boxes} label="memories" value={stats.data?.memories ?? 0} loading={loading} />
            <HeroStat icon={Waypoints} label="graph nodes" value={stats.data?.entities ?? 0} loading={loading} />
            <HeroStat icon={HelpCircle} label="open gaps" value={stats.data?.openGaps ?? 0} loading={loading} />
          </div>
        </div>
      </div>

      {brains.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground">
          {q.isLoading ? "Loading…" : "No brains yet. A namespace appears the first time you retain into it."}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
          {brains.map((b) => (
            <BrainCard
              key={b.namespace}
              b={b}
              onDelete={() => setDeleteNs(b.namespace)}
              onLaunch={() => setLaunchNs(b.namespace)}
            />
          ))}
        </div>
      )}

      {/* Tokens, users and cross-brain admin live out of the main flow. */}
      <div className="flex flex-wrap items-center gap-1.5 rounded-xl border border-border bg-card px-4 py-3 text-xs text-muted-foreground">
        <Lock className="h-3.5 w-3.5" /> Tokens, users and cross-brain admin live under
        <Link to="/admin/users" className="font-medium text-primary hover:underline">Admin</Link>.
      </div>

      {deleteNs && <DeleteBrainModal namespace={deleteNs} onClose={() => setDeleteNs(null)} />}
      {launchNs && <LaunchSessionModal namespace={launchNs} onClose={() => setLaunchNs(null)} />}
    </div>
  );
}
