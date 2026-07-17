import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Rocket } from "lucide-react";
import { brainApi, type ActivityItem } from "../lib/brain";
import { LaunchSessionModal } from "../components/launch-session-modal";

const FILTERS = ["all", "searches", "writes", "errors"] as const;

function Outcome({ o }: { o: string }) {
  const m: Record<string, string> = {
    hit: "bg-emerald-500/15 text-emerald-500", empty: "bg-amber-500/15 text-amber-500",
    error: "bg-rose-500/15 text-rose-500", running: "bg-sky-500/15 text-sky-500",
  };
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${m[o] ?? "bg-muted text-muted-foreground"}`}>{o}</span>;
}

/** Operation feed. When `namespace` is supplied (brain workspace) it filters to
 * that brain and surfaces a "Launch session bound to this brain" action. */
export function BrainSessions({ namespace }: { namespace?: string } = {}) {
  const scoped = !!namespace;
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("all");
  const [launching, setLaunching] = useState(false);
  const q = useQuery({ queryKey: ["brain", "activity", "full"], queryFn: () => brainApi.activity(200), refetchInterval: 6_000 });
  const items = q.data?.items ?? [];
  const rows = useMemo(() => {
    const base = scoped ? items.filter((i) => i.namespace === namespace) : items;
    switch (filter) {
      case "searches": return base.filter((i) => i.op === "recall");
      case "writes": return base.filter((i) => i.op === "retain" || i.op === "reconsolidate");
      case "errors": return base.filter((i) => i.outcome === "error");
      default: return base;
    }
  }, [items, filter, scoped, namespace]);

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Sessions</h1>
          <p className="text-sm text-muted-foreground">
            {scoped
              ? <>Operations against <span className="font-medium text-foreground">{namespace}</span> — and launch a Claude Code session bound to it.</>
              : "Live, evidence-oriented feed of every operation against memory."}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {scoped && (
            <button
              onClick={() => setLaunching(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-primary/40 px-2.5 py-1.5 text-xs font-medium text-primary transition hover:bg-primary/10"
            >
              <Rocket className="h-3.5 w-3.5" /> Launch session
            </button>
          )}
          <div className="flex gap-1">
            {FILTERS.map((f) => (
              <button key={f} onClick={() => setFilter(f)}
                className={`rounded-md px-2 py-1 text-xs capitalize ${filter === f ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted"}`}>{f}</button>
            ))}
          </div>
        </div>
      </div>
      {launching && namespace && <LaunchSessionModal namespace={namespace} onClose={() => setLaunching(false)} />}
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        {rows.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">{q.isLoading ? "Loading…" : "No activity yet."}</div>
        ) : (
          <div className="divide-y divide-border">
            {rows.map((a: ActivityItem) => (
              <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
                <span className="font-medium text-foreground">{a.op}</span>
                <span className="text-muted-foreground">{a.namespace || "—"}</span>
                <span className="text-xs text-muted-foreground">{a.agentId}</span>
                <span className="ms-auto text-xs text-muted-foreground tabular-nums">{a.latencyMs ? `${a.latencyMs}ms` : ""}</span>
                <Outcome o={a.outcome} />
                <span className="text-xs text-muted-foreground tabular-nums">{a.ts ? new Date(a.ts).toLocaleTimeString() : ""}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
