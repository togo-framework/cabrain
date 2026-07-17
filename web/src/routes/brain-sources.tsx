import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Plug, Plus, Trash2, RefreshCw, Check, Copy, X, Webhook, FileText,
  Globe, Github, Database, Link2, AlertTriangle, CircleDot,
} from "lucide-react";
import { brainApi, type Datasource } from "../lib/brain";
import { SynapseField, NeuralGlyph } from "../components/neural";

// The connector kinds the picker offers. `fields` drive the per-kind form; a
// value maps 1:1 into the created source's `config`. `soon` kinds are shown as
// disabled chips (backend connectors not built yet).
type Field = { key: string; label: string; placeholder: string; textarea?: boolean; optional?: boolean };
type KindSpec = {
  kind: string; label: string; icon: any; blurb: string;
  fields: Field[]; soon?: boolean;
};

const KINDS: KindSpec[] = [
  {
    kind: "webhook", label: "Webhook", icon: Webhook,
    blurb: "Push documents in from anywhere — we mint a URL + secret to POST to.",
    fields: [],
  },
  {
    kind: "text", label: "Text / Markdown", icon: FileText,
    blurb: "Paste text or markdown to ingest directly as memories.",
    fields: [{ key: "content", label: "Content", placeholder: "# Notes\nPaste markdown or plain text…", textarea: true }],
  },
  {
    kind: "crawler", label: "Website", icon: Globe,
    blurb: "Crawl a URL and ingest its pages.",
    fields: [
      { key: "url", label: "Start URL", placeholder: "https://docs.example.com" },
      { key: "maxPages", label: "Max pages", placeholder: "50", optional: true },
    ],
  },
  {
    kind: "github", label: "GitHub", icon: Github,
    blurb: "Ingest files from a repository path.",
    fields: [
      { key: "repo", label: "Repo", placeholder: "owner/name" },
      { key: "branch", label: "Branch", placeholder: "main", optional: true },
      { key: "path", label: "Path", placeholder: "docs/", optional: true },
    ],
  },
  {
    kind: "sql", label: "SQL", icon: Database,
    blurb: "Run a query against a database and ingest the rows.",
    fields: [
      { key: "dsn", label: "Connection string (DSN)", placeholder: "postgres://user:pass@host/db" },
      { key: "query", label: "Query", placeholder: "SELECT id, body FROM articles", textarea: true },
    ],
  },
  // Coming soon — disabled in the picker.
  { kind: "pdf", label: "PDF", icon: FileText, blurb: "Ingest PDF documents.", fields: [], soon: true },
  { kind: "image", label: "Image", icon: CircleDot, blurb: "Ingest images with captions.", fields: [], soon: true },
  { kind: "mcp", label: "MCP server", icon: Plug, blurb: "Pull context from an MCP server.", fields: [], soon: true },
];

const KIND_BY: Record<string, KindSpec> = Object.fromEntries(KINDS.map((k) => [k.kind, k]));

function iconFor(kind: string) { return KIND_BY[kind]?.icon ?? Plug; }

