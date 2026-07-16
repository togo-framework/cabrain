# CaBrain capture mode (SPEC §6)

`capture-mode.py` is a Claude Code **Stop** hook that passively accumulates
memory mass: at the end of each assistant turn it keeps the turn only if it states
a durable decision / correction / fact, redacts `<private>…</private>` spans and
secret-shaped text, and fire-and-forget POSTs it to `memory_retain`.

**It is opt-in and does nothing unless `CABRAIN_CAPTURE=1`.** It POSTs turn content
to the CaBrain app, so enable it deliberately, per workstream.

## Enable

Add to your Claude Code settings (`.claude/settings.json` for this repo, or
`~/.claude/settings.json` globally):

```jsonc
{
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command",
        "command": "python3 /home/coder/caBrain/.claude/hooks/capture-mode.py" } ] }
    ]
  },
  "env": {
    "CABRAIN_CAPTURE": "1",
    "CABRAIN_API_URL": "http://localhost:8080",
    "CABRAIN_NAMESPACE": "cabrain",
    "CABRAIN_AGENT_ID": "claude-code"
  }
}
```

| Env | Default | Meaning |
|---|---|---|
| `CABRAIN_CAPTURE` | *(off)* | must be `1` to capture anything |
| `CABRAIN_API_URL` | `http://localhost:8080` | the running CaBrain app |
| `CABRAIN_NAMESPACE` | derived from `cwd` basename | project scope (one grant per project, F5) |
| `CABRAIN_AGENT_ID` | *(empty = trusted)* | `X-Agent-Id` for grant checks |

## Guarantees

- **Best-effort:** any failure (endpoint down, embed unavailable pre-deploy, a
  non-worthy or all-private turn) exits 0 silently — never blocks the session.
- **Privacy:** `<private>…</private>` is stripped pre-POST; a turn containing a
  secret shape (private key, `password=…`, `AKIA…`) is dropped entirely.
- **Provenance:** `source_kind=claude_code`, `source_ref=<session id>`.
- Final importance + the ADD/UPDATE/NOOP dedupe are computed server-side (§4.1);
  the hook only decides *worthy vs skip*.

Until the app is deployed on `stack_stacknet` (so `retain` can embed via TEI), the
POST simply drops — the hook is inert-but-correct, exactly as designed.
