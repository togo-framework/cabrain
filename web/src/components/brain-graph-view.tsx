import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Maximize2, Minus, Plus, X, Link2, FileText, Loader2 } from "lucide-react";
import { Badge, Button } from "@togo-framework/ui";
import { brainApi, type GraphData, type GraphNode } from "../lib/brain";
import { colorForGroup, compareGroups } from "../lib/brain-colors";

// ── Layout geometry (world coordinates) ───────────────────────────────────
const CARD_W = 190;
const CARD_H = 44;
const CARD_GAP = 9;
const COL_PITCH = CARD_W + 104; // column stride — the gap is the edge lane
const HEADER_H = 40; // column header (group name + count)
const PAD = 44;
const MIN_K = 0.15;
const MAX_K = 2.4;

type Placed = { node: GraphNode; x: number; y: number };
type Column = { group: string; count: number; nodes: GraphNode[]; x: number };

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/** Cubic-bezier path between two card anchors, routed horizontally. */
function edgePath(a: Placed, b: Placed): string {
  const forward = b.x >= a.x;
  const sx = forward ? a.x + CARD_W : a.x;
  const sy = a.y + CARD_H / 2;
  const tx = forward ? b.x : b.x + CARD_W;
  const ty = b.y + CARD_H / 2;
  const dx = Math.max(40, Math.abs(tx - sx) * 0.5) * (forward ? 1 : -1);
  return `M ${sx} ${sy} C ${sx + dx} ${sy}, ${tx - dx} ${ty}, ${tx} ${ty}`;
}