function StatusPill({ status }: { status: string }) {
  const map: Record<string, { cls: string; label: string; pulse?: boolean }> = {
    idle: { cls: "bg-muted text-muted-foreground", label: "idle" },
    syncing: { cls: "bg-amber-500/15 text-amber-500", label: "syncing", pulse: true },
    ok: { cls: "bg-emerald-500/15 text-emerald-500", label: "ok" },
    error: { cls: "bg-rose-500/15 text-rose-500", label: "error" },
  };
  const s = map[status] ?? map.idle;
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${s.cls}`}>
      <span className={`h-1.5 w-1.5 rounded-full bg-current ${s.pulse ? "cb-spark" : ""}`} /> {s.label}
    </span>
  );
}

function KindBadge({ kind }: { kind: string }) {
  const Icon = iconFor(kind);
  const spec = KIND_BY[kind];
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary">
      <Icon className="h-3 w-3" /> {spec?.label ?? kind}
    </span>
  );
}

function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      onClick={async () => { try { await navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* ignore */ } }}
      className="inline-flex items-center gap-1 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted"
    >
      {done ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
      {done ? "Copied" : label}
    </button>
  );
}

/** One configured source. Shows kind, status, doc-count, last-sync and the
 * sync/delete actions. Webhook sources reveal their push URL + secret inline. */
function SourceRow({ s }: { s: Datasource }) {
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [showHook, setShowHook] = useState(false);

  const sync = useMutation({
    mutationFn: () => brainApi.syncDatasource({ id: s.id }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["brain", "datasources"] }),
  });
  const del = useMutation({
    mutationFn: () => brainApi.deleteDatasource({ id: s.id }),
    onSuccess: (res) => {
      if (res.error) return;
      qc.invalidateQueries({ queryKey: ["brain", "datasources"] });
    },
  });

  const isWebhook = s.kind === "webhook";
  const secret = typeof s.config?.secret === "string" ? (s.config.secret as string) : "";

  return (
    <div className="px-4 py-3.5 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          {(() => { const Icon = iconFor(s.kind); return <Icon className="h-4 w-4" />; })()}
        </span>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium text-foreground">{s.name}</span>
            <KindBadge kind={s.kind} />
          </div>
          <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <StatusPill status={s.status} />
            <span className="tabular-nums">{s.docCount.toLocaleString()} docs</span>
            <span>· synced {s.lastSyncAt ? new Date(s.lastSyncAt).toLocaleString() : "never"}</span>
            {isWebhook && (
              <button onClick={() => setShowHook((v) => !v)} className="inline-flex items-center gap-1 text-primary hover:underline">
                <Link2 className="h-3 w-3" /> {showHook ? "hide" : "push URL"}
              </button>
            )}
          </div>
        </div>

        <div className="ms-auto flex items-center gap-2">
          {!isWebhook && (
            <button
              onClick={() => sync.mutate()}
              disabled={sync.isPending || s.status === "syncing"}
              className="inline-flex items-center gap-1 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground transition hover:bg-muted disabled:opacity-50"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${sync.isPending ? "animate-spin" : ""}`} /> {sync.isPending ? "Syncing…" : "Sync"}
            </button>
          )}
          {confirmDelete ? (
            <span className="inline-flex items-center gap-1">
              <button
                onClick={() => del.mutate()}
                disabled={del.isPending}
                className="inline-flex items-center gap-1 rounded-lg bg-rose-500 px-2.5 py-1.5 text-xs font-medium text-white transition hover:bg-rose-600 disabled:opacity-50"
              >
                <Trash2 className="h-3.5 w-3.5" /> {del.isPending ? "…" : "Confirm"}
              </button>
              <button onClick={() => setConfirmDelete(false)} className="rounded-md p-1 text-muted-foreground hover:text-foreground"><X className="h-3.5 w-3.5" /></button>
            </span>
          ) : (
            <button onClick={() => setConfirmDelete(true)} title="Delete source" className="rounded-md p-1.5 text-muted-foreground transition hover:bg-rose-500/10 hover:text-rose-500">
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>

      {/* Sync result / errors as styled caveats, not raw errors. */}
      {sync.data && !sync.data.error && (
        <div className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-1.5 text-xs text-emerald-500">
          <Check className="h-3.5 w-3.5" /> Ingested {sync.data.ingested.toLocaleString()} docs · {sync.data.status}
        </div>
      )}
      {(sync.data?.error || s.lastError) && (
        <div className="mt-2 inline-flex items-center gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs text-amber-500">
          <AlertTriangle className="h-3.5 w-3.5" /> {sync.data?.error ?? s.lastError}
        </div>
      )}
      {del.data?.error && (
        <div className="mt-2 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-1.5 text-xs text-rose-400">
          {del.data.error.code}: {del.data.error.message}
        </div>
      )}

      {/* Webhook push endpoint — the URL + secret to hand out. */}
      {isWebhook && showHook && (
        <div className="mt-2 space-y-2 rounded-lg border border-border bg-background/60 p-3">
          <div className="text-xs text-muted-foreground">POST documents here to push into this brain:</div>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-2 py-1 font-mono text-xs text-foreground">{brainApi.ingestUrl(s.id)}</code>
            <CopyButton value={brainApi.ingestUrl(s.id)} label="URL" />
          </div>
          {secret && (
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-md bg-muted px-2 py-1 font-mono text-xs text-foreground">X-Webhook-Secret: {secret}</code>
              <CopyButton value={secret} label="Secret" />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** Add-source modal — a kind picker whose form fields change per kind. On a
 * webhook create, the push URL + secret are shown to copy before closing. */
function AddSourceModal({ namespace, onClose }: { namespace: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [kind, setKind] = useState<string>("webhook");
  const [name, setName] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [created, setCreated] = useState<Datasource | null>(null);

  const spec = KIND_BY[kind];

  const create = useMutation({
    mutationFn: () => {
      const config: Record<string, unknown> = {};
      for (const f of spec.fields) {
        const v = values[f.key];
        if (v !== undefined && v !== "") config[f.key] = v;
      }
      return brainApi.createDatasource({ namespace, kind, name: name.trim(), config });
    },
    onSuccess: (res) => {
      if (res.error) return;
      qc.invalidateQueries({ queryKey: ["brain", "datasources"] });
      if (res.kind === "webhook") setCreated(res); // reveal the push URL + secret
      else onClose();
    },
  });

  const requiredMissing = spec.fields.some((f) => !f.optional && !values[f.key]?.trim());
  const secret = created && typeof created.config?.secret === "string" ? (created.config.secret as string) : "";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm" onClick={onClose}>
      <div className="w-full max-w-lg rounded-xl border border-border bg-card p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        {created ? (
          // Post-create: show the webhook push endpoint to copy.
          <div>
            <div className="mb-3 flex items-center gap-2">
              <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-500"><Check className="h-5 w-5" /></span>
              <div>
                <div className="font-semibold text-foreground">Webhook source ready</div>
                <div className="text-xs text-muted-foreground">Push documents to this URL with the secret header.</div>
              </div>
            </div>
            <div className="space-y-2">
              <div>
                <label className="mb-1 block text-xs font-medium text-muted-foreground">Push URL</label>
                <div className="flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-md bg-background px-2 py-1.5 font-mono text-xs text-foreground">{brainApi.ingestUrl(created.id)}</code>
                  <CopyButton value={brainApi.ingestUrl(created.id)} label="Copy" />
                </div>
              </div>
              {secret && (
                <div>
                  <label className="mb-1 block text-xs font-medium text-muted-foreground">Header — X-Webhook-Secret</label>
                  <div className="flex items-center gap-2">
                    <code className="min-w-0 flex-1 truncate rounded-md bg-background px-2 py-1.5 font-mono text-xs text-foreground">{secret}</code>
                    <CopyButton value={secret} label="Copy" />
                  </div>
                </div>
              )}
            </div>
            <div className="mt-4 flex justify-end">
              <button onClick={onClose} className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground">Done</button>
            </div>
          </div>
        ) : (
          <>
            <div className="mb-3 flex items-center gap-2">
              <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary"><Plus className="h-5 w-5" /></span>
              <div>
                <div className="font-semibold text-foreground">Add a source</div>
                <div className="text-xs text-muted-foreground">Connect knowledge into <code>{namespace}</code>.</div>
              </div>
            </div>

            {/* Kind picker */}
            <div className="mb-3 grid grid-cols-3 gap-2 sm:grid-cols-4">
              {KINDS.map((k) => {
                const Icon = k.icon;
                const active = k.kind === kind;
                return (
                  <button
                    key={k.kind}
                    disabled={k.soon}
                    onClick={() => { setKind(k.kind); setValues({}); }}
                    title={k.soon ? "Coming soon" : k.blurb}
                    className={`relative flex flex-col items-center gap-1 rounded-lg border px-2 py-2.5 text-xs transition ${
                      active ? "border-primary bg-primary/10 text-primary" : "border-border text-muted-foreground hover:bg-muted"
                    } ${k.soon ? "cursor-not-allowed opacity-50" : ""}`}
                  >
                    <Icon className="h-4 w-4" />
                    <span className="truncate">{k.label}</span>
                    {k.soon && <span className="absolute right-1 top-1 rounded bg-muted px-1 text-[9px] text-muted-foreground">soon</span>}
                  </button>
                );
              })}
            </div>

            <p className="mb-3 text-xs text-muted-foreground">{spec.blurb}</p>

            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-muted-foreground">Name</label>
                <input
                  autoFocus value={name} onChange={(e) => setName(e.target.value)}
                  placeholder={`e.g. ${spec.label} source`}
                  className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
                />
              </div>
              {spec.fields.map((f) => (
                <div key={f.key}>
                  <label className="mb-1 block text-xs font-medium text-muted-foreground">
                    {f.label}{f.optional && <span className="ms-1 opacity-60">(optional)</span>}
                  </label>
                  {f.textarea ? (
                    <textarea
                      value={values[f.key] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                      rows={4} placeholder={f.placeholder}
                      className="w-full resize-y rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs outline-none focus:border-primary"
                    />
                  ) : (
                    <input
                      value={values[f.key] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                      placeholder={f.placeholder}
                      className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:border-primary"
                    />
                  )}
                </div>
              ))}
              {kind === "webhook" && (
                <div className="rounded-lg border border-border bg-background/60 px-3 py-2 text-xs text-muted-foreground">
                  A push URL and secret are generated on create — you'll copy them next.
                </div>
              )}
              {create.data?.error && (
                <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
                  {create.data.error.code}: {create.data.error.message}
                </div>
              )}
            </div>

            <div className="mt-4 flex justify-end gap-2">
              <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground hover:bg-muted">Cancel</button>
              <button
                onClick={() => create.mutate()}
                disabled={!name.trim() || requiredMissing || create.isPending}
                className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-60"
              >
                {create.isPending ? "Connecting…" : "Add source"}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

/** Sources — the data-source connectors for a brain. Lists configured sources
 * and offers an Add-source flow with a per-kind form. Scoped to the workspace's
 * brain via the `namespace` prop. */
export function BrainSources({ namespace }: { namespace: string }) {
  const [adding, setAdding] = useState(false);
  const sources = useQuery({
    queryKey: ["brain", "datasources", namespace],
    queryFn: () => brainApi.datasources(namespace),
    enabled: !!namespace,
    refetchInterval: 10_000,
  });
  const list = sources.data?.datasources ?? [];

  return (
    <div className="mx-auto max-w-4xl space-y-5 p-6">
      {/* Neural hero */}
      <div className="relative overflow-hidden rounded-2xl border border-border bg-gradient-to-br from-indigo-500/10 via-violet-500/5 to-teal-400/10 p-6">
        <SynapseField className="opacity-40" />
        <div className="relative flex items-start gap-3">
          <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white shadow-[0_0_22px_-4px] shadow-violet-500/60">
            <Plug className="h-5 w-5" />
          </span>
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Sources</h1>
            <p className="text-sm text-muted-foreground">
              Connect knowledge into <span className="font-medium text-foreground">{namespace}</span> — like GitHub, a website, a SQL DB, or a webhook. Each source ingests documents as memories.
            </p>
          </div>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card">
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <Plug className="h-4 w-4 text-primary" />
          <span className="text-sm font-medium">Connected sources</span>
          {list.length > 0 && <span className="text-xs text-muted-foreground">{list.length}</span>}
          <button
            onClick={() => setAdding(true)}
            className="ms-auto inline-flex items-center gap-1 rounded-lg bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground transition hover:opacity-90"
          >
            <Plus className="h-3.5 w-3.5" /> Add source
          </button>
        </div>

        {sources.isLoading ? (
          <div className="px-4 py-10 text-center text-sm text-muted-foreground">Loading sources…</div>
        ) : list.length === 0 ? (
          <div className="relative overflow-hidden px-4 py-14 text-center">
            <SynapseField className="opacity-20" />
            <div className="relative mx-auto max-w-sm">
              <span className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-xl bg-primary/15 text-primary"><NeuralGlyph className="h-6 w-6" /></span>
              <div className="text-sm font-medium text-foreground">No sources connected yet</div>
              <p className="mt-1 text-sm text-muted-foreground">
                Sources connect knowledge into this brain — like GitHub, a website, a SQL DB, or a webhook. Add one to start ingesting.
              </p>
              <button
                onClick={() => setAdding(true)}
                className="mt-4 inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition hover:opacity-90"
              >
                <Plus className="h-4 w-4" /> Add your first source
              </button>
            </div>
          </div>
        ) : (
          <div className="divide-y divide-border">
            {list.map((s) => <SourceRow key={s.id} s={s} />)}
          </div>
        )}
      </div>

      {adding && <AddSourceModal namespace={namespace} onClose={() => setAdding(false)} />}
    </div>
  );
}
