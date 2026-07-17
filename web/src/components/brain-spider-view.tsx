import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Maximize2, Minus, Plus } from "lucide-react";
import { type GraphData, type GraphNode } from "../lib/brain";
import { colorForGroup, compareGroups } from "../lib/brain-colors";
import { NodeDetail } from "./brain-graph-view";

// ── Zoom limits (shared feel with the schema view) ─────────────────────────
const MIN_K = 0.15;
const MAX_K = 2.6;

// ── Force-simulation tuning (velocity-Verlet w/ d3-style alpha cooling) ────
const CHARGE = -800; // node-node repulsion strength (negative = repel)
const SPRING = 0.06; // edge spring stiffness toward REST length
const REST = 74; // edge rest length (world units)
const GRAV = 0.06; // centering pull toward the origin (keeps the cloud framed)
const DECAY = 0.62; // per-tick velocity damping (0..1, lower = more friction)
const JITTER = 0.55; // live relaxation jitter, faded out with alpha
const ALPHA_DECAY = 0.018; // how fast the sim cools toward the target
const ALPHA_MIN = 0.0038; // below this the loop parks (until re-heated)
const ALPHA_WARM = 0.85; // alpha to (re)heat to on interaction
const MAX_V = 90; // per-tick velocity clamp (stability at high alpha)
const SEED_SPREAD = 320; // radius of the deterministic seed disc

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

// Deterministic seeded RNG so initial positions are reproducible per node id
// (no Math.random at import; seed from an FNV-1a hash of the id).
function fnv1a(s: string): number {
  let h = 2166136261 >>> 0;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h >>> 0;
}
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// One simulated node — mutated in-place by the rAF loop (never via React state).
type SimNode = {
  id: string;
  name: string;
  group?: string;
  color: string;
  deg: number;
  r: number;
  x: number;
  y: number;
  vx: number;
  vy: number;
  fx: number | null; // pinned world position (from a drag), or null when free
  fy: number | null;
  labeled: boolean; // hub node → always show its label
  gEl: SVGGElement | null; // stable ref target for per-frame transform writes
  setGEl: (el: SVGGElement | null) => void;
};
type SimEdge = {
  s: SimNode;
  t: SimNode;
  color: string;
  lineEl: SVGLineElement | null;
  setLineEl: (el: SVGLineElement | null) => void;
};

type View = { k: number; tx: number; ty: number };

/** Spider view — an animated, force-directed "living brain": nodes settle from
 * deterministic seeds under repulsion + edge springs + centering gravity, run in
 * a single requestAnimationFrame loop that parks when cool and re-heats on
 * interaction. Nodes are draggable (pin), clickable (focus + neighbor highlight),
 * and the whole field pans / zooms. Colors come from the shared brain palette. */
