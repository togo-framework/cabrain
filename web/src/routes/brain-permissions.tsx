import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  KeyRound, Plus, Copy, Check, Trash2, Shield, ChevronRight, ChevronDown, X,
} from "lucide-react";
import { brainApi, type Grant, type Token } from "../lib/brain";

function CopyButton({ value }: { value: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      onClick={async () => {
        try { await navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* ignore */ }
      }}
      className="inline-flex items-center gap-1 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
    >
      {done ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
      {done ? "Copied" : "Copy"}
    </button>
  );
}

/** Per-brain grants editor for one agent. Toggling read/write upserts via /grant;
 * the trash icon revokes the grant for that brain. */
function GrantsEditor({ agentId, grants }: { agentId: string; grants: Grant[] }) {
  const qc = useQueryClient();
  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces });
  const [addNs, setAddNs] = useState("");

  const invalidate = () => qc.invalidateQueries({ queryKey: ["brain", "tokens"] });
  const upsert = useMutation({
    mutationFn: (v: { namespace: string; canRead: boolean; canWrite: boolean }) =>
      brainApi.grant({ agentId, ...v }),
    onSuccess: invalidate,
  });
  const revoke = useMutation({
    mutationFn: (namespace: string) => brainApi.revokeGrant({ agentId, namespace }),
    onSuccess: invalidate,
  });

  const brains = namespaces.data?.brains ?? [];
  const granted = new Set(grants.map((g) => g.namespace));
  const available = brains.filter((b) => !granted.has(b.namespace));

  return (
    <div className="space-y-2 bg-muted/30 px-4 py-3">
      <div className="text-xs font-medium text-muted-foreground">Per-brain access for {agentId}</div>
      {grants.length === 0 && <div className="text-xs text-muted-foreground">No grants yet — this agent can only reach brains an admin token allows.</div>}
      {grants.map((g) => (
        <div key={g.namespace} className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 text-sm">
          <span className="font-medium text-foreground">{g.namespace}</span>
          <label className="ms-auto inline-flex items-center gap-1 text-xs text-muted-foreground">
            <input
              type="checkbox" checked={g.canRead} disabled={upsert.isPending}
              onChange={(e) => upsert.mutate({ namespace: g.namespace, canRead: e.target.checked, canWrite: g.canWrite })}
            />
            read
          </label>
          <label className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <input
              type="checkbox" checked={g.canWrite} disabled={upsert.isPending}
              onChange={(e) => upsert.mutate({ namespace: g.namespace, canRead: g.canRead, canWrite: e.target.checked })}
            />
            write
          </label>
          <button
            onClick={() => revoke.mutate(g.namespace)} disabled={revoke.isPending}
            title="Revoke grant"
            className="rounded-md p-1 text-muted-foreground transition hover:bg-rose-500/10 hover:text-rose-500 disabled:opacity-50"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      {available.length > 0 && (
        <div className="flex items-center gap-2 pt-1">
          <select
            value={addNs} onChange={(e) => setAddNs(e.target.value)}
            className="rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs outline-none focus:border-primary"
          >
            <option value="">Add a brain…</option>
            {available.map((b) => <option key={b.namespace} value={b.namespace}>{b.namespace}</option>)}
          </select>
          <button
            onClick={() => { if (addNs) { upsert.mutate({ namespace: addNs, canRead: true, canWrite: false }); setAddNs(""); } }}
            disabled={!addNs || upsert.isPending}
            className="inline-flex items-center gap-1 rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted disabled:opacity-50"
          >
            <Plus className="h-3.5 w-3.5" /> Grant read
          </button>
        </div>
      )}
    </div>
  );
}

function TokenRow({ t }: { t: Token }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const revoke = useMutation({
    mutationFn: () => brainApi.revokeToken({ token: t.token }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["brain", "tokens"] }),
  });

  return (
    <div className={t.revoked ? "opacity-50" : ""}>
      <div className="flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
        <button onClick={() => setOpen((o) => !o)} className="text-muted-foreground hover:text-foreground">
          {open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        </button>
        <span className="font-medium text-foreground">{t.agentId}</span>
        {t.label && <span className="text-xs text-muted-foreground">{t.label}</span>}
        {t.isAdmin && (
          <span className="inline-flex items-center gap-1 rounded-full bg-violet-500/15 px-2 py-0.5 text-xs font-medium text-violet-500">
            <Shield className="h-3 w-3" /> admin
          </span>
        )}
        {t.revoked && <span className="rounded-full bg-rose-500/15 px-2 py-0.5 text-xs font-medium text-rose-500">revoked</span>}
        <span className="ms-auto text-xs text-muted-foreground">
          {t.grants?.length ?? 0} grant{(t.grants?.length ?? 0) === 1 ? "" : "s"} · last used {t.lastUsedAt ? new Date(t.lastUsedAt).toLocaleString() : "never"}
        </span>
        {!t.revoked && (
          <button
            onClick={() => revoke.mutate()} disabled={revoke.isPending}
            className="inline-flex items-center gap-1 rounded-lg border border-rose-500/40 px-2.5 py-1.5 text-xs font-medium text-rose-500 transition hover:bg-rose-500/10 disabled:opacity-50"
          >
            <Trash2 className="h-3.5 w-3.5" /> Revoke
          </button>
        )}
      </div>
      {open && <GrantsEditor agentId={t.agentId} grants={t.grants ?? []} />}
    </div>
  );
}

function CreateTokenForm() {
  const qc = useQueryClient();
  const [agentId, setAgentId] = useState("");
  const [label, setLabel] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [created, setCreated] = useState<Token | null>(null);

  const create = useMutation({
    mutationFn: () => brainApi.createToken({ agentId: agentId.trim(), label: label.trim(), isAdmin }),
    onSuccess: (t) => {
      if (t.error) return;
      setCreated(t);
      setAgentId(""); setLabel(""); setIsAdmin(false);
      qc.invalidateQueries({ queryKey: ["brain", "tokens"] });
    },
  });

  return (
    <div className="rounded-xl border border-border bg-card">
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <Plus className="h-4 w-4 text-primary" />
        <span className="text-sm font-medium">Create token</span>
      </div>
      <div className="space-y-3 p-4">
        <div className="flex flex-col gap-2 sm:flex-row">
          <input
            value={agentId} onChange={(e) => setAgentId(e.target.value)} placeholder="agent id (e.g. alice)"
            className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary sm:max-w-[220px]"
          />
          <input
            value={label} onChange={(e) => setLabel(e.target.value)} placeholder="label (e.g. laptop MCP)"
            className="w-full flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
          />
          <label className="inline-flex items-center gap-2 rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground">
            <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin(e.target.checked)} /> admin
          </label>
          <button
            onClick={() => create.mutate()} disabled={!agentId.trim() || create.isPending}
            className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
          >
            {create.isPending ? "Creating…" : "Create"}
          </button>
        </div>
        {create.data?.error && (
          <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
            {create.data.error.code}: {create.data.error.message}
          </div>
        )}
        {created && (
          <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3">
            <div className="mb-1 text-xs font-medium text-emerald-500">
              Token for {created.agentId} — shown once. Copy it now.
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <code className="flex-1 break-all rounded-md bg-background px-2 py-1.5 font-mono text-xs text-foreground">{created.token}</code>
              <CopyButton value={created.token} />
              <button onClick={() => setCreated(null)} className="rounded-md p-1 text-muted-foreground hover:text-foreground"><X className="h-4 w-4" /></button>
            </div>
            <div className="mt-2 text-xs text-muted-foreground">
              The holder sets <code>CABRAIN_TOKEN</code> in their MCP config to act as <strong>{created.agentId}</strong>.
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export function BrainPermissions() {
  const tokens = useQuery({ queryKey: ["brain", "tokens"], queryFn: brainApi.tokens, refetchInterval: 20_000 });
  const list = tokens.data?.tokens ?? [];

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Permissions</h1>
        <p className="text-sm text-muted-foreground">
          Access tokens + per-brain grants. This console is trusted-admin; a token's holder sets
          {" "}<code>CABRAIN_TOKEN</code> in their MCP config to act as that agent.
        </p>
      </div>

      <CreateTokenForm />

      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <KeyRound className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Tokens</span>
          {list.length > 0 && <span className="text-xs text-muted-foreground">{list.length}</span>}
        </div>
        {tokens.isLoading ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Loading tokens…</div>
        ) : list.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">No tokens yet. Create one above.</div>
        ) : (
          <div className="divide-y divide-border">
            {list.map((t) => <TokenRow key={t.token} t={t} />)}
          </div>
        )}
      </div>
    </div>
  );
}
