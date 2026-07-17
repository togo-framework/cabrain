import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HelpCircle, Check, X, RotateCcw } from "lucide-react";
import { brainApi, type Gap, type GapStatus } from "../lib/brain";

const STATUS_META: Record<string, { label: string; cls: string }> = {
  open: { label: "Open", cls: "bg-amber-500/15 text-amber-500" },
  indexed: { label: "Indexed", cls: "bg-emerald-500/15 text-emerald-500" },
  dismissed: { label: "Dismissed", cls: "bg-muted text-muted-foreground" },
};
const ORDER: Array<keyof typeof STATUS_META> = ["open", "indexed", "dismissed"];

function GapRow({ g, onResolve, busy }: { g: Gap; onResolve: (s: GapStatus) => void; busy: boolean }) {
  return (
    <div className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium text-foreground" title={g.query}>{g.query}</div>
        <div className="mt-0.5 flex flex-wrap gap-2 text-xs text-muted-foreground">
          <span className="rounded bg-muted px-1.5 py-0.5">{g.namespace}</span>
          <span>{g.hits} {g.hits === 1 ? "miss" : "misses"}</span>
          <span>first {g.firstSeen ? new Date(g.firstSeen).toLocaleDateString() : "—"}</span>
          <span>last {g.lastSeen ? new Date(g.lastSeen).toLocaleString() : "—"}</span>
          {g.resolution && <span className="italic">“{g.resolution}”</span>}
        </div>
      </div>
      <div className="flex shrink-0 gap-2">
        {g.status !== "indexed" && (
          <button
            onClick={() => onResolve("indexed")}
            disabled={busy}
            className="inline-flex items-center gap-1 rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1.5 text-xs font-medium text-emerald-500 transition hover:bg-emerald-500/20 disabled:opacity-50"
          >
            <Check className="h-3.5 w-3.5" /> Mark indexed
          </button>
        )}
        {g.status !== "dismissed" && (
          <button
            onClick={() => onResolve("dismissed")}
            disabled={busy}
            className="inline-flex items-center gap-1 rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition hover:bg-muted disabled:opacity-50"
          >
            <X className="h-3.5 w-3.5" /> Dismiss
          </button>
        )}
        {g.status !== "open" && (
          <button
            onClick={() => onResolve("open")}
            disabled={busy}
            className="inline-flex items-center gap-1 rounded-lg border border-amber-500/40 px-2.5 py-1.5 text-xs font-medium text-amber-500 transition hover:bg-amber-500/10 disabled:opacity-50"
          >
            <RotateCcw className="h-3.5 w-3.5" /> Reopen
          </button>
        )}
      </div>
    </div>
  );
}

/** Knowledge gaps. When `namespace` is supplied (brain workspace) it locks to
 * that brain and hides the brain selector, keeping the status filter. */
export function BrainGaps({ namespace }: { namespace?: string } = {}) {
  const scoped = !!namespace;
  const qc = useQueryClient();
  const [statusFilter, setStatusFilter] = useState<string>(""); // "" = all
  const [nsState, setNsState] = useState<string>("");
  const nsFilter = scoped ? namespace! : nsState;

  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, enabled: !scoped });
  // Fetch with server filters when set; the "" default returns open+indexed, so to
  // get dismissed too we ask per status when "all" is selected.
  const gaps = useQuery({
    queryKey: ["brain", "gaps", "all", statusFilter, nsFilter],
    queryFn: async () => {
      if (statusFilter) return brainApi.gaps({ status: statusFilter, namespace: nsFilter, limit: 200 });
      // "All statuses": union of the three explicit statuses.
      const [open, indexed, dismissed] = await Promise.all([
        brainApi.gaps({ status: "open", namespace: nsFilter, limit: 200 }),
        brainApi.gaps({ status: "indexed", namespace: nsFilter, limit: 200 }),
        brainApi.gaps({ status: "dismissed", namespace: nsFilter, limit: 200 }),
      ]);
      return { gaps: [...(open.gaps ?? []), ...(indexed.gaps ?? []), ...(dismissed.gaps ?? [])] };
    },
    refetchInterval: 15_000,
  });

  const resolve = useMutation({
    mutationFn: (v: { id: number; status: GapStatus }) => brainApi.resolveGap(v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["brain", "gaps"] });
      qc.invalidateQueries({ queryKey: ["brain", "stats"] });
    },
  });

  const grouped = useMemo(() => {
    const all = gaps.data?.gaps ?? [];
    const m: Record<string, Gap[]> = { open: [], indexed: [], dismissed: [] };
    for (const g of all) (m[g.status] ?? (m[g.status] = [])).push(g);
    return m;
  }, [gaps.data]);

  const brains = namespaces.data?.brains ?? [];
  const visibleStatuses = statusFilter ? [statusFilter] : ORDER;
  const total = (gaps.data?.gaps ?? []).length;

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Knowledge gaps</h1>
          <p className="text-sm text-muted-foreground">
            Recall queries that came back thin or empty. Index the ones worth capturing; dismiss the noise.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <select
            value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}
            className="rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:border-primary"
          >
            <option value="">All statuses</option>
            {ORDER.map((s) => <option key={s} value={s}>{STATUS_META[s].label}</option>)}
          </select>
          {!scoped && (
            <select
              value={nsFilter} onChange={(e) => setNsState(e.target.value)}
              className="rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:border-primary"
            >
              <option value="">All brains</option>
              {brains.map((b) => <option key={b.namespace} value={b.namespace}>{b.namespace}</option>)}
            </select>
          )}
        </div>
      </div>

      {gaps.isLoading ? (
        <div className="rounded-xl border border-border bg-card px-4 py-10 text-center text-sm text-muted-foreground">Loading gaps…</div>
      ) : total === 0 ? (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-border bg-card px-4 py-12 text-center text-sm text-muted-foreground">
          <HelpCircle className="h-8 w-8 opacity-40" />
          No gaps here — every recall is finding memories.
        </div>
      ) : (
        visibleStatuses.map((s) => {
          const rows = grouped[s] ?? [];
          if (rows.length === 0) return null;
          const meta = STATUS_META[s];
          return (
            <div key={s} className="rounded-xl border border-border bg-card">
              <div className="flex items-center gap-2 border-b border-border px-4 py-3">
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${meta.cls}`}>{meta.label}</span>
                <span className="text-xs text-muted-foreground">{rows.length}</span>
              </div>
              <div className="divide-y divide-border">
                {rows.map((g) => (
                  <GapRow
                    key={g.id}
                    g={g}
                    busy={resolve.isPending && resolve.variables?.id === g.id}
                    onResolve={(status) => resolve.mutate({ id: g.id, status })}
                  />
                ))}
              </div>
            </div>
          );
        })
      )}
    </div>
  );
}