export function BrainGraphView({ data, namespace }: { data: GraphData; namespace: string }) {
  const [focusId, setFocusId] = useState<string | null>(null);
  const [view, setView] = useState({ k: 1, tx: 0, ty: 0 });
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const [vpSize, setVpSize] = useState({ w: 0, h: 0 });

  // ── Columnar layout: group → column, index → row ────────────────────────
  const layout = useMemo(() => {
    const nodes = data.nodes ?? [];
    const edges = data.edges ?? [];

    const byGroup = new Map<string, GraphNode[]>();
    for (const n of nodes) {
      const g = n.group ?? "entity";
      const arr = byGroup.get(g) ?? [];
      arr.push(n);
      byGroup.set(g, arr);
    }
    const groups = [...byGroup.keys()].sort(compareGroups);

    const pos = new Map<string, Placed>();
    const columns: Column[] = groups.map((group, ci) => {
      const colNodes = byGroup.get(group)!;
      const x = PAD + ci * COL_PITCH;
      colNodes.forEach((node, ri) => {
        pos.set(node.id, { node, x, y: PAD + HEADER_H + ri * (CARD_H + CARD_GAP) });
      });
      return { group, count: colNodes.length, nodes: colNodes, x };
    });

    const maxRows = Math.max(1, ...columns.map((c) => c.nodes.length));
    const worldW = PAD * 2 + Math.max(0, columns.length - 1) * COL_PITCH + CARD_W;
    const worldH = PAD * 2 + HEADER_H + maxRows * (CARD_H + CARD_GAP);

    // adjacency for focus / neighbor lists
    const adj = new Map<string, Set<string>>();
    for (const e of edges) {
      if (!pos.has(e.source) || !pos.has(e.target)) continue;
      (adj.get(e.source) ?? adj.set(e.source, new Set()).get(e.source)!).add(e.target);
      (adj.get(e.target) ?? adj.set(e.target, new Set()).get(e.target)!).add(e.source);
    }

    const nodeById = new Map(nodes.map((n) => [n.id, n]));
    return { columns, pos, edges, worldW, worldH, adj, nodeById };
  }, [data]);

  const { columns, pos, edges, worldW, worldH, adj, nodeById } = layout;

  // ── Measure viewport ────────────────────────────────────────────────────
  useLayoutEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      setVpSize({ w: el.clientWidth, h: el.clientHeight });
    });
    ro.observe(el);
    setVpSize({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  // ── Fit the whole world into the viewport ───────────────────────────────
  const fit = useCallback(() => {
    if (!vpSize.w || !vpSize.h) return;
    const k = clamp(Math.min(vpSize.w / (worldW + 40), vpSize.h / (worldH + 40)), MIN_K, MAX_K);
    setView({ k, tx: (vpSize.w - worldW * k) / 2, ty: (vpSize.h - worldH * k) / 2 });
  }, [vpSize, worldW, worldH]);

  // Auto-fit on new dataset / first measure.
  const fitKey = `${namespace}|${worldW}|${worldH}|${vpSize.w}x${vpSize.h}`;
  const lastFit = useRef("");
  useEffect(() => {
    if (!vpSize.w || lastFit.current === fitKey) return;
    lastFit.current = fitKey;
    fit();
  }, [fitKey, vpSize.w, fit]);

  // Clear focus when the dataset changes.
  useEffect(() => setFocusId(null), [namespace]);

  // ── Zoom (wheel toward cursor) ──────────────────────────────────────────
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      setView((v) => {
        const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
        const k = clamp(v.k * factor, MIN_K, MAX_K);
        const wx = (mx - v.tx) / v.k;
        const wy = (my - v.ty) / v.k;
        return { k, tx: mx - wx * k, ty: my - wy * k };
      });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const zoomBy = (factor: number) =>
    setView((v) => {
      const k = clamp(v.k * factor, MIN_K, MAX_K);
      const cx = vpSize.w / 2, cy = vpSize.h / 2;
      const wx = (cx - v.tx) / v.k, wy = (cy - v.ty) / v.k;
      return { k, tx: cx - wx * k, ty: cy - wy * k };
    });

  // ── Pan (drag background) ───────────────────────────────────────────────
  const drag = useRef<{ x: number; y: number; tx: number; ty: number; moved: boolean } | null>(null);
  const onPointerDown = (e: React.PointerEvent) => {
    (e.target as Element).setPointerCapture?.(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, tx: view.tx, ty: view.ty, moved: false };
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x, dy = e.clientY - d.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true;
    setView((v) => ({ ...v, tx: d.tx + dx, ty: d.ty + dy }));
  };
  const onPointerUp = (e: React.PointerEvent) => {
    const d = drag.current;
    drag.current = null;
    // A click on empty background (no drag) clears focus.
    if (d && !d.moved && (e.target as HTMLElement).dataset.bg === "1") setFocusId(null);
  };

  // ── Focus / highlight sets ──────────────────────────────────────────────
  const neighbors = useMemo(() => {
    if (!focusId) return null;
    const set = new Set<string>([focusId]);
    for (const id of adj.get(focusId) ?? []) set.add(id);
    return set;
  }, [focusId, adj]);

  const focusNode = focusId ? nodeById.get(focusId) : null;
  const neighborNodes: GraphNode[] = useMemo(() => {
    if (!focusId) return [];
    return [...(adj.get(focusId) ?? [])]
      .map((id) => nodeById.get(id))
      .filter((n): n is GraphNode => !!n)
      .sort((a, b) => compareGroups(a.group ?? "", b.group ?? "") || a.name.localeCompare(b.name));
  }, [focusId, adj, nodeById]);

  // ── Legend (group → color, with counts) ─────────────────────────────────
  const legend = columns.map((c) => ({ group: c.group, count: c.count, color: colorForGroup(c.group) }));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* Legend */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border px-3 py-2 text-xs">
        {legend.map((l) => (
          <span key={l.group} className="inline-flex items-center gap-1.5 text-muted-foreground">
            <span className="h-2.5 w-2.5 rounded-[3px]" style={{ background: l.color }} />
            <span className="capitalize text-foreground/80">{l.group}</span>
            <span className="tabular-nums opacity-60">{l.count}</span>
          </span>
        ))}
      </div>

      <div className="relative flex min-h-0 flex-1">
        {/* ── Canvas ── */}
        <div
          ref={viewportRef}
          className="relative min-w-0 flex-1 overflow-hidden bg-[radial-gradient(circle_at_1px_1px,theme(colors.border)_1px,transparent_0)] [background-size:22px_22px]"
          style={{ cursor: drag.current ? "grabbing" : "grab", touchAction: "none" }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
        >
          {/* Background hit layer (click to clear focus) */}
          <div data-bg="1" className="absolute inset-0" />

          {/* Focus banner */}
          {focusNode && (
            <div className="pointer-events-none absolute inset-x-0 top-0 z-20 flex justify-center p-3">
              <div className="pointer-events-auto flex items-center gap-2 rounded-full border border-border bg-card/95 px-3 py-1.5 text-xs shadow-sm backdrop-blur">
                <span className="h-2 w-2 rounded-full" style={{ background: colorForGroup(focusNode.group) }} />
                <span className="text-muted-foreground">Focused on</span>
                <span className="max-w-[220px] truncate font-medium text-foreground">{focusNode.name}</span>
                <button
                  onClick={() => setFocusId(null)}
                  className="ml-1 inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <X className="h-3 w-3" /> Clear focus
                </button>
              </div>
            </div>
          )}

          {/* Transformed world */}
          <div
            className="absolute left-0 top-0 origin-top-left"
            style={{ transform: `translate(${view.tx}px, ${view.ty}px) scale(${view.k})` }}
          >
            {/* Edges */}
            <svg
              width={worldW}
              height={worldH}
              className="pointer-events-none absolute left-0 top-0 overflow-visible"
            >
              {edges.map((e, i) => {
                const a = pos.get(e.source), b = pos.get(e.target);
                if (!a || !b) return null;
                const color = colorForGroup((a.node.group === "root" ? b.node.group : a.node.group) ?? undefined);
                const lit = focusId ? e.source === focusId || e.target === focusId : false;
                const opacity = focusId ? (lit ? 0.95 : 0.05) : 0.28;
                return (
                  <path
                    key={i}
                    d={edgePath(a, b)}
                    fill="none"
                    stroke={color}
                    strokeWidth={lit ? 2.25 : 1.25}
                    strokeOpacity={opacity}
                  />
                );
              })}
            </svg>

            {/* Column headers */}
            {columns.map((c) => (
              <div
                key={`h-${c.group}`}
                className="absolute flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide"
                style={{ left: c.x, top: PAD - 6, width: CARD_W }}
              >
                <span className="h-2.5 w-2.5 rounded-[3px]" style={{ background: colorForGroup(c.group) }} />
                <span className="truncate text-foreground/90">{c.group}</span>
                <span className="tabular-nums text-muted-foreground">{c.count}</span>
              </div>
            ))}

            {/* Cards */}
            {columns.map((c) =>
              c.nodes.map((n) => {
                const p = pos.get(n.id)!;
                const color = colorForGroup(n.group);
                const isFocus = n.id === focusId;
                const inFocus = !neighbors || neighbors.has(n.id);
                return (
                  <button
                    key={n.id}
                    onPointerDown={(e) => e.stopPropagation()}
                    onClick={(e) => {
                      e.stopPropagation();
                      setFocusId(n.id);
                    }}
                    title={n.name}
                    className="absolute flex items-center gap-2 rounded-md border bg-card px-2.5 text-left shadow-sm transition-opacity"
                    style={{
                      left: p.x,
                      top: p.y,
                      width: CARD_W,
                      height: CARD_H,
                      opacity: inFocus ? 1 : 0.12,
                      borderColor: isFocus ? color : undefined,
                      boxShadow: isFocus ? `0 0 0 2px ${color}` : undefined,
                    }}
                  >
                    <span className="h-6 w-1 shrink-0 rounded-full" style={{ background: color }} />
                    <span className="min-w-0 flex-1 truncate text-xs font-medium text-foreground">{n.name}</span>
                  </button>
                );
              }),
            )}
          </div>

          {/* Zoom / fit controls (bottom-left, Cognee-style) */}
          <div className="absolute bottom-3 left-3 z-20 flex flex-col overflow-hidden rounded-lg border border-border bg-card/95 shadow-sm backdrop-blur">
            <button onClick={() => zoomBy(1.2)} className="flex h-8 w-8 items-center justify-center text-muted-foreground hover:bg-muted hover:text-foreground" title="Zoom in">
              <Plus className="h-4 w-4" />
            </button>
            <button onClick={() => zoomBy(1 / 1.2)} className="flex h-8 w-8 items-center justify-center border-t border-border text-muted-foreground hover:bg-muted hover:text-foreground" title="Zoom out">
              <Minus className="h-4 w-4" />
            </button>
            <button onClick={fit} className="flex h-8 w-8 items-center justify-center border-t border-border text-muted-foreground hover:bg-muted hover:text-foreground" title="Fit to view">
              <Maximize2 className="h-4 w-4" />
            </button>
          </div>
          <div className="absolute bottom-3 left-14 z-20 rounded-md border border-border bg-card/90 px-2 py-1 text-[11px] tabular-nums text-muted-foreground backdrop-blur">
            {Math.round(view.k * 100)}%
          </div>
        </div>

        {/* ── Detail panel ── */}
        {focusNode && (
          <NodeDetail
            key={focusNode.id}
            node={focusNode}
            namespace={namespace}
            neighbors={neighborNodes}
            onFocus={setFocusId}
            onClose={() => setFocusId(null)}
          />
        )}
      </div>
    </div>
  );
}

// ── Right detail panel ──────────────────────────────────────────────────────
function NodeDetail({
  node,
  namespace,
  neighbors,
  onFocus,
  onClose,
}: {
  node: GraphNode;
  namespace: string;
  neighbors: GraphNode[];
  onFocus: (id: string) => void;
  onClose: () => void;
}) {
  const uuid = node.id.startsWith("ent:") ? node.id.slice(4) : null;
  const mem = useQuery({
    queryKey: ["brain", "memory", namespace, uuid],
    queryFn: () => brainApi.getMemory(namespace, uuid!),
    enabled: !!uuid && !!namespace,
  });

  const color = colorForGroup(node.group);
  const content = mem.data && !mem.data.error ? mem.data.content : null;

  return (
    <aside className="flex w-80 shrink-0 flex-col border-l border-border bg-card">
      <div className="flex items-start justify-between gap-2 border-b border-border p-4">
        <div className="min-w-0">
          <div className="mb-1.5 flex items-center gap-1.5">
            <span className="h-2.5 w-2.5 rounded-[3px]" style={{ background: color }} />
            <Badge variant="outline" className="capitalize">{node.group ?? "node"}</Badge>
          </div>
          <h2 className="break-words text-sm font-semibold leading-snug text-foreground">{node.name}</h2>
        </div>
        <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" onClick={onClose}>
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-4">
        {/* Connections */}
        <div className="mb-4">
          <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            <Link2 className="h-3.5 w-3.5" /> Connections
            <span className="tabular-nums opacity-70">{neighbors.length}</span>
          </div>
          {neighbors.length === 0 ? (
            <p className="text-xs text-muted-foreground">No direct connections.</p>
          ) : (
            <ul className="space-y-1">
              {neighbors.map((n) => (
                <li key={n.id}>
                  <button
                    onClick={() => onFocus(n.id)}
                    className="flex w-full items-center gap-2 rounded-md border border-transparent px-2 py-1.5 text-left hover:border-border hover:bg-muted"
                  >
                    <span className="h-4 w-1 shrink-0 rounded-full" style={{ background: colorForGroup(n.group) }} />
                    <span className="min-w-0 flex-1 truncate text-xs text-foreground">{n.name}</span>
                    <span className="shrink-0 text-[10px] capitalize text-muted-foreground">{n.group}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Memory content (entity nodes only) */}
        {uuid && (
          <div>
            <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              <FileText className="h-3.5 w-3.5" /> Memory
            </div>
            {mem.isLoading ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading memory…
              </div>
            ) : content ? (
              <>
                {(mem.data?.memoryType || mem.data?.sourceRef) && (
                  <div className="mb-2 flex flex-wrap gap-1.5 text-[10px] text-muted-foreground">
                    {mem.data?.memoryType && <Badge variant="secondary" className="font-normal">{mem.data.memoryType}</Badge>}
                    {mem.data?.network && <Badge variant="secondary" className="font-normal">{mem.data.network}</Badge>}
                    {mem.data?.sourceKind && <Badge variant="secondary" className="font-normal">{mem.data.sourceKind}</Badge>}
                  </div>
                )}
                <pre className="max-h-[46vh] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-background p-3 text-xs leading-relaxed text-foreground/90">
                  {content}
                </pre>
              </>
            ) : (
              <p className="text-xs text-muted-foreground">
                {mem.data?.error ? mem.data.error.message : "No memory content."}
              </p>
            )}
          </div>
        )}
      </div>
    </aside>
  );
}
