import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Boxes, Waypoints, Share2, Search as SearchIcon, HelpCircle, Lock, Network, ArrowRight,
} from "lucide-react";
import { brainApi, type ActivityItem } from "../lib/brain";
import { BrainGraphView } from "../components/brain-graph-view";

/** Compact stat tile for a single brain. */
function Tile({ label, value, loading, icon: Icon, tone, to, namespace }: {
  label: string; value: number; loading: boolean; icon: any;
  tone?: "warn"; to?: string; namespace: string;
}) {
  const zero = !loading && value === 0;
  const warn = tone === "warn" && !zero;
  const body = (
    <div className="relative overflow-hidden rounded-xl border border-border bg-card p-4 transition hover:border-primary/40">
      <div className="pointer-events-none absolute -right-6 -top-8 h-20 w-20 rounded-full bg-gradient-to-br from-indigo-500/15 via-violet-500/10 to-teal-400/10 blur-xl" />
      <div className="relative mb-2 flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        <Icon className={`h-4 w-4 ${warn ? "text-amber-500" : zero ? "text-muted-foreground/40" : "text-primary"}`} />
      </div>
      <div className={`relative text-2xl font-semibold tabular-nums ${warn ? "text-amber-500" : zero ? "text-muted-foreground/50" : "text-foreground"}`}>
        {loading ? "—" : value.toLocaleString()}
      </div>
    </div>
  );
  if (to) return <Link to={to} params={{ namespace }}>{body}</Link>;
  return body;
}

/** Brain Overview — the flagship. "Everything wired over the brain": the rich
 * graph explorer for THIS brain is the centerpiece, framed by the brain's live
 * stat tiles and a compact recent-activity strip. */
export function BrainOverview({ namespace }: { namespace: string }) {
  const detail = useQuery({
    queryKey: ["brain", "detail", namespace],
    queryFn: () => brainApi.brainDetail(namespace),
    refetchInterval: 15_000,
  });
  const graph = useQuery({
    queryKey: ["brain", "graph", namespace],
    queryFn: () => brainApi.graph(namespace, 250),
  });
  const secrets = useQuery({
    queryKey: ["brain", "secrets", namespace],
    queryFn: () => brainApi.secrets(namespace),
  });
  const activity = useQuery({
    queryKey: ["brain", "activity"],
    queryFn: () => brainApi.activity(200),
    refetchInterval: 8_000,
  });

  const d = detail.data && !detail.data.error ? detail.data : undefined;
  const nodes = graph.data?.nodes ?? [];
  const edges = graph.data?.edges ?? [];
  const secretCount = secrets.data?.secrets?.length ?? 0;
  const recent = useMemo(
    () => (activity.data?.items ?? []).filter((i) => i.namespace === namespace).slice(0, 8),
    [activity.data, namespace],
  );
  const loading = detail.isLoading;

  return (
    <div className="flex flex-col gap-4 p-6">
      {/* Live stat tiles for this brain */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
        <Tile label="Memories" value={d?.memories ?? 0} loading={loading} icon={Boxes} namespace={namespace} />
        <Tile label="Graph nodes" value={nodes.length} loading={graph.isLoading} icon={Waypoints} namespace={namespace} />
        <Tile label="Graph edges" value={edges.length} loading={graph.isLoading} icon={Share2} namespace={namespace} />
        <Tile label="Recalls" value={d?.recalls ?? 0} loading={loading} icon={SearchIcon} namespace={namespace} />
        <Tile label="Open gaps" value={d?.openGaps ?? 0} loading={loading} icon={HelpCircle} tone="warn" to="/b/$namespace/gaps" namespace={namespace} />
        <Tile label="Secrets" value={secretCount} loading={secrets.isLoading} icon={Lock} to="/b/$namespace/secrets" namespace={namespace} />
      </div>

      {/* The graph — centerpiece */}
      <div className="flex h-[600px] flex-col overflow-hidden rounded-xl border border-border bg-card">
        <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <Network className="h-4 w-4 text-primary" />
            <span className="text-sm font-medium">Memory graph</span>
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {nodes.length} nodes · {edges.length} edges{graph.data?.derived ? " · derived" : ""}
          </span>
        </div>
        {nodes.length === 0 ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground">
            <Network className="h-8 w-8 opacity-40" />
            <div className="text-sm">{graph.isLoading ? "Loading graph…" : "No graph yet — retain memories to grow entities."}</div>
          </div>
        ) : (
          <BrainGraphView key={namespace} data={{ ready: true, nodes, edges }} namespace={namespace} />
        )}
      </div>

      {/* Recent activity strip */}
      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
          <span className="text-sm font-medium">Recent activity</span>
          <Link to="/b/$namespace/activity" params={{ namespace }} className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">
            View all <ArrowRight className="h-3 w-3" />
          </Link>
        </div>
        {recent.length === 0 ? (
          <div className="px-4 py-8 text-center text-sm text-muted-foreground">
            {activity.isLoading ? "Loading…" : "No activity for this brain yet."}
          </div>
        ) : (
          <div className="divide-y divide-border">
            {recent.map((a: ActivityItem) => (
              <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                <span className="font-medium text-foreground">{a.op}</span>
                <span className="text-xs text-muted-foreground">{a.agentId || "—"}</span>
                <span className="ms-auto text-xs text-muted-foreground tabular-nums">{a.latencyMs ? `${a.latencyMs}ms` : ""}</span>
                <span className="text-xs text-muted-foreground tabular-nums">{a.ts ? new Date(a.ts).toLocaleTimeString() : ""}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
