import { useMemo, useState } from "react";
import type { GraphData, GraphNode } from "../lib/brain";

// Stable palette for entity `group` (typename) coloring — same hues family the
// dashboard uses for actor dots, so the console reads as one system.
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

function colorFor(group: string | undefined): string {
  if (group === "root") return "#f43f5e"; // rose — the brain core
  if (group === "type") return "#94a3b8"; // slate — the type spine
  return hueFor(group ?? "entity");
}

type Placed = GraphNode & { x: number; y: number; r: number };

/**
 * Radial mindmap of the derived hierarchy (root → type → entity).
 * Hand-rolled layout: root at center, type nodes on an inner ring, each type's
 * entities fanned across its angular sector on an outer ring. No deps.
 */
export function BrainMindmap({ data, height = 560 }: { data: GraphData; height?: number }) {
  const [hover, setHover] = useState<string | null>(null);

  const layout = useMemo(() => {
    const W = 960, H = height, cx = W / 2, cy = H / 2;
    const R1 = Math.min(W, H) * 0.24; // type ring
    const R2 = Math.min(W, H) * 0.44; // entity ring

    const nodes = data.nodes ?? [];
    const edges = data.edges ?? [];

    const root = nodes.find((n) => n.group === "root");
    const types = nodes.filter((n) => n.group === "type");
    const typeIds = new Set(types.map((t) => t.id));

    // entity -> its type parent (from edges type -> entity)
    const parentOf = new Map<string, string>();
    for (const e of edges) {
      if (typeIds.has(e.source) && !typeIds.has(e.target) && e.target !== root?.id) {
        parentOf.set(e.target, e.source);
      }
    }
    const entitiesByType = new Map<string, GraphNode[]>();
    for (const n of nodes) {
      if (n.group === "root" || n.group === "type") continue;
      const p = parentOf.get(n.id);
      if (!p) continue;
      const arr = entitiesByType.get(p) ?? [];
      arr.push(n);
      entitiesByType.set(p, arr);
    }

    const pos = new Map<string, Placed>();
    if (root) pos.set(root.id, { ...root, x: cx, y: cy, r: 11 });

    const nT = Math.max(types.length, 1);
    types.forEach((t, i) => {
      const center = (2 * Math.PI * (i + 0.5)) / nT - Math.PI / 2;
      pos.set(t.id, { ...t, x: cx + R1 * Math.cos(center), y: cy + R1 * Math.sin(center), r: 7 });

      const ents = entitiesByType.get(t.id) ?? [];
      const half = (Math.PI / nT) * 0.82; // sector half-width, padded
      const k = ents.length;
      ents.forEach((en, j) => {
        const frac = k > 1 ? j / (k - 1) - 0.5 : 0;
        const a = center + frac * 2 * half;
        pos.set(en.id, { ...en, x: cx + R2 * Math.cos(a), y: cy + R2 * Math.sin(a), r: 4 });
      });
    });

    return { W, H, edges, placed: [...pos.values()], pos };
  }, [data, height]);

  const { W, H, edges, placed, pos } = layout;

  // neighbors of the hovered node (for highlight)
  const neighbors = useMemo(() => {
    if (!hover) return null;
    const set = new Set<string>([hover]);
    for (const e of edges) {
      if (e.source === hover) set.add(e.target);
      if (e.target === hover) set.add(e.source);
    }
    return set;
  }, [hover, edges]);

  const dim = (id: string) => (neighbors && !neighbors.has(id) ? 0.12 : 1);

  // color legend from the type groups present on entities
  const legend = useMemo(() => {
    const groups = new Set<string>();
    for (const n of data.nodes ?? []) {
      if (n.group && n.group !== "root" && n.group !== "type") groups.add(n.group);
    }
    return [...groups].sort();
  }, [data]);

  const hovered = hover ? pos.get(hover) : null;

  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} className="h-[560px] w-full select-none" style={{ height }}>
        {/* edges */}
        {edges.map((e, i) => {
          const a = pos.get(e.source), b = pos.get(e.target);
          if (!a || !b) return null;
          const lit = neighbors && neighbors.has(e.source) && neighbors.has(e.target);
          return (
            <line
              key={i}
              x1={a.x} y1={a.y} x2={b.x} y2={b.y}
              stroke="currentColor"
              className={lit ? "text-primary" : "text-border"}
              strokeWidth={lit ? 1.5 : 1}
              style={{ opacity: neighbors ? (lit ? 0.9 : 0.08) : 0.5 }}
            />
          );
        })}
        {/* nodes */}
        {placed.map((n) => {
          const isStruct = n.group === "root" || n.group === "type";
          return (
            <g
              key={n.id}
              style={{ opacity: dim(n.id), cursor: "pointer" }}
              onMouseEnter={() => setHover(n.id)}
              onMouseLeave={() => setHover((h) => (h === n.id ? null : h))}
            >
              <circle cx={n.x} cy={n.y} r={n.r} fill={colorFor(n.group)}
                stroke="var(--background, #0b0b0f)" strokeWidth={1.5} />
              {isStruct && (
                <text x={n.x} y={n.y - n.r - 5} textAnchor="middle"
                  className="fill-foreground text-[11px] font-medium" style={{ pointerEvents: "none" }}>
                  {n.name.length > 26 ? n.name.slice(0, 25) + "…" : n.name}
                </text>
              )}
            </g>
          );
        })}
        {/* hovered entity label (entities are unlabeled by default to avoid clutter) */}
        {hovered && hovered.group !== "root" && hovered.group !== "type" && (
          <g style={{ pointerEvents: "none" }}>
            <text x={hovered.x} y={hovered.y - hovered.r - 5} textAnchor="middle"
              className="fill-foreground text-[11px] font-medium">
              {hovered.name.length > 44 ? hovered.name.slice(0, 43) + "…" : hovered.name}
            </text>
          </g>
        )}
      </svg>

      {legend.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 px-1 text-xs text-muted-foreground">
          {legend.map((g) => (
            <span key={g} className="inline-flex items-center gap-1.5">
              <span className="h-2.5 w-2.5 rounded-full" style={{ background: colorFor(g) }} />
              {g}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
