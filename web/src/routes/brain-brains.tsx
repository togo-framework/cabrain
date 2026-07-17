import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Download, Trash2, AlertTriangle, HelpCircle, Search, Rocket,
  ArrowRight, Boxes, Waypoints, Lock, Activity as ActivityIcon,
} from "lucide-react";
import { brainApi, type BrainDetail, type NamespaceInfo } from "../lib/brain";
import { SynapseField, NeuralGlyph, NeuralCellMark } from "../components/neural";
import { hueForBrain as hueFor } from "../lib/brain-colors";
import { LaunchSessionModal } from "../components/launch-session-modal";

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm" onClick={onClose}>
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

/** A brain's living identity for compact contexts (breadcrumbs, dense lists): a
 * neural glyph in the brain's own stable hue with a soft synaptic glow. */
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

/** A memory-type as a synapse terminal — a glowing dot + label, NOT a bar. A
 * cluster of these reads like dendrite endings rather than a spreadsheet. */
function TypeSynapse({ label, count }: { label: string; count: number }) {
  const c = hueFor(label);
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] text-muted-foreground"
      style={{ background: `${c}12` }}
      title={`${label} · ${count.toLocaleString()}`}
    >
      <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: c, boxShadow: `0 0 6px ${c}` }} />
      <span className="truncate text-foreground/80">{label}</span>
      <span className="tabular-nums opacity-70">{count.toLocaleString()}</span>
    </span>
  );
}

/** One brain, rendered as a living neural cell — an organic node in the map, not
 * a database row. A pulsing cytoplasm glow in the brain's own hue fills the
 * membrane, faint synapse filaments drift inside, and the firing soma
 * (NeuralGlyph) is its face. Key facts read as synapse terminals, and the whole
 * cell is the primary "Enter brain" gesture. */
