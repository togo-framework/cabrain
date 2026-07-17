import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Rocket, Copy, Check } from "lucide-react";
import { brainApi, type SessionResult } from "../lib/brain";

/** Launch-session modal — mints a scoped token + Claude Code MCP config for this
 * brain (read-only or read+write) and shows the ready-to-paste .mcp.json snippet.
 * The raw token is shown once; copy it now. Shared by the Brains hub cards and the
 * brain workspace Sessions tab. */
export function LaunchSessionModal({ namespace, onClose }: { namespace: string; onClose: () => void }) {
  const [write, setWrite] = useState(false);
  const [copied, setCopied] = useState(false);
  const [res, setRes] = useState<SessionResult | null>(null);
  const launch = useMutation({
    mutationFn: () => brainApi.launchSession({ namespace, write, label: `console session for ${namespace}` }),
    onSuccess: (r) => { if (!r.error) setRes(r); },
  });
  const snippet = res ? JSON.stringify(res.mcpConfig, null, 2) : "";
  const copy = () => {
    navigator.clipboard?.writeText(snippet).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div className="w-full max-w-lg rounded-xl border border-border bg-card p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-3 flex items-center gap-2">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary/15 text-primary"><Rocket className="h-5 w-5" /></span>
          <div>
            <div className="font-semibold text-foreground">Launch session</div>
            <div className="text-xs text-muted-foreground">Start a Claude Code session bound to <code>{namespace}</code>.</div>
          </div>
        </div>

        {!res ? (
          <>
            <label className="mb-3 flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-sm">
              <input type="checkbox" checked={write} onChange={(e) => setWrite(e.target.checked)} className="h-4 w-4 accent-primary" />
              <span className="text-foreground">Allow writes <span className="text-muted-foreground">(retain into this brain)</span></span>
            </label>
            {launch.data?.error && (
              <div className="mb-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
                {launch.data.error.code}: {launch.data.error.message}
              </div>
            )}
            <div className="flex justify-end gap-2">
              <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground hover:bg-muted">Cancel</button>
              <button
                onClick={() => launch.mutate()}
                disabled={launch.isPending}
                className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition hover:opacity-90 disabled:opacity-40"
              >
                <Rocket className="h-4 w-4" /> {launch.isPending ? "Minting…" : "Mint session token"}
              </button>
            </div>
          </>
        ) : (
          <>
            <div className="mb-2 flex items-center gap-2 text-xs">
              <span className="rounded-full bg-primary/15 px-2 py-0.5 font-medium text-primary">{res.namespace}</span>
              <span className={`rounded-full px-2 py-0.5 font-medium ${res.write ? "bg-emerald-500/15 text-emerald-500" : "bg-muted text-muted-foreground"}`}>
                {res.write ? "read + write" : "read-only"}
              </span>
              <span className="text-muted-foreground">agent {res.agentId}</span>
            </div>
            <p className="mb-2 text-xs text-muted-foreground">
              Paste into <code>.mcp.json</code>, then start Claude Code. The token is shown once — copy it now.
            </p>
            <div className="relative">
              <pre className="max-h-56 overflow-auto rounded-lg border border-border bg-background p-3 text-xs text-foreground"><code>{snippet}</code></pre>
              <button
                onClick={copy}
                className="absolute right-2 top-2 inline-flex items-center gap-1 rounded-md border border-border bg-card px-2 py-1 text-xs text-foreground hover:bg-muted"
              >
                {copied ? <><Check className="h-3.5 w-3.5 text-emerald-500" /> Copied</> : <><Copy className="h-3.5 w-3.5" /> Copy</>}
              </button>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">{res.howto}</p>
            <div className="mt-3 flex justify-end">
              <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-muted-foreground hover:bg-muted">Done</button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
