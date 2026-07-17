// Brain / neural visual language — hand-rolled SVG glyphs and animated synapse
// fields. No deps (strict CSP); everything inherits `currentColor` and the theme
// tokens so it works in dark/light and never fights @togo-framework/ui. The CSS
// keyframes these use live in app.css (cb-* classes) and honor reduced-motion.

/** Compact neuron glyph (soma + dendrites + synapse dots) for the sidebar mark. */
export function NeuralGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6}
      strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
      <circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />
      <path d="M12 9V4M12 15v5M9 12H4M15 12h5M9.9 9.9 6.4 6.4M14.1 14.1l3.5 3.5M14.1 9.9l3.5-3.5M9.9 14.1l-3.5 3.5" />
      <circle cx="4" cy="4" r="1.3" fill="currentColor" stroke="none" />
      <circle cx="20" cy="4" r="1.3" fill="currentColor" stroke="none" />
      <circle cx="4" cy="20" r="1.3" fill="currentColor" stroke="none" />
      <circle cx="20" cy="20" r="1.3" fill="currentColor" stroke="none" />
    </svg>
  );
}

// Shared gradient stops for every synapse surface (indigo → violet → teal).
function SynEdgeDefs({ id }: { id: string }) {
  return (
    <defs>
      <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stopColor="#6366f1" />
        <stop offset="50%" stopColor="#8b5cf6" />
        <stop offset="100%" stopColor="#14b8a6" />
      </linearGradient>
    </defs>
  );
}

/**
 * Faint synapse field — animated nodes + edges rendered behind hero content.
 * Absolutely positioned; the parent should be `relative overflow-hidden`.
 *
 * Uses `preserveAspectRatio="xMidYMid slice"` so the mesh scales uniformly and
 * COVERS its box without ever squishing — critical on wide/short hero panels
 * where `none` used to stretch it into distorted diagonal streaks. Nodes are
 * spread across the full canvas so a cropped band still reads as alive.
 */
export function SynapseField({ className = "" }: { className?: string }) {
  const nodes = [
    [8, 26], [20, 58], [33, 20], [46, 46], [60, 16],
    [66, 68], [79, 34], [90, 60], [14, 80], [44, 82],
    [72, 90], [88, 14], [26, 40], [54, 66], [12, 50], [82, 78],
  ] as const;
  const edges: [number, number][] = [
    [0, 1], [0, 2], [2, 3], [1, 3], [3, 4], [3, 6], [4, 6], [5, 7], [6, 7], [7, 8],
    [1, 8], [3, 9], [5, 9], [4, 5], [11, 6], [12, 3], [13, 9], [14, 1], [15, 7], [10, 9],
  ];
  return (
    <svg
      viewBox="0 0 100 100" preserveAspectRatio="xMidYMid slice" aria-hidden
      className={`pointer-events-none absolute inset-0 h-full w-full ${className}`}
    >
      <SynEdgeDefs id="syn-edge" />
      {edges.map(([a, b], i) => (
        <line
          key={i}
          x1={nodes[a][0]} y1={nodes[a][1]} x2={nodes[b][0]} y2={nodes[b][1]}
          stroke="url(#syn-edge)" strokeWidth={0.35} vectorEffect="non-scaling-stroke"
        />
      ))}
      {nodes.map(([x, y], i) => (
        <circle key={i} cx={x} cy={y} r={0.9} fill="url(#syn-edge)">
          <animate
            attributeName="opacity" values="0.35;1;0.35" dur={`${2.4 + (i % 4) * 0.7}s`}
            begin={`${(i % 5) * 0.4}s`} repeatCount="indefinite"
          />
        </circle>
      ))}
    </svg>
  );
}

// A wider, sparser neural mesh for full-page ambient backdrops — flowing axons
// (dashed, signal-carrying) plus twinkling synapse nodes. Deterministic layout
// (no jank) and very low opacity so it reads as texture, never noise.
const BACKDROP_NODES: [number, number][] = [
  [6, 18], [17, 44], [11, 74], [26, 12], [30, 62], [22, 90],
  [41, 30], [38, 82], [50, 52], [55, 16], [48, 96], [63, 40],
  [60, 74], [72, 22], [76, 58], [70, 92], [84, 36], [88, 70],
  [94, 14], [92, 50], [83, 88], [4, 54], [34, 46], [66, 8],
];
const BACKDROP_EDGES: [number, number][] = [
  [0, 1], [1, 2], [0, 3], [3, 6], [1, 4], [4, 5], [6, 7], [6, 8], [8, 4],
  [8, 11], [9, 6], [9, 13], [11, 12], [12, 15], [11, 14], [14, 16], [16, 17],
  [13, 16], [17, 20], [16, 19], [18, 19], [9, 23], [21, 2], [22, 8], [10, 12],
];

