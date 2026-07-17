// Brain / neural visual language — a hand-rolled SVG glyph and a soft synapse
// backdrop. No deps; inherits `currentColor` and the theme tokens so it works in
// dark/light and never fights @togo-framework/ui.

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

/**
 * Faint synapse field — animated nodes + edges rendered behind hero content.
 * Absolutely positioned; the parent should be `relative overflow-hidden`.
 */
export function SynapseField({ className = "" }: { className?: string }) {
  const nodes = [
    [8, 30], [22, 62], [34, 22], [48, 48], [61, 18],
    [67, 70], [80, 38], [90, 64], [15, 82], [44, 84],
  ] as const;
  const edges: [number, number][] = [
    [0, 1], [0, 2], [2, 3], [1, 3], [3, 4], [3, 6], [4, 6], [5, 7], [6, 7], [7, 8], [1, 8], [3, 9], [5, 9], [4, 5],
  ];
  return (
    <svg
      viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden
      className={`pointer-events-none absolute inset-0 h-full w-full ${className}`}
    >
      <defs>
        <linearGradient id="syn-edge" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#6366f1" />
          <stop offset="50%" stopColor="#8b5cf6" />
          <stop offset="100%" stopColor="#14b8a6" />
        </linearGradient>
      </defs>
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
