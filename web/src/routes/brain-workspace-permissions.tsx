import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Shield, X, Link as LinkIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import { brainApi, type Grant, type Token } from "../lib/brain";

/** Per-agent read/write row for THIS brain. Toggling read/write upserts the grant
 * via /grant; the trash icon revokes this agent's access to this brain. */
function AgentGrantRow({ agentId, isAdmin, namespace, grant }: {
  agentId: string; isAdmin: boolean; namespace: string; grant?: Grant;
}) {
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["brain", "tokens"] });
  const upsert = useMutation({
    mutationFn: (v: { canRead: boolean; canWrite: boolean }) => brainApi.grant({ agentId, namespace, ...v }),
    onSuccess: invalidate,
  });
  const revoke = useMutation({
    mutationFn: () => brainApi.revokeGrant({ agentId, namespace }),
    onSuccess: invalidate,
  });

  const canRead = grant?.canRead ?? false;
  const canWrite = grant?.canWrite ?? false;

  return (
    <div className="flex flex-wrap items-center gap-2 px-4 py-3 text-sm">
      <span className="font-medium text-foreground">{agentId}</span>
      {isAdmin && (
        <span className="inline-flex items-center gap-1 rounded-full bg-violet-500/15 px-2 py-0.5 text-xs font-medium text-violet-500">
          <Shield className="h-3 w-3" /> admin
        </span>
      )}
      {isAdmin && <span className="text-xs text-muted-foreground">full access via admin token</span>}
      <label className="ms-auto inline-flex items-center gap-1 text-xs text-muted-foreground">
        <input
          type="checkbox" checked={canRead} disabled={upsert.isPending}
          onChange={(e) => upsert.mutate({ canRead: e.target.checked, canWrite })}
        />
        read
      </label>
      <label className="inline-flex items-center gap-1 text-xs text-muted-foreground">
        <input
          type="checkbox" checked={canWrite} disabled={upsert.isPending}
          onChange={(e) => upsert.mutate({ canRead: canRead || e.target.checked, canWrite: e.target.checked })}
        />
        write
      </label>
      {grant ? (
        <button
          onClick={() => revoke.mutate()} disabled={revoke.isPending}
          title="Revoke this brain's grant"
          className="rounded-md p-1 text-muted-foreground transition hover:bg-rose-500/10 hover:text-rose-500 disabled:opacity-50"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      ) : (
        <span className="w-6" />
      )}
    </div>
  );
}

/** Per-brain permissions — who can read/write THIS brain. Reuses the token +
 * grant API filtered to a single namespace. Token creation itself lives under
 * Admin (cross-brain). */
export function BrainWorkspacePermissions({ namespace }: { namespace: string }) {
  const tokens = useQuery({ queryKey: ["brain", "tokens"], queryFn: brainApi.tokens, refetchInterval: 20_000 });
  const list = (tokens.data?.tokens ?? []).filter((t: Token) => !t.revoked);

  const rows = useMemo(() => {
    // Dedupe by agentId; carry this brain's grant + admin flag.
    const m = new Map<string, { agentId: string; isAdmin: boolean; grant?: Grant }>();
    for (const t of list) {
      if (!t.agentId) continue;
      const grant = (t.grants ?? []).find((g) => g.namespace === namespace);
      const cur = m.get(t.agentId);
      m.set(t.agentId, {
        agentId: t.agentId,
        isAdmin: (cur?.isAdmin ?? false) || t.isAdmin,
        grant: cur?.grant ?? grant,
      });
    }
    return [...m.values()].sort((a, b) => a.agentId.localeCompare(b.agentId));
  }, [list, namespace]);

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Permissions</h1>
        <p className="text-sm text-muted-foreground">
          Who can read or write <span className="font-medium text-foreground">{namespace}</span>. Toggle a grant per agent;
          admin tokens always have full access. Create tokens under{" "}
          <Link to="/admin/tokens" className="inline-flex items-center gap-1 font-medium text-primary hover:underline">
            <LinkIcon className="h-3 w-3" /> Admin · Tokens
          </Link>.
        </p>
      </div>

      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <KeyRound className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Agents</span>
          {rows.length > 0 && <span className="text-xs text-muted-foreground">{rows.length}</span>}
        </div>
        {tokens.isLoading ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Loading agents…</div>
        ) : rows.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">
            No agents yet. Create a token under Admin · Tokens, then grant it access here.
          </div>
        ) : (
          <div className="divide-y divide-border">
            {rows.map((r) => (
              <AgentGrantRow key={r.agentId} agentId={r.agentId} isAdmin={r.isAdmin} namespace={namespace} grant={r.grant} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
