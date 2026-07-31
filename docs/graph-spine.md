# The venture spine: one query, and how it stays fresh

The typed graph holds the studio's spine

```
portfolio -> venture -> repo -> member -> activity -> feed
                     -> goal / okr -> roadmap -> code_module -> doc
```

Two things were missing once the edges existed: a way to ASK for a whole
neighbourhood without hand-writing a recursive CTE, and something to keep the
edges from rotting. This document covers both.

---

## 1. `POST /api/brain/graph/spine`

Everything about one venture (or portfolio, repo, person) in one call, grouped by
role, with a **true total next to a capped sample**.

`GET /api/brain/graph/spine?namespace=&entity=&depth=&perGroup=&window=&roles=`
takes the same parameters, so it is curl-able. MCP tool: `graph_spine`.

### Request

| field       | type       | default                  | meaning |
|-------------|------------|--------------------------|---------|
| `namespace` | string     | —                        | brain (required) |
| `entity`    | string     | —                        | entity NAME (exact, then case-insensitive) or id (required) |
| `depth`     | int        | 2 (max 4)                | hops. 2 = venture→repo→code/doc/activity. Use 3 for a portfolio. |
| `hubs`      | string[]   | `repo,venture,feed`      | the only entity types expanded PAST the first hop |
| `roles`     | string[]   | all                      | restrict which groups come back |
| `perGroup`  | int        | 10 (max 200)             | cap per group. **Never** changes `total`. |
| `window`    | string     | —                        | relative bound: `24h`, `7d`, `2w`, `3m` |
| `since`/`until` | RFC3339 | —                       | explicit bound (overrides `window` for `since`) |
| `timeRoles` | string[]   | `activity,meeting,channel,feed` | the roles the window applies to; `["*"]` = all |

Two design choices worth knowing:

* **`hubs` is what stops the walk exploding.** Past hop 1 only structural hub
  types are expanded. `portfolio` is deliberately NOT a default hub: expanding it
  mid-walk lets a venture climb to its portfolio and come back down with its
  SIBLINGS' repos, presented as its own. Pass it explicitly if you want that.
* **The window only binds event-bearing roles.** "SADA last week" must still list
  SADA's repos — they are its repos, not repos created last week. The response
  echoes `window.appliedTo` so the semantics are never implicit.

### Response

```jsonc
{
  "root":   { "id": "...", "name": "SADA", "type": "venture" },
  "depth":  2,
  "hubs":   ["repo","venture","feed"],
  "window": { "since": "...", "appliedTo": ["activity","meeting","channel","feed"] },
  "groups": [
    { "role": "repo", "total": 4, "shown": 2, "capped": true,
      "items": [ { "id":"...", "name":"x-arcom/aeroplane", "type":"repo",
                   "depth":1, "via":"REPO_OF",
                   "path":["SADA","x-arcom/aeroplane"], "at":"..." } ] }
  ],
  "totals":   { "repo": 4, "person": 13, "issue": 240 },
  "nodes":    354,   // distinct entities reachable, all roles
  "returned": 26     // entities actually returned inside items
}
```

`total` is the real population; `shown`/`capped` describe the sample. This is the
lesson the Graph Explorer taught the hard way — its legend showed the SAMPLING
QUOTA (17) as if it were the population. A capped list must never be able to
masquerade as a total, so both numbers are always present.

A role that was explicitly requested and came back empty is returned with
`total: 0` rather than omitted: "no meetings last week" and "I forgot to look for
meetings" are different answers.

An unknown `entity` returns 404 with a `candidates` list (substring matches,
portfolios and ventures first).

### Worked example

```
POST /api/brain/graph/spine {"namespace":"flowos","entity":"SADA","depth":2,"perGroup":2}
→ 354 nodes in 13 groups:
   portfolio 1 · venture 3 · repo 4 · person 13 · agent 4 · activity 44 ·
   feed 1 · channel 2 · goal 4 · roadmap 5 · code_module 10 · doc 19 ·
   issue 240 · meeting 4
```

Add `"window":"7d"` and `activity` drops 44 → 7 while `repo` and `person` stay
put, which is the point.

---

## 2. Keeping it fresh — where each job runs

