import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Waypoints, Loader2 } from "lucide-react";
import { brainApi } from "../lib/brain";
import { BrainGraphView } from "../components/brain-graph-view";

/** Graph Explorer — a Cognee-style "memory schema" of a brain: nodes grouped
 * into columns by type, curved connection lines, click-to-focus with a detail
 * panel, and zoom / pan / fit. Scoped to a chosen brain (namespace). */
export function BrainGraph() {
  const [ns, setNs] = useState("");
  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces });
  const g = useQuery({ queryKey: ["brain", "graph", ns], queryFn: () => brainApi.graph(ns, 250) });

  const brains = namespaces.data?.brains ?? [];

  // Default to the richest brain (most memories) for a full memory-schema view.
  useEffect(() => {
    if (ns === "" && brains.length > 0) {
      const top = [...brains].sort((a, b) => b.memories - a.memories)[0];
      if (top) setNs(top.namespace);
    }
  }, [brains, ns]);

  const nodes = g.data?.nodes ?? [];
  const edges = g.data?.edges ?? [];

  return (
    <div className="flex h-[calc(100vh-3.5rem)] flex-col">
      <div className="flex flex-wrap items-end justify-between gap-2 border-b border-border px-6 py-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Graph Explorer</h1>
          <p className="text-sm text-muted-foreground">
            The reasoning memory — each brain's entities grouped by type. Click a card to focus its links.
          </p>
        </div>
        <div className="flex items-center gap-3">
          {(g.isFetching || namespaces.isLoading) && (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          )}
          <span className="hidden text-xs text-muted-foreground sm:inline">
            {nodes.length} nodes · {edges.length} edges
            {g.data?.derived ? " · derived" : ""}
          </span>
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
      </div>

      {nodes.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground">
          <Waypoints className="h-8 w-8 opacity-40" />
          <div className="text-sm">
            {g.isLoading ? "Loading graph…" : "No graph yet — retain memories to grow entities."}
          </div>
        </div>
      ) : (
        <BrainGraphView key={ns} data={{ ready: true, nodes, edges }} namespace={ns} />
      )}
    </div>
  );
}
