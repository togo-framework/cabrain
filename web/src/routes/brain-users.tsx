import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Users, Shield } from "lucide-react";
import { brainApi, type ActivityItem } from "../lib/brain";

/** Stable colored dot per actor — same scheme as the dashboard. */
function ActorDot({ id }: { id: string }) {
  const hues = ["#6366f1", "#ec4899", "#14b8a6", "#f59e0b", "#8b5cf6", "#ef4444", "#10b981", "#3b82f6"];
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return <span className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ background: hues[h % hues.length] }} />;
}

function Outcome({ o }: { o: string }) {
  const m: Record<string, string> = {
    hit: "bg-emerald-500/15 text-emerald-500", empty: "bg-amber-500/15 text-amber-500",
    error: "bg-rose-500/15 text-rose-500", running: "bg-sky-500/15 text-sky-500",
    add: "bg-emerald-500/15 text-emerald-500", update: "bg-violet-500/15 text-violet-500",
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${m[o] ?? "bg-muted text-muted-foreground"}`}>{o}</span>;
}

export function BrainUsers() {
  const [selected, setSelected] = useState<string>(""); // "" = all agents

  // Identities come from tokens (declared agents) unioned with agents seen in the
  // activity stream (agents that have actually done something).
  const tokens = useQuery({ queryKey: ["brain", "tokens"], queryFn: brainApi.tokens });
  const activity = useQuery({ queryKey: ["brain", "activity"], queryFn: () => brainApi.activity(200), refetchInterval: 8_000 });

  const items = activity.data?.items ?? [];
  const tokenList = tokens.data?.tokens ?? [];

  const agents = useMemo(() => {
    const map = new Map<string, { agentId: string; isAdmin: boolean; actions: number; lastAt: string }>();
    for (const t of tokenList) {
      if (!t.agentId) continue;
      map.set(t.agentId, { agentId: t.agentId, isAdmin: t.isAdmin, actions: 0, lastAt: "" });
    }
    for (const i of items) {
      const id = i.agentId || "(anonymous)";
      const cur = map.get(id) ?? { agentId: id, isAdmin: false, actions: 0, lastAt: "" };
      cur.actions += 1;
      if (!cur.lastAt || (i.ts && i.ts > cur.lastAt)) cur.lastAt = i.ts;
      map.set(id, cur);
    }
    return [...map.values()].sort((a, b) => b.actions - a.actions || a.agentId.localeCompare(b.agentId));
  }, [tokenList, items]);

  const rows = useMemo(() => {
    if (!selected) return items;
    if (selected === "(anonymous)") return items.filter((i) => !i.agentId);
    return items.filter((i) => i.agentId === selected);
  }, [items, selected]);

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Users &amp; activity</h1>
        <p className="text-sm text-muted-foreground">Identities are agent ids. Pick an agent to see its recent actions.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_1fr]">
        {/* Agent list */}
        <div className="rounded-xl border border-border bg-card">
          <div className="flex items-center gap-2 border-b border-border px-4 py-3">
            <Users className="h-4 w-4 text-primary" />
            <span className="text-sm font-medium">Agents</span>
            <span className="text-xs text-muted-foreground">{agents.length}</span>
          </div>
          <div className="divide-y divide-border">
            <button
              onClick={() => setSelected("")}
              className={`flex w-full items-center gap-2 px-4 py-2.5 text-left text-sm transition hover:bg-muted ${selected === "" ? "bg-muted" : ""}`}
            >
              <span className="font-medium text-foreground">All agents</span>
              <span className="ms-auto text-xs text-muted-foreground">{items.length}</span>
            </button>
            {agents.map((a) => (
              <button
                key={a.agentId}
                onClick={() => setSelected(a.agentId)}
                className={`flex w-full items-center gap-2 px-4 py-2.5 text-left text-sm transition hover:bg-muted ${selected === a.agentId ? "bg-muted" : ""}`}
              >
                <ActorDot id={a.agentId} />
                <span className="min-w-0 flex-1 truncate font-medium text-foreground">{a.agentId}</span>
                {a.isAdmin && <Shield className="h-3 w-3 shrink-0 text-violet-500" />}
                <span className="text-xs text-muted-foreground">{a.actions}</span>
              </button>
            ))}
            {agents.length === 0 && (
              <div className="px-4 py-8 text-center text-xs text-muted-foreground">
                {tokens.isLoading || activity.isLoading ? "Loading…" : "No agents yet."}
              </div>
            )}
          </div>
        </div>

        {/* Actions for the selected agent */}
        <div className="rounded-xl border border-border bg-card">
          <div className="flex items-center gap-2 border-b border-border px-4 py-3">
            <span className="text-sm font-medium">{selected ? `Actions · ${selected}` : "Recent actions"}</span>
            <span className="ms-auto text-xs text-muted-foreground">{rows.length}</span>
          </div>
          {rows.length === 0 ? (
            <div className="px-4 py-10 text-center text-sm text-muted-foreground">
              {activity.isLoading ? "Loading…" : "No activity for this agent yet."}
            </div>
          ) : (
            <div className="divide-y divide-border">
              {rows.map((a: ActivityItem) => (
                <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                  <span className="font-medium text-foreground">{a.op}</span>
                  <span className="text-muted-foreground">{a.namespace || "—"}</span>
                  <span className="ms-auto text-xs text-muted-foreground tabular-nums">{a.latencyMs ? `${a.latencyMs}ms` : ""}</span>
                  <Outcome o={a.outcome} />
                  <span className="text-xs text-muted-foreground tabular-nums">{a.ts ? new Date(a.ts).toLocaleTimeString() : ""}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
