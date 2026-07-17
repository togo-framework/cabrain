import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Database, Download, Trash2, AlertTriangle, HelpCircle, Search } from "lucide-react";
import { brainApi, type BrainDetail, type NamespaceInfo } from "../lib/brain";

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

function BrainCard({ b, onDelete }: { b: NamespaceInfo; onDelete: () => void }) {
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
    <div className="flex flex-col rounded-xl border border-border bg-card p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary"><Database className="h-4 w-4" /></span>
        <span className="font-medium text-foreground">{b.namespace}</span>
        {d && d.openGaps > 0 && (
          <span className="ms-auto inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-500">
            <HelpCircle className="h-3 w-3" /> {d.openGaps} {d.openGaps === 1 ? "gap" : "gaps"}
          </span>
        )}
      </div>

      <div className="text-2xl font-semibold tabular-nums text-foreground">{b.memories.toLocaleString()}</div>
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

      {/* Admin */}
      <div className="mt-4 flex items-center gap-2 border-t border-border pt-3">
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

/** Brains = datasets/namespaces (Cognee calls them "brains"). */
export function BrainBrains() {
  const q = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, refetchInterval: 15_000 });
  const brains = q.data?.brains ?? [];
  const [deleteNs, setDeleteNs] = useState<string | null>(null);

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Brains</h1>
        <p className="text-sm text-muted-foreground">Namespaces — each an isolated scope over the one shared store.</p>
      </div>
      {brains.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground">
          {q.isLoading ? "Loading…" : "No brains yet. A namespace appears the first time you retain into it."}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
          {brains.map((b) => (
            <BrainCard key={b.namespace} b={b} onDelete={() => setDeleteNs(b.namespace)} />
          ))}
        </div>
      )}
      {deleteNs && <DeleteBrainModal namespace={deleteNs} onClose={() => setDeleteNs(null)} />}
    </div>
  );
}
