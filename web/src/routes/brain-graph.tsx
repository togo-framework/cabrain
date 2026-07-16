import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Waypoints } from "lucide-react";
import { brainApi } from "../lib/brain";

/** Graph Explorer / Mindmap — deterministic circular layout of the entity
 * subgraph (nodes = entities, edges = co-occurrence via memory_entities). */
export function BrainGraph() {
  const [ns, setNs] = useState("");
  const g = useQuery({ queryKey: ["brain", "graph", ns], queryFn: () => brainApi.graph(ns, 150) });

  const nodes = g.data?.nodes ?? [];
  const edges = g.data?.edges ?? [];
  const W = 900, H = 560, cx = W / 2, cy = H / 2, R = Math.min(W, H) / 2 - 60;
  const pos = new Map<string, { x: number; y: number }>();
  nodes.forEach((n, i) => {
    const a = (i / Math.max(nodes.length, 1)) * Math.PI * 2;
    pos.set(n.id, { x: cx + R * Math.cos(a), y: cy + R * Math.sin(a) });
  });

  return (
    <div className="mx-auto max-w-6xl space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Graph Explorer</h1>
          <p className="text-sm text-muted-foreground">The reasoning memory — entities and their relationships.</p>
        </div>
        <input value={ns} onChange={(e) => setNs(e.target.value)} placeholder="filter by brain (namespace)"
          className="rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary" />
      </div>

      <div className="rounded-xl border border-border bg-card p-2">
        {nodes.length === 0 ? (
          <div className="flex h-[560px] flex-col items-center justify-center gap-2 text-muted-foreground">
            <Waypoints className="h-8 w-8 opacity-40" />
            <div className="text-sm">{g.isLoading ? "Loading graph…" : "No graph yet — retain memories to grow entities."}</div>
          </div>
        ) : (
          <svg viewBox={`0 0 ${W} ${H}`} className="h-[560px] w-full">
            {edges.map((e, i) => {
              const a = pos.get(e.source), b = pos.get(e.target);
              if (!a || !b) return null;
              return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y} stroke="currentColor" className="text-border" strokeWidth={1} />;
            })}
            {nodes.map((n) => {
              const p = pos.get(n.id)!;
              return (
                <g key={n.id}>
                  <circle cx={p.x} cy={p.y} r={6} className="fill-primary" />
                  <text x={p.x + 9} y={p.y + 4} className="fill-foreground text-[11px]">{n.name}</text>
                </g>
              );
            })}
          </svg>
        )}
      </div>
      <div className="text-xs text-muted-foreground">{nodes.length} nodes · {edges.length} edges</div>
    </div>
  );
}