function BrainCell({ b, onDelete, onLaunch }: { b: NamespaceInfo; onDelete: () => void; onLaunch: () => void }) {
  const c = hueFor(b.namespace);
  const detail = useQuery({
    queryKey: ["brain", "detail", b.namespace],
    queryFn: () => brainApi.brainDetail(b.namespace),
  });
  const d: BrainDetail | undefined = detail.data && !detail.data.error ? detail.data : undefined;

  const types = d ? Object.entries(d.types).sort((a, e) => e[1] - a[1]) : [];
  const topTypes = types.slice(0, 4);

  return (
    <div
      className="cb-fire group relative flex flex-col overflow-hidden rounded-[1.75rem] p-5 transition duration-300 hover:-translate-y-1"
      style={{
        border: `1px solid ${c}22`,
        background: `radial-gradient(120% 80% at 50% -10%, ${c}1f, transparent 60%), color-mix(in srgb, var(--card) 78%, transparent)`,
        boxShadow: `0 12px 40px -22px ${c}, inset 0 0 40px -30px ${c}`,
        backdropFilter: "blur(8px)",
      }}
    >
      {/* pulsing cytoplasm — the cell's living glow (organic, not a rectangle) */}
      <div
        className="cb-halo pointer-events-none absolute -right-10 -top-14 h-52 w-52 rounded-full blur-2xl"
        style={{ background: `radial-gradient(circle, ${c}55, transparent 62%)` }}
      />
      {/* faint synapse filaments drifting through the membrane */}
      <SynapseField className="opacity-[0.10] transition-opacity duration-300 group-hover:opacity-20" />

      {/* Stretched link — the whole cell enters the brain (no nested anchors:
          the action controls below sit above this overlay via z-index). */}
      <Link
        to="/b/$namespace" params={{ namespace: b.namespace }}
        aria-label={`Enter ${b.namespace}`} title={`Enter ${b.namespace}`}
        className="absolute inset-0 z-[1] rounded-[1.75rem]"
      />

      {/* Identity: firing soma + namespace + open-gaps synapse-warning */}
      <div className="pointer-events-none relative z-[2] flex items-center gap-3">
        <NeuralCellMark color={c} size={54} />
        <div className="min-w-0 flex-1">
          <div
            className="truncate text-base font-semibold text-foreground transition group-hover:text-primary"
            style={{ textShadow: `0 0 18px ${c}33` }}
          >
            {b.namespace}
          </div>
          <div className="text-[11px] text-muted-foreground">
            {b.lastAt ? `firing · ${new Date(b.lastAt).toLocaleDateString()}` : "dormant"}
          </div>
        </div>
        {d && d.openGaps > 0 && (
          <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-500">
            <HelpCircle className="h-3 w-3" /> {d.openGaps}
          </span>
        )}
      </div>

      {/* Mass of the cell — memory count */}
      <div className="pointer-events-none relative z-[2] mt-4 flex items-baseline gap-2">
        <span className="text-3xl font-semibold tabular-nums text-foreground" style={{ textShadow: `0 0 22px ${c}44` }}>
          {b.memories.toLocaleString()}
        </span>
        <span className="text-xs text-muted-foreground">memories</span>
        {d && (
          <span className="ms-auto inline-flex items-center gap-1 text-[11px] text-muted-foreground">
            <Search className="h-3 w-3" /> {d.recalls.toLocaleString()}
          </span>
        )}
      </div>

      {/* Dendrite terminals — top memory types as glowing synapse dots */}
      <div className="pointer-events-none relative z-[2] mt-3 flex min-h-[2.75rem] flex-wrap content-start gap-1.5">
        {detail.isLoading ? (
          <span className="text-xs text-muted-foreground">Sensing dendrites…</span>
        ) : topTypes.length > 0 ? (
          topTypes.map(([t, n]) => <TypeSynapse key={t} label={t} count={n} />)
        ) : (
          <span className="text-xs text-muted-foreground">Fresh cell — no memory types yet.</span>
        )}
      </div>

      {/* Primary synapse — enter the brain (visual; the stretched link handles the click). */}
      <div
        className="pointer-events-none relative z-[2] mt-4 inline-flex items-center justify-center gap-1.5 rounded-xl px-3 py-2.5 text-sm font-medium text-white transition group-hover:brightness-110"
        style={{ background: `linear-gradient(135deg, ${c}, ${c}bb)`, boxShadow: `0 6px 20px -8px ${c}` }}
      >
        Enter brain <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
      </div>

      {/* Secondary admin synapses — elevated above the stretched link so they're
          independently clickable. */}
      <div className="relative z-[3] mt-3 flex items-center gap-2 border-t pt-3" style={{ borderColor: `${c}1f` }}>
        <button
          onClick={onLaunch}
          className="inline-flex items-center gap-1.5 rounded-lg border border-border/70 bg-card/60 px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
        >
          <Rocket className="h-3.5 w-3.5" /> Launch
        </button>
        <a
          href={brainApi.exportUrl(b.namespace)}
          className="inline-flex items-center gap-1.5 rounded-lg border border-border/70 bg-card/60 px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
        >
          <Download className="h-3.5 w-3.5" /> Export
        </a>
        <button
          onClick={onDelete}
          title="Delete brain"
          className="ms-auto rounded-lg border border-rose-500/40 bg-card/60 p-1.5 text-rose-500 transition hover:bg-rose-500/10"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

/** A glowing stat node for the neural header — reads as a synapse, not a KPI box. */
function HeroStat({ icon: Icon, label, value, loading }: { icon: any; label: string; value: number; loading: boolean }) {
  return (
    <div className="group relative flex items-center gap-2.5 overflow-hidden rounded-xl border border-border/60 bg-card/60 px-3.5 py-2.5 backdrop-blur">
      <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10 text-primary shadow-[0_0_16px_-6px] shadow-primary/60">
        <Icon className="h-4 w-4" />
      </span>
      <div>
        <div className="text-base font-semibold tabular-nums leading-none text-foreground">{loading ? "—" : value.toLocaleString()}</div>
        <div className="mt-0.5 text-[11px] text-muted-foreground">{label}</div>
      </div>
    </div>
  );
}

/** Brains hub — the flagship. The landing reads as looking *at* a brain: a neural
 * map where every brain is a glowing, firing cell wired into one organism. Each
 * cell opens that brain's scoped workspace. */
export function BrainsHub() {
  const q = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, refetchInterval: 15_000 });
  const stats = useQuery({ queryKey: ["brain", "stats"], queryFn: brainApi.stats, refetchInterval: 10_000 });
  const brains = q.data?.brains ?? [];
  const [deleteNs, setDeleteNs] = useState<string | null>(null);
  const [launchNs, setLaunchNs] = useState<string | null>(null);
  const loading = stats.isLoading;

  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      {/* Neural header — the organism at a glance. */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-indigo-500/15 via-violet-500/8 to-teal-400/12 p-6">
        <SynapseField className="opacity-50" />
        <div className="relative">
          <div className="mb-1 flex items-center gap-3">
            <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white shadow-[0_0_22px_-4px] shadow-violet-500/60">
              <NeuralGlyph className="h-6 w-6 cb-breathe" />
            </span>
            <div>
              <h1 className="text-2xl font-semibold text-foreground">Your brains</h1>
              <p className="text-sm text-muted-foreground">
                One shared memory, many living brains — enter a cell to explore its graph, sessions, sources and gaps.
              </p>
            </div>
          </div>
          <div className="mt-4 flex flex-wrap gap-2">
            <HeroStat icon={NeuralGlyph} label="brains" value={stats.data?.brains ?? brains.length} loading={loading} />
            <HeroStat icon={Boxes} label="memories" value={stats.data?.memories ?? 0} loading={loading} />
            <HeroStat icon={Waypoints} label="graph nodes" value={stats.data?.entities ?? 0} loading={loading} />
            <HeroStat icon={ActivityIcon} label="recalls · 24h" value={stats.data?.recalls24h ?? 0} loading={loading} />
            <HeroStat icon={HelpCircle} label="open gaps" value={stats.data?.openGaps ?? 0} loading={loading} />
          </div>
        </div>
      </div>

      {/* The neural map — cells wired together by faint flowing synapses behind them. */}
      {brains.length === 0 ? (
        <div className="relative overflow-hidden rounded-2xl border border-border bg-card/70 p-12 text-center">
          <SynapseField className="opacity-30" />
          <div className="relative mx-auto max-w-sm">
            <span className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary"><NeuralGlyph className="h-6 w-6" /></span>
            <div className="text-sm text-muted-foreground">
              {q.isLoading ? "Waking the network…" : "No brains yet. A brain forms the first time an agent retains a memory into a namespace."}
            </div>
          </div>
        </div>
      ) : (
        <div className="relative">
          {/* decorative connective tissue — synapses weaving between the cells */}
          <SynapseField className="opacity-[0.12]" />
          <div className="relative grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {brains.map((b) => (
              <BrainCell
                key={b.namespace}
                b={b}
                onDelete={() => setDeleteNs(b.namespace)}
                onLaunch={() => setLaunchNs(b.namespace)}
              />
            ))}
          </div>
        </div>
      )}

      {/* Tokens, users and cross-brain admin live out of the main flow. */}
      <div className="flex flex-wrap items-center gap-1.5 rounded-xl border border-border bg-card/70 px-4 py-3 text-xs text-muted-foreground backdrop-blur">
        <Lock className="h-3.5 w-3.5" /> Tokens, users and cross-brain admin live under
        <Link to="/admin/users" className="font-medium text-primary hover:underline">Admin</Link>.
      </div>

      {deleteNs && <DeleteBrainModal namespace={deleteNs} onClose={() => setDeleteNs(null)} />}
      {launchNs && <LaunchSessionModal namespace={launchNs} onClose={() => setLaunchNs(null)} />}
    </div>
  );
}
