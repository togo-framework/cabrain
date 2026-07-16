import { useQuery } from "@tanstack/react-query";
import { Database } from "lucide-react";
import { brainApi } from "../lib/brain";

/** Brains = datasets/namespaces (Cognee calls them "brains"). */
export function BrainBrains() {
  const q = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces, refetchInterval: 15_000 });
  const brains = q.data?.brains ?? [];

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Brains</h1>
        <p className="text-sm text-muted-foreground">Namespaces — each an isolated scope over the one shared store.</p>
      </div>
      {brains.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center text-sm text-muted-foreground">
          {q.isLoading ? "Loading…" : "No brains yet. A namespace appears the first time you retain into it."}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {brains.map((b) => (
            <div key={b.namespace} className="rounded-xl border border-border bg-card p-4">
              <div className="mb-2 flex items-center gap-2">
                <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/15 text-primary"><Database className="h-4 w-4" /></span>
                <span className="font-medium text-foreground">{b.namespace}</span>
              </div>
              <div className="text-2xl font-semibold tabular-nums text-foreground">{b.memories.toLocaleString()}</div>
              <div className="text-xs text-muted-foreground">memories · last {b.lastAt ? new Date(b.lastAt).toLocaleDateString() : "—"}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
