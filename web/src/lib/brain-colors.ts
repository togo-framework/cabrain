// Shared node/group coloring for the brain graph surfaces (dashboard Mindmap
// card + the full Graph Explorer), so both read as one system.
//
// Structural groups get fixed, recognizable hues; every other group (entity
// type names like "post", "doc", "commit", …) gets a stable hashed hue drawn
// from a palette chosen to stay distinct from the structural colors.

// Fixed hues for the structural spine of every brain.
export const STRUCT_COLORS: Record<string, string> = {
  root: "#ec4899", // pink — the brain core
  portfolio: "#8b5cf6", // violet
  venture: "#14b8a6", // teal
  type: "#94a3b8", // slate — the type spine (overview / typed brains)
};

// Palette for entity `group` (typename) coloring. Deliberately avoids the
// pink/violet/teal/slate used above so structural nodes stay distinct.
const ENTITY_PALETTE = [
  "#3b82f6", // blue
  "#f59e0b", // amber
  "#ef4444", // red
  "#10b981", // emerald
  "#f97316", // orange
  "#06b6d4", // cyan
  "#a855f7", // purple
  "#84cc16", // lime
  "#0ea5e9", // sky
  "#d946ef", // fuchsia
  "#eab308", // yellow
  "#22c55e", // green
  "#6366f1", // indigo
  "#f43f5e", // rose
];

function hashHue(key: string): string {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0;
  return ENTITY_PALETTE[h % ENTITY_PALETTE.length];
}

/** Stable color for a node `group`. */
export function colorForGroup(group: string | undefined): string {
  if (group && STRUCT_COLORS[group]) return STRUCT_COLORS[group];
  return hashHue(group ?? "entity");
}

// Human ordering for group columns / legend: structural spine first, then
// entity type groups alphabetically.
const GROUP_RANK: Record<string, number> = { root: 0, portfolio: 1, venture: 2, type: 3 };

/** Sort comparator for group keys (structural first, then alpha). */
export function compareGroups(a: string, b: string): number {
  const ra = GROUP_RANK[a] ?? 100;
  const rb = GROUP_RANK[b] ?? 100;
  if (ra !== rb) return ra - rb;
  return a.localeCompare(b);
}
