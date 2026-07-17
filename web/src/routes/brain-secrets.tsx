import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Lock, Plus, Copy, Check, Trash2, Eye, EyeOff, ShieldAlert, X,
} from "lucide-react";
import { brainApi, type SecretMeta } from "../lib/brain";

// The kinds the backend recognises (auto-capture + manual). `generic` is the default.
const KINDS = [
  "generic", "api_key", "password", "token", "env",
  "private_key", "connection_string", "credential",
] as const;

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

function KindBadge({ kind }: { kind: string }) {
  return (
    <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 font-mono text-xs text-muted-foreground">
      {kind || "generic"}
    </span>
  );
}

/** One secret row. The value is NEVER preloaded — it's fetched lazily via a
 * reveal mutation only when the user clicks Reveal. `permission_denied` (reveal
 * needs write/admin) is surfaced inline instead of crashing the row. */
function SecretRow({ s }: { s: SecretMeta }) {
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);

  const reveal = useMutation({
    mutationFn: () => brainApi.revealSecret({ namespace: s.namespace, name: s.name }),
  });
  const del = useMutation({
    mutationFn: () => brainApi.deleteSecret({ namespace: s.namespace, name: s.name }),
    onSuccess: (res) => {
      if (res.error) return;
      setConfirmDelete(false);
      qc.invalidateQueries({ queryKey: ["brain", "secrets"] });
    },
  });

  const shown = reveal.data && !reveal.data.error ? reveal.data.value : undefined;
  const revealErr = reveal.data?.error;

  return (
    <div className="px-4 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium text-foreground">{s.name}</span>
        <KindBadge kind={s.kind} />
        {shown === undefined ? (
          <code className="rounded-md bg-background px-2 py-1 font-mono text-xs text-muted-foreground">{s.hint || "•••"}</code>
        ) : (
          <code className="max-w-[280px] flex-1 break-all rounded-md bg-background px-2 py-1 font-mono text-xs text-foreground">{shown}</code>
        )}

        <span className="ms-auto text-xs text-muted-foreground">
          {s.sourceRef && <span className="me-2">src {s.sourceRef}</span>}
          {s.createdBy && <span className="me-2">by {s.createdBy}</span>}
          upd {s.updatedAt ? new Date(s.updatedAt).toLocaleString() : "—"}
        </span>

        {shown !== undefined ? (
          <>
            <CopyButton value={shown} />
            <button
              onClick={() => reveal.reset()}
              className="inline-flex items-center gap-1 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
            >
              <EyeOff className="h-3.5 w-3.5" /> Hide
            </button>
          </>
        ) : (
          <button
            onClick={() => reveal.mutate()}
            disabled={reveal.isPending}
            className="inline-flex items-center gap-1 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted disabled:opacity-50"
          >
            <Eye className="h-3.5 w-3.5" /> {reveal.isPending ? "Revealing…" : "Reveal"}
          </button>
        )}

        {confirmDelete ? (
          <span className="inline-flex items-center gap-1">
            <button
              onClick={() => del.mutate()}
              disabled={del.isPending}
              className="inline-flex items-center gap-1 rounded-lg bg-rose-500 px-2.5 py-1.5 text-xs font-medium text-white transition hover:bg-rose-600 disabled:opacity-50"
            >
              <Trash2 className="h-3.5 w-3.5" /> {del.isPending ? "Deleting…" : "Confirm"}
            </button>
            <button
              onClick={() => setConfirmDelete(false)}
              className="rounded-md p-1 text-muted-foreground hover:text-foreground"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </span>
        ) : (
          <button
            onClick={() => setConfirmDelete(true)}
            title="Delete secret"
            className="rounded-md p-1.5 text-muted-foreground transition hover:bg-rose-500/10 hover:text-rose-500"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {revealErr && (
        <div className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs text-amber-500">
          <ShieldAlert className="h-3.5 w-3.5" /> {revealErr.code}: {revealErr.message}
        </div>
      )}
      {del.data?.error && (
        <div className="mt-2 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-1.5 text-xs text-rose-400">
          {del.data.error.code}: {del.data.error.message}
        </div>
      )}
    </div>
  );
}

/** Add-secret modal — name + value + kind, POSTed to putSecret (write/admin). */
function AddSecretModal({ namespace, onClose }: { namespace: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [kind, setKind] = useState<string>("generic");

  const put = useMutation({
    mutationFn: () => brainApi.putSecret({ namespace, name: name.trim(), value, kind }),
    onSuccess: (res) => {
      if (res.error) return;
      qc.invalidateQueries({ queryKey: ["brain", "secrets"] });
      onClose();
    },
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div className="w-full max-w-md rounded-xl border border-border bg-card p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center gap-2">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary"><Plus className="h-5 w-5" /></span>
          <div>
            <div className="font-semibold text-foreground">Add secret</div>
            <div className="text-xs text-muted-foreground">Stored encrypted in <code>{namespace}</code>.</div>
          </div>
        </div>
        <div className="space-y-3">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Name</label>
            <input
              autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. OPENAI_API_KEY"
              className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Value</label>
            <textarea
              value={value} onChange={(e) => setValue(e.target.value)} rows={3} placeholder="sk-…"
              className="w-full resize-y rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs outline-none focus:border-primary"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground">Kind</label>
            <select
              value={kind} onChange={(e) => setKind(e.target.value)}
              className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
            >
              {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
            </select>
          </div>
          {put.data?.error && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
              {put.data.error.code}: {put.data.error.message}
            </div>
          )}
        </div>
        <div className="mt-4 flex justify-end gap-2">
          <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground hover:bg-muted">Cancel</button>
          <button
            onClick={() => put.mutate()}
            disabled={!name.trim() || !value || put.isPending}
            className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-60"
          >
            {put.isPending ? "Saving…" : "Save secret"}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Secrets vault — per-brain, namespace-scoped. Values are encrypted at rest and
 * are only ever fetched on an explicit Reveal (which needs write access). */
export function BrainSecrets() {
  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces });
  const brains = namespaces.data?.brains ?? [];

  // Auto-select the richest brain (most memories) once namespaces load.
  const richest = useMemo(
    () => [...brains].sort((a, b) => b.memories - a.memories)[0]?.namespace ?? "",
    [brains],
  );
  const [ns, setNs] = useState("");
  useEffect(() => { if (!ns && richest) setNs(richest); }, [richest, ns]);

  const [adding, setAdding] = useState(false);

  const secrets = useQuery({
    queryKey: ["brain", "secrets", ns],
    queryFn: () => brainApi.secrets(ns),
    enabled: !!ns,
  });
  const list = secrets.data?.secrets ?? [];

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Secrets</h1>
          <p className="text-sm text-muted-foreground">
            Secrets are encrypted at rest and auto-captured from retained content; revealing requires write access.
          </p>
        </div>
        <select
          value={ns}
          onChange={(e) => setNs(e.target.value)}
          className="rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
        >
          {brains.length === 0 && <option value="">No brains</option>}
          {brains.map((b) => (
            <option key={b.namespace} value={b.namespace}>
              {b.namespace} ({b.memories.toLocaleString()})
            </option>
          ))}
        </select>
      </div>

      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <Lock className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Vault</span>
          {list.length > 0 && <span className="text-xs text-muted-foreground">{list.length}</span>}
          <button
            onClick={() => setAdding(true)}
            disabled={!ns}
            className="ms-auto inline-flex items-center gap-1 rounded-lg bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground transition disabled:cursor-not-allowed disabled:opacity-60"
          >
            <Plus className="h-3.5 w-3.5" /> Add secret
          </button>
        </div>

        {!ns ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Select a brain to view its vault.</div>
        ) : secrets.isLoading ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Loading secrets…</div>
        ) : secrets.data?.secrets === undefined ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Couldn't load secrets.</div>
        ) : list.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">
            No secrets in <code>{ns}</code> yet. Add one, or they'll appear as retained content is redacted.
          </div>
        ) : (
          <div className="divide-y divide-border">
            {list.map((s) => <SecretRow key={s.name} s={s} />)}
          </div>
        )}
      </div>

      {adding && ns && <AddSecretModal namespace={ns} onClose={() => setAdding(false)} />}
    </div>
  );
}