/**
 * NeuralBackdrop — an ambient, full-bleed neural mesh meant to sit behind an
 * entire page or layout so the whole product feels like one brain. Render it as
 * a `fixed inset-0` sibling behind your content (give the content `relative z-10`).
 * Decorative only (`pointer-events-none`, aria-hidden). Opacity is intentionally
 * low; tune per surface with `className`.
 */
export function NeuralBackdrop({ className = "" }: { className?: string }) {
  return (
    <div className={`pointer-events-none fixed inset-0 overflow-hidden ${className}`} aria-hidden>
      {/* soft radial pools of neural light */}
      <div className="absolute -left-32 -top-24 h-96 w-96 rounded-full bg-indigo-500/10 blur-3xl" />
      <div className="absolute right-[-10rem] top-1/3 h-[28rem] w-[28rem] rounded-full bg-violet-500/10 blur-3xl" />
      <div className="absolute bottom-[-8rem] left-1/3 h-96 w-96 rounded-full bg-teal-400/10 blur-3xl" />
      <svg viewBox="0 0 100 100" preserveAspectRatio="xMidYMid slice"
        className="absolute inset-0 h-full w-full opacity-[0.5]">
        <SynEdgeDefs id="syn-backdrop" />
        {BACKDROP_EDGES.map(([a, b], i) => {
          const [x1, y1] = BACKDROP_NODES[a];
          const [x2, y2] = BACKDROP_NODES[b];
          return (
            <line key={i} x1={x1} y1={y1} x2={x2} y2={y2}
              stroke="url(#syn-backdrop)" strokeWidth={0.18} vectorEffect="non-scaling-stroke"
              className={i % 3 === 0 ? "cb-flow" : undefined}
              style={i % 3 === 0 ? { animationDelay: `${(i % 5) * 1.3}s` } : undefined} />
          );
        })}
        {BACKDROP_NODES.map(([x, y], i) => (
          <circle key={i} cx={x} cy={y} r={i % 4 === 0 ? 0.7 : 0.45}
            fill="url(#syn-backdrop)" className="cb-spark"
            style={{ animationDelay: `${(i % 6) * 0.5}s` }} />
        ))}
      </svg>
    </div>
  );
}

/**
 * NeuralCellMark — a brain's living identity: a breathing soma inside a pulsing
 * radial halo with two expanding synapse rings, all tinted in the brain's own
 * `color`. Each brain's avatar on the neural hub. The firing rings are
 * decorative (`pointer-events-none` via inline layout); the glyph is the mark.
 */
export function NeuralCellMark({ color, size = 56, firing = true }: { color: string; size?: number; firing?: boolean }) {
  return (
    <span
      className="relative inline-flex shrink-0 items-center justify-center rounded-full"
      style={{ height: size, width: size }}
      aria-hidden
    >
      {/* pulsing radial halo */}
      <span
        className={firing ? "cb-halo absolute inset-0 rounded-full" : "absolute inset-0 rounded-full"}
        style={{ background: `radial-gradient(circle at 50% 45%, ${color}55, ${color}14 60%, transparent 72%)` }}
      />
      {/* expanding synapse rings (firing) */}
      {firing && (
        <>
          <span className="cb-ring absolute inset-0 rounded-full" style={{ border: `1px solid ${color}` }} />
          <span className="cb-ring absolute inset-0 rounded-full" style={{ border: `1px solid ${color}`, animationDelay: "1.6s" }} />
        </>
      )}
      {/* soma — the neuron core */}
      <span
        className={`relative flex items-center justify-center rounded-full ${firing ? "cb-breathe" : ""}`}
        style={{
          height: "62%", width: "62%",
          background: `radial-gradient(circle at 35% 30%, ${color}, ${color}aa)`,
          boxShadow: `0 0 18px -2px ${color}, inset 0 0 8px -3px #fff8`,
          color: "#fff",
        }}
      >
        <NeuralGlyph className="h-3/5 w-3/5" />
      </span>
    </span>
  );
}