| job | what it does | where it runs | cadence |
|---|---|---|---|
| `flowos-sync.py` | FlowOS prod → brain memories (HTTPS) | `/opt/flowos-brain-sync`, Proxmox `159.195.203.241` | systemd timer, 1 min |
| `flowos-analytics.py` + `flowos-activity-rollups.py` | DuckDB analytics plane → rollup memories (HTTPS) | `/opt/cabrain-analytics`, same host | systemd timer, 1 h |
| `code-index.py` | repomix code/doc chunks (HTTPS) | `/opt/cabrain-code-index`, same host | systemd timer, 30 min |
| **`graph-sync-run.sh`** | **the graph builders: nodes, edges, repo links, spine, rollup relink** | **the Coder workspace** (`~/.cabrain-graph-sync`) | **supervised loop, 10 min** |

### Why the graph builders are NOT on the Proxmox box

They `INSERT` into `entities` / `entity_edges`, and there is no HTTP surface for
graph writes, so they need a direct **brain DSN**. Everything else on that host
writes over HTTPS and needs none.

An earlier attempt to move them there hit `password authentication failed for
user cabrain` against `10.10.10.30:5432` and concluded the credentials were
wrong. They were not — **`10.10.10.30` is the wrong database.** It is Proxmox LXC
102 `togo-db`, i.e. FlowOS PROD (`onestudio_hub`), which of course has no
`cabrain` role. The brain lives in the `pg` container on the `stack_stacknet`
docker network inside Docker Desktop on the operator's machine (`172.18.0.22`
internally, published to the workspace as `host.docker.internal:55432`, egress
`196.137.11.160`). From the Proxmox host, ports 5432, 55432 and 8080 on that
egress address are all closed — measured, not assumed. No credential fixes a
route that does not exist.

The Coder workspace is the only host that reaches **both**: the brain through the
Docker Desktop gateway, and FlowOS prod through an SSH tunnel to the Proxmox box
which `graph-sync-run.sh` opens for itself.

The workspace has no systemd and no cron (PID 1 is the coder agent), so the timer
is a `flock`-guarded supervised loop (`graph-sync-loop.sh`). Start it with:

```bash
nohup setsid ~/caBrain/scripts/graph-sync-loop.sh >/dev/null 2>&1 &
cat ~/.cabrain-graph-sync/state.json     # {"lastRun":"...","rc":0}
tail ~/.cabrain-graph-sync/run.log
```

Config (DSNs, token, tunnel target, interval) is `~/.cabrain-graph-sync/env`,
mode 600. **No secret is in the repo** — the scripts read the environment only.

If the graph ever needs to run somewhere durable and unattended, the real fix is
an HTTP graph-write endpoint so the builders can go over HTTPS like everything
else; then they can move to `/opt` next to the other three timers.

### The rollup relink step

`/api/brain/retain` does not update in place — a repeat `sourceRef` SUPERSEDES,
inserting a row with a NEW id while `memory_entities` still points at the old
one. The rollup job on the Proxmox host has no brain DSN, so every rollup it
refreshes comes back orphaned from the entity graph (its last run orphaned 26).
`flowos-rollup-relink.py` runs at the end of every graph pass and re-attaches
them from each rollup's own metadata — no FlowOS access needed.

---

## 3. Two data bugs this work fixed at the source

**Future-stamped rollups.** `flowos-activity-rollups.py` stamped `validAt` with
the period END, so the week in progress landed in the future (48 rollups at
`2026-08-02` while the run was on `2026-07-31`). Every recall bounded by
`until=now` silently excluded the newest week, and an agent asked about "last
week" concluded the brain had no activity since May. `end_ts()` now clamps to
now. Past periods are unaffected.

**English-only rollups.** Recall is vector + BM25, so an Arabic question scored
highest against the Arabic text sitting inside indexed SOURCE CODE and the
rollups never surfaced. The weekly `rollup:leaderboard:studio:<isoweek>` memory
is bilingual (English + Arabic), carries the FULL ranking rather than a top-5,
and names the **zero-commit** roster members explicitly — dropping them turns a
real "least active" answer into "no data". Only the newest week is labelled
"last week / اخر اسبوع"; older ones say "a past week", because 13 near-identical
summaries otherwise score within 0.02 of each other and "last week" lands on a
random one.