export function SpiderGraphView({ data, namespace }: { data: GraphData; namespace: string }) {
  const [focusId, setFocusId] = useState<string | null>(null);
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [zoomPct, setZoomPct] = useState(100);

  const viewportRef = useRef<HTMLDivElement | null>(null);
  const worldRef = useRef<SVGGElement | null>(null);
  const vpSizeRef = useRef({ w: 0, h: 0 });

  // ── Build the simulation graph (stable objects across re-renders) ─────────
  const sim = useMemo(() => {
    const rawNodes = data.nodes ?? [];
    const rawEdges = data.edges ?? [];

    const deg = new Map<string, number>();
    for (const e of rawEdges) {
      deg.set(e.source, (deg.get(e.source) ?? 0) + 1);
      deg.set(e.target, (deg.get(e.target) ?? 0) + 1);
    }
    const maxDeg = Math.max(1, ...deg.values());

    const nodes: SimNode[] = rawNodes.map((n) => {
      const rng = mulberry32(fnv1a(n.id));
      // seed on a disc (sqrt for uniform area), deterministic per id
      const ang = rng() * Math.PI * 2;
      const rad = Math.sqrt(rng()) * SEED_SPREAD;
      const d = deg.get(n.id) ?? 0;
      const node: SimNode = {
        id: n.id,
        name: n.name,
        group: n.group,
        color: colorForGroup(n.group),
        deg: d,
        r: 4 + Math.min(11, Math.sqrt(d) * 2.3),
        x: Math.cos(ang) * rad,
        y: Math.sin(ang) * rad,
        vx: 0,
        vy: 0,
        fx: null,
        fy: null,
        labeled: d >= Math.max(4, maxDeg * 0.5),
        gEl: null,
        setGEl: () => {},
      };
      node.setGEl = (el) => {
        node.gEl = el;
      };
      return node;
    });

    const byId = new Map(nodes.map((n) => [n.id, n]));

    const edges: SimEdge[] = [];
    const adj = new Map<string, Set<string>>();
    for (const e of rawEdges) {
      const s = byId.get(e.source);
      const t = byId.get(e.target);
      if (!s || !t) continue;
      const edge: SimEdge = {
        s,
        t,
        // color by the non-root endpoint, mirroring the schema view's edges
        color: colorForGroup((s.group === "root" ? t.group : s.group) ?? undefined),
        lineEl: null,
        setLineEl: () => {},
      };
      edge.setLineEl = (el) => {
        edge.lineEl = el;
      };
      edges.push(edge);
      (adj.get(e.source) ?? adj.set(e.source, new Set()).get(e.source)!).add(e.target);
      (adj.get(e.target) ?? adj.set(e.target, new Set()).get(e.target)!).add(e.source);
    }

    const nodeById = new Map(rawNodes.map((n) => [n.id, n]));
    return { nodes, edges, adj, byId, nodeById };
  }, [data]);

  const { nodes, edges, adj, nodeById } = sim;

  // ── Runtime refs (kept out of React to avoid per-frame re-renders) ────────
  const viewRef = useRef<View>({ k: 1, tx: 0, ty: 0 });
  const alphaRef = useRef(ALPHA_WARM);
  const rafRef = useRef<number | null>(null);
  const runningRef = useRef(false);
  const dragRef = useRef<SimNode | null>(null);
  const jitterRng = useRef(mulberry32(0x9e3779b9));

  // Apply the current pan/zoom to the world <g> (imperative → no React churn).
  const applyView = useCallback(() => {
    const g = worldRef.current;
    const v = viewRef.current;
    if (g) g.setAttribute("transform", `translate(${v.tx},${v.ty}) scale(${v.k})`);
  }, []);

  // ── The physics tick ──────────────────────────────────────────────────────
  const tick = useCallback(() => {
    const ns = sim.nodes;
    const es = sim.edges;
    const n = ns.length;
    let alpha = alphaRef.current;
    const rng = jitterRng.current;

    // Repulsion (all-pairs ~ charge/dist²). O(n²): fine ≤ ~250 nodes.
    for (let i = 0; i < n; i++) {
      const a = ns[i];
      for (let j = i + 1; j < n; j++) {
        const b = ns[j];
        let dx = b.x - a.x;
        let dy = b.y - a.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) {
          // coincident → nudge apart deterministically
          dx = (rng() - 0.5) * 0.1;
          dy = (rng() - 0.5) * 0.1;
          d2 = dx * dx + dy * dy + 0.01;
        }
        const w = (CHARGE * alpha) / d2; // negative → push apart
        a.vx += dx * w;
        a.vy += dy * w;
        b.vx -= dx * w;
        b.vy -= dy * w;
      }
    }

    // Edge springs toward REST length.
    for (let i = 0; i < es.length; i++) {
      const { s, t } = es[i];
      const dx = t.x - s.x;
      const dy = t.y - s.y;
      const dist = Math.sqrt(dx * dx + dy * dy) || 0.01;
      const f = ((dist - REST) / dist) * SPRING * alpha;
      const fx = dx * f;
      const fy = dy * f;
      s.vx += fx * 0.5;
      s.vy += fy * 0.5;
      t.vx -= fx * 0.5;
      t.vy -= fy * 0.5;
    }

    // Centering gravity + live jitter, then integrate.
    let kinetic = 0;
    for (let i = 0; i < n; i++) {
      const a = ns[i];
      if (a.fx !== null && a.fy !== null) {
        // pinned (being/was dragged): snap, kill momentum
        a.x = a.fx;
        a.y = a.fy;
        a.vx = 0;
        a.vy = 0;
        continue;
      }
      a.vx += -a.x * GRAV * alpha + (rng() - 0.5) * JITTER * alpha;
      a.vy += -a.y * GRAV * alpha + (rng() - 0.5) * JITTER * alpha;
      a.vx = clamp(a.vx * DECAY, -MAX_V, MAX_V);
      a.vy = clamp(a.vy * DECAY, -MAX_V, MAX_V);
      a.x += a.vx;
      a.y += a.vy;
      kinetic += a.vx * a.vx + a.vy * a.vy;
    }

    // Cool the system.
    alpha += (0 - alpha) * ALPHA_DECAY;
    alphaRef.current = alpha;

    // Write positions to the DOM (transforms + edge endpoints) — one pass, no React.
    for (let i = 0; i < n; i++) {
      const a = ns[i];
      if (a.gEl) a.gEl.setAttribute("transform", `translate(${a.x.toFixed(2)},${a.y.toFixed(2)})`);
    }
    for (let i = 0; i < es.length; i++) {
      const e = es[i];
      const l = e.lineEl;
      if (!l) continue;
      l.setAttribute("x1", e.s.x.toFixed(2));
      l.setAttribute("y1", e.s.y.toFixed(2));
      l.setAttribute("x2", e.t.x.toFixed(2));
      l.setAttribute("y2", e.t.y.toFixed(2));
    }

    // Park when cooled (kinetic energy negligible) unless a drag keeps it warm.
    return alpha > ALPHA_MIN || kinetic > 0.6 || dragRef.current !== null;
  }, [sim]);

  const loop = useCallback(() => {
    const alive = tick();
    if (alive && !document.hidden) {
      rafRef.current = requestAnimationFrame(loop);
    } else {
      runningRef.current = false;
      rafRef.current = null;
    }
  }, [tick]);

  const heat = useCallback(
    (to = ALPHA_WARM) => {
      alphaRef.current = Math.max(alphaRef.current, to);
      if (!runningRef.current) {
        runningRef.current = true;
        rafRef.current = requestAnimationFrame(loop);
      }
    },
    [loop],
  );

  // Kick off (and restart on a new dataset). Re-heat from the seeded layout.
  useEffect(() => {
    // Center the seed cloud (origin) in the viewport before it settles, so the
    // "flying in" happens on-screen rather than off the top-left corner.
    const { w, h } = vpSizeRef.current;
    if (w && h) {
      viewRef.current = { k: 1, tx: w / 2, ty: h / 2 };
      applyView();
      setZoomPct(100);
    }
    alphaRef.current = 1;
    heat(1);
    return () => {
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
      runningRef.current = false;
      rafRef.current = null;
    };
  }, [sim, heat, applyView]);

  // Reset focus/hover on a new brain.
  useEffect(() => {
    setFocusId(null);
    setHoverId(null);
  }, [namespace]);

  // Pause when the tab is hidden; resume when visible again.
  useEffect(() => {
    const onVis = () => {
      if (!document.hidden) heat(Math.max(alphaRef.current, 0.05));
    };
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, [heat]);

  // ── Viewport measurement ──────────────────────────────────────────────────
  useLayoutEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const measure = () => {
      vpSizeRef.current = { w: el.clientWidth, h: el.clientHeight };
    };
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    measure();
    return () => ro.disconnect();
  }, []);

  // ── Fit / re-center (frames the current node cloud, then re-heats) ────────
  const fit = useCallback(
    (reheat = true) => {
      const { w, h } = vpSizeRef.current;
      if (!w || !h || nodes.length === 0) return;
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const nn of nodes) {
        minX = Math.min(minX, nn.x - nn.r);
        minY = Math.min(minY, nn.y - nn.r);
        maxX = Math.max(maxX, nn.x + nn.r);
        maxY = Math.max(maxY, nn.y + nn.r);
      }
      const worldW = Math.max(1, maxX - minX);
      const worldH = Math.max(1, maxY - minY);
      const k = clamp(Math.min(w / (worldW + 120), h / (worldH + 120)), MIN_K, MAX_K);
      const cx = (minX + maxX) / 2;
      const cy = (minY + maxY) / 2;
      viewRef.current = { k, tx: w / 2 - cx * k, ty: h / 2 - cy * k };
      applyView();
      setZoomPct(Math.round(k * 100));
      if (reheat) heat(0.5);
    },
    [nodes, applyView, heat],
  );

  // Auto-fit once shortly after mount (nodes have flown out from their seeds).
  const didFit = useRef("");
  useEffect(() => {
    didFit.current = "";
    const t = window.setTimeout(() => {
      if (didFit.current !== namespace) {
        didFit.current = namespace;
        fit(false);
      }
    }, 650);
    return () => window.clearTimeout(t);
  }, [namespace, fit]);

  // ── Zoom (wheel toward cursor) ────────────────────────────────────────────
  useEffect(() => {
    const el = viewportRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      const v = viewRef.current;
      const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
      const k = clamp(v.k * factor, MIN_K, MAX_K);
      const wx = (mx - v.tx) / v.k;
      const wy = (my - v.ty) / v.k;
      viewRef.current = { k, tx: mx - wx * k, ty: my - wy * k };
      applyView();
      setZoomPct(Math.round(k * 100));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [applyView]);

  const zoomBy = useCallback(
    (factor: number) => {
      const { w, h } = vpSizeRef.current;
      const v = viewRef.current;
      const k = clamp(v.k * factor, MIN_K, MAX_K);
      const cx = w / 2, cy = h / 2;
      const wx = (cx - v.tx) / v.k, wy = (cy - v.ty) / v.k;
      viewRef.current = { k, tx: cx - wx * k, ty: cy - wy * k };
      applyView();
      setZoomPct(Math.round(k * 100));
    },
    [applyView],
  );

  // ── Pointer handling: drag a node (pin) OR pan the empty field ────────────
  const pan = useRef<{ x: number; y: number; tx: number; ty: number; moved: boolean } | null>(null);
  const nodeDrag = useRef<{ node: SimNode; moved: boolean } | null>(null);

  const toWorld = (clientX: number, clientY: number) => {
    const el = viewportRef.current!;
    const rect = el.getBoundingClientRect();
    const v = viewRef.current;
    return {
      x: (clientX - rect.left - v.tx) / v.k,
      y: (clientY - rect.top - v.ty) / v.k,
    };
  };

  const onNodePointerDown = (e: React.PointerEvent, node: SimNode) => {
    e.stopPropagation();
    (e.target as Element).setPointerCapture?.(e.pointerId);
    const p = toWorld(e.clientX, e.clientY);
    node.fx = p.x;
    node.fy = p.y;
    nodeDrag.current = { node, moved: false };
    dragRef.current = node;
    heat(0.6);
  };

  const onPointerDownBg = (e: React.PointerEvent) => {
    (e.target as Element).setPointerCapture?.(e.pointerId);
    const v = viewRef.current;
    pan.current = { x: e.clientX, y: e.clientY, tx: v.tx, ty: v.ty, moved: false };
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const nd = nodeDrag.current;
    if (nd) {
      const p = toWorld(e.clientX, e.clientY);
      nd.node.fx = p.x;
      nd.node.fy = p.y;
      nd.moved = true;
      heat(0.4);
      return;
    }
    const d = pan.current;
    if (!d) return;
    const dx = e.clientX - d.x, dy = e.clientY - d.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true;
    viewRef.current = { ...viewRef.current, tx: d.tx + dx, ty: d.ty + dy };
    applyView();
  };

  const onPointerUp = (e: React.PointerEvent) => {
    const nd = nodeDrag.current;
    if (nd) {
      nodeDrag.current = null;
      dragRef.current = null;
      if (!nd.moved) {
        // treated as a click → toggle focus; free the node again
        nd.node.fx = null;
        nd.node.fy = null;
        setFocusId((cur) => (cur === nd.node.id ? null : nd.node.id));
      }
      // (a real drag keeps the node pinned where it was dropped)
      heat(0.35);
      return;
    }
    const d = pan.current;
    pan.current = null;
    if (d && !d.moved && (e.target as HTMLElement).dataset.bg === "1") setFocusId(null);
  };

  // Double-click a node → unpin it back into the simulation.
  const onNodeDoubleClick = (e: React.MouseEvent, node: SimNode) => {
    e.stopPropagation();
    node.fx = null;
    node.fy = null;
    heat(0.5);
  };

  const recenter = () => {
    // clear all pins and re-relax from the current layout
    for (const nn of nodes) {
      nn.fx = null;
      nn.fy = null;
    }
    heat(1);
    fit(false);
  };

  // ── Focus / highlight sets ────────────────────────────────────────────────
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

  const labelSet = useMemo(() => {
    const s = new Set<string>();
    if (neighbors) for (const id of neighbors) s.add(id);
    if (hoverId) s.add(hoverId);
    return s;
  }, [neighbors, hoverId]);

  // ── Static graph subtree (memoized so pan/zoom never rebuild it) ──────────
  // Depends only on structure + focus/hover; the rAF loop mutates positions.
  const graph = useMemo(() => {
    const dim = (id: string) => (neighbors ? !neighbors.has(id) : false);
    return (
      <>
        <g>
          {edges.map((e, i) => {
            const lit = focusId ? e.s.id === focusId || e.t.id === focusId : false;
            const faded = neighbors ? !lit : false;
            return (
              <line
                key={i}
                ref={e.setLineEl}
                stroke={e.color}
                strokeWidth={lit ? 1.9 : 1}
                strokeOpacity={faded ? 0.04 : lit ? 0.85 : 0.2}
                strokeLinecap="round"
              />
            );
          })}
        </g>
        <g>
          {nodes.map((n) => {
            const isFocus = n.id === focusId;
            const faded = dim(n.id);
            const showLabel = isFocus || n.labeled || labelSet.has(n.id);
            return (
              <g
                key={n.id}
                ref={n.setGEl}
                className="brain-node cursor-pointer"
                style={{ opacity: faded ? 0.12 : 1 }}
                onPointerDown={(e) => onNodePointerDown(e, n)}
                onDoubleClick={(e) => onNodeDoubleClick(e, n)}
                onPointerEnter={() => setHoverId(n.id)}
                onPointerLeave={() => setHoverId((h) => (h === n.id ? null : h))}
              >
                {/* enlarged transparent hit target (easier to grab small nodes) */}
                <circle r={Math.max(n.r + 8, 13)} fill="transparent" />
                {/* soft synapse halo */}
                <circle
                  className={`brain-halo${n.labeled ? " pulse" : ""}`}
                  r={n.r * 2.3}
                  fill={n.color}
                  opacity={isFocus ? 0.32 : 0.16}
                  style={{ pointerEvents: "none" }}
                />
                {/* core */}
                <circle
                  r={n.r}
                  fill={n.color}
                  stroke={isFocus ? "#fff" : n.color}
                  strokeOpacity={isFocus ? 0.9 : 0.35}
                  strokeWidth={isFocus ? 2 : 1}
                  filter={isFocus ? "url(#spider-glow)" : undefined}
                />
                {n.fx !== null && (
                  <circle r={n.r + 3.5} fill="none" stroke={n.color} strokeOpacity={0.7} strokeWidth={1} strokeDasharray="2 2" style={{ pointerEvents: "none" }} />
                )}
                {showLabel && (
                  <text
                    x={0}
                    y={-n.r - 5}
                    textAnchor="middle"
                    className="fill-current text-foreground/90"
                    style={{
                      pointerEvents: "none",
                      fontSize: isFocus ? 12 : 10,
                      fontWeight: isFocus ? 600 : 500,
                      paintOrder: "stroke",
                      stroke: "var(--color-background, #0b0b0f)",
                      strokeWidth: 3,
                      strokeLinejoin: "round",
                    }}
                  >
                    {n.name.length > 26 ? n.name.slice(0, 25) + "…" : n.name}
                  </text>
                )}
              </g>
            );
          })}
        </g>
      </>
    );
    // onNode* handlers are stable via refs; deliberately excluded from deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes, edges, focusId, neighbors, labelSet]);

  return (
    <div className="relative flex min-h-0 flex-1">
      {/* keyframes for the living-brain halo pulse (inline → CSP-safe) */}
      <style>{`
        @keyframes brainPulse { 0%,100% { transform: scale(1); } 50% { transform: scale(1.14); } }
        .brain-halo { transform-box: fill-box; transform-origin: center; }
        .brain-halo.pulse { animation: brainPulse 3.6s ease-in-out infinite; }
        .brain-node:hover .brain-halo { opacity: 0.42 !important; }
      `}</style>

      <div
        ref={viewportRef}
        className="relative min-w-0 flex-1 overflow-hidden bg-[radial-gradient(circle_at_1px_1px,theme(colors.border)_1px,transparent_0)] [background-size:22px_22px]"
        style={{ cursor: pan.current ? "grabbing" : "grab", touchAction: "none" }}
        onPointerDown={onPointerDownBg}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
      >
        {/* Neural vignette — a soft central glow for the "living brain" feel */}
        <div
          className="pointer-events-none absolute inset-0 opacity-70"
          style={{ background: "radial-gradient(ellipse at 50% 42%, rgb(99 102 241 / 0.07), transparent 62%)" }}
        />
        {/* Background hit layer (click empty space to clear focus) */}
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
                Clear focus
              </button>
            </div>
          </div>
        )}

        <svg className="absolute inset-0 h-full w-full overflow-visible" style={{ pointerEvents: "none" }}>
          <defs>
            <filter id="spider-glow" x="-80%" y="-80%" width="260%" height="260%">
              <feGaussianBlur stdDeviation="3.2" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          {/* world group — pan/zoom via imperative transform; children own their positions */}
          <g ref={worldRef} style={{ pointerEvents: "auto" }}>
            {graph}
          </g>
        </svg>

        {/* Zoom / fit controls */}
        <div className="absolute bottom-3 left-3 z-20 flex flex-col overflow-hidden rounded-lg border border-border bg-card/95 shadow-sm backdrop-blur">
          <button onClick={() => zoomBy(1.2)} className="flex h-8 w-8 items-center justify-center text-muted-foreground hover:bg-muted hover:text-foreground" title="Zoom in">
            <Plus className="h-4 w-4" />
          </button>
          <button onClick={() => zoomBy(1 / 1.2)} className="flex h-8 w-8 items-center justify-center border-t border-border text-muted-foreground hover:bg-muted hover:text-foreground" title="Zoom out">
            <Minus className="h-4 w-4" />
          </button>
          <button onClick={recenter} className="flex h-8 w-8 items-center justify-center border-t border-border text-muted-foreground hover:bg-muted hover:text-foreground" title="Fit & re-heat">
            <Maximize2 className="h-4 w-4" />
          </button>
        </div>
        <div className="absolute bottom-3 left-14 z-20 rounded-md border border-border bg-card/90 px-2 py-1 text-[11px] tabular-nums text-muted-foreground backdrop-blur">
          {zoomPct}%
        </div>
        <div className="pointer-events-none absolute bottom-3 right-3 z-20 rounded-md border border-border bg-card/80 px-2 py-1 text-[10px] text-muted-foreground backdrop-blur">
          drag to move · double-click to release · scroll to zoom
        </div>
      </div>

      {/* Shared detail panel (reused from the schema view) */}
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
  );
}
