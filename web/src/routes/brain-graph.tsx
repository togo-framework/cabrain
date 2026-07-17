import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Waypoints } from "lucide-react";
import { brainApi } from "../lib/brain";
import { BrainMindmap } from "../components/brain-mindmap";

/** Graph Explorer / Mindmap — radial view of the derived hierarchy
 * (root brain → type nodes → entity nodes), scoped to a chosen brain. */
export function BrainGraph() {
  const [ns, setNs] = useState("");
  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces });
  const g = useQuery({ queryKey: ["brain", "graph", ns], queryFn: () => brainApi.graph(ns, 120) });

  const nodes = g.data?.nodes ?? [];
  const edges = g.data?.edges ?? [];
  const brains = namespaces.data?.brains ?? [];

  return (
    <div className="mx-auto max-w-6xl space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Graph Explorer</h1>
          <p className="text-sm text-muted-foreground">The reasoning memory — a mindmap of each brain's entities by type.</p>
        </div>
        <select
          value={ns}
          onChange={(e) => setNs(e.target.value)}
          className="rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
        >
          <option value="">All brains (overview)</option>
          {brains.map((b) => (
            <option key={b.namespace} value={b.namespace}>
              {b.namespace} ({b.memories.toLocaleString()})
            </option>
          ))}
        </select>
      </div>

      <div className="rounded-xl border border-border bg-card p-2">
        {nodes.length === 0 ? (
          <div className="flex h-[560px] flex-col items-center justify-center gap-2 text-muted-foreground">
            <Waypoints className="h-8 w-8 opacity-40" />
            <div className="text-sm">{g.isLoading ? "Loading graph…" : "No graph yet — retain memories to grow entities."}</div>
          </div>
        ) : (
          <BrainMindmap data={{ ready: true, nodes, edges }} />
        )}
      </div>
      <div className="text-xs text-muted-foreground">
        {nodes.length} nodes · {edges.length} edges
        {g.data?.derived ? " · derived from memory types" : ""} · hover a node to trace its links
      </div>
    </div>
  );
}
