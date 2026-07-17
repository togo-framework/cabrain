import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { brainApi, type ActivityItem } from "../lib/brain";

/** Stable colored dot per actor (matches the hub/users scheme). */
function ActorDot({ id }: { id: string }) {
  const hues = ["#6366f1", "#ec4899", "#14b8a6", "#f59e0b", "#8b5cf6", "#ef4444", "#10b981", "#3b82f6"];
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return <span className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: hues[h % hues.length] }} />;
}

function OutcomeBadge({ outcome }: { outcome: string }) {
  const map: Record<string, string> = {
    hit: "bg-emerald-500/15 text-emerald-500",
    empty: "bg-amber-500/15 text-amber-500",
    error: "bg-rose-500/15 text-rose-500",
    running: "bg-sky-500/15 text-sky-500",
    add: "bg-emerald-500/15 text-emerald-500",
    update: "bg-violet-500/15 text-violet-500",
    invalidate: "bg-rose-500/15 text-rose-500",
    noop: "bg-muted text-muted-foreground",
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${map[outcome] ?? "bg-muted text-muted-foreground"}`}>{outcome}</span>;
}

/** This brain's memory activity log — every retain/recall against the namespace,
 * newest first. Reuses the dashboard's activity list, filtered to $namespace. */
export function BrainActivity({ namespace }: { namespace: string }) {
  const q = useQuery({ queryKey: ["brain", "activity"], queryFn: () => brainApi.activity(200), refetchInterval: 8_000 });
  const rows = useMemo(
    () => (q.data?.items ?? []).filter((i) => i.namespace === namespace),
    [q.data, namespace],
  );

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Activity</h1>
        <p className="text-sm text-muted-foreground">
          Every operation against <span className="font-medium text-foreground">{namespace}</span>, newest first.
        </p>
      </div>

      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <Activity className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Memory activity</span>
          {rows.length > 0 && <span className="text-xs text-muted-foreground">{rows.length}</span>}
        </div>
        {q.isLoading ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Loading activity…</div>
        ) : rows.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">
            No activity for this brain yet. Every retain/recall shows up here.
          </div>
        ) : (
          <div className="divide-y divide-border">
            {rows.map((a: ActivityItem) => (
              <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                <ActorDot id={a.agentId || a.namespace || "?"} />
                <span className="font-medium text-foreground">{a.op}</span>
                <span className="text-xs text-muted-foreground">{a.agentId || "—"}</span>
                <span className="ms-auto text-xs text-muted-foreground tabular-nums">{a.latencyMs ? `${a.latencyMs}ms` : ""}</span>
                <OutcomeBadge outcome={a.outcome} />
                <span className="text-xs text-muted-foreground tabular-nums">{a.ts ? new Date(a.ts).toLocaleTimeString() : ""}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
