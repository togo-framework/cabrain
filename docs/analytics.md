# FlowOS analytics plane (DuckDB)

CaBrain answers two very different kinds of question, and they need two very
different stores. This document explains the split, why it exists, and how to
run the ETL.

## The split

| | Vector brain (Postgres + pgvector) | Analytics plane (DuckDB) |
|---|---|---|
| **Question shape** | "What do we know about X?" | "How many X, and when?" |
| **Answers with** | passages, facts, relationships | numbers, rates, trends |
| **Contents** | ventures, people, agents, issues, posts, decisions, learnings, research | `feature_events`, `transition_events`, `claude_activity_events`, `github_commits`, `issue_activity`, `notifications`, `agent_actions`, `github_pull_requests`, `github_daily_metrics` |
| **Scale** | ~1.8k memories (`flowos`), 159 entities, ~435 edges | ~670k event rows |
| **Access** | `memory_recall` MCP tool | SQL over `data/flowos-analytics.duckdb` |

### Why the event tables are NOT embedded

This is the important part, and it is a deliberate decision rather than an
omission.

The ~670k rows in the event tables are **counting data, not semantic content**.
A row of `feature_events` says "user U clicked feature F at time T". Embedding
that produces a vector that is nearly identical to the 465,969 other rows just
like it. The practical consequences:

1. **Recall drowns.** Semantic search ranks by similarity. Half a million
   near-duplicate low-information vectors crowd out the few hundred memories
   that actually carry meaning. This has already happened once — flooding the
   brain with event rows is what degraded recall previously.
2. **It answers the question badly anyway.** "How many commits last week?" is a
   `COUNT(*)`, not a nearest-neighbour search. Vector search cannot count; it
   can only retrieve the *k* most similar rows, which is the wrong operation.
   Asking it for a total gives you a plausible-looking wrong number.
3. **Cost.** Embedding 670k rows is a large, recurring bill for data whose
   entire information content is already captured by its columns.

So the rule is:

> **Semantic content goes in the vector brain. Countable events go in DuckDB.**

If you want to know *what a venture is about*, recall it. If you want to know
*how active it was in July*, query DuckDB.

### …but the brain still needs to be able to answer "who was busy?"

The split above is right and stays. Its side effect, though, was that the brain
could not answer *any* activity question: ask it "who was most active last
week?" and the numbers were in a file nothing consults.

The fix is **rollups, not rows** — see "Activity rollups" below. A few hundred
period summaries carry the shape of the activity (who, which repo, how much);
the 670k rows stay in DuckDB and remain the only place an exact `COUNT(*)`
comes from.

### What is deliberately dropped in transit

- `claude_activity_events.prompt_text` — free-form prompt bodies are semantic
  content, and belong on the brain side if anywhere. `prompt_length` and
  `prompt_hash` are kept so volume is still measurable.
- `users.password_hash`, `users.calendar_token`, `users.email` — secrets and PII
  stay in production. The dimension table carries only display names and roles.

## Safety

The ETL reads FlowOS **production** Postgres. Every read is wrapped in
`BEGIN READ ONLY` … `ROLLBACK`. The script issues no `INSERT`, `UPDATE`,
`DELETE`, or DDL against Postgres — it cannot, the transaction is read-only.
All writes go to the local DuckDB file.

`data/` is gitignored. The DuckDB file is derived data and must never be
committed — it contains customer production data.

## Running the ETL

Credentials come from the environment. **Never** put the DSN in a file.

```bash
export FLOWOS_DSN='postgresql://USER:PASS@HOST:PORT/onestudio_hub'

# first load / full rebuild (~670k rows)
python3 scripts/flowos-analytics.py --full

# incremental: pull only what is new since the stored watermark
python3 scripts/flowos-analytics.py --since auto

# incremental from an explicit date
python3 scripts/flowos-analytics.py --since 2026-07-01

# rebuild only the aggregate views (no Postgres access)
python3 scripts/flowos-analytics.py --views-only

# what is loaded right now
python3 scripts/flowos-analytics.py --stats
```

Dependencies: `pip install duckdb pg8000 pyarrow`.

### Incremental mode

`--since auto` reads each table's stored watermark from `_etl_watermark` and
re-pulls from **two days before** it, so late-arriving or backfilled rows are not
missed. The overlap is safe because incremental batches delete rows with
matching `id` before inserting — re-running never duplicates data.

`--full` truncates each table and reloads, which is also idempotent.

### Performance notes (measured, not guessed)

Two things dominate, and both were found by benchmarking rather than assumption:

- **Never bind rows one at a time.** Batches are handed to DuckDB as Arrow
  tables. Measured on a 20k-row batch of `feature_events`:
  `executemany` into a `PRIMARY KEY` table ran at **79 rows/s**; the same data
  via Arrow ran at **143,000 rows/s**. The fact tables therefore have **no
  primary key** — DuckDB's ART index makes constrained inserts pathologically
  slow and buys nothing, since de-duplication is a hash anti-join.
- **Order by `id`, not by time, on full loads.** `feature_events` and
  `transition_events` have no standalone index on `created_at`, so paging by
  time forces a full sort of ~465k rows per batch (8.8s vs 4.5s per 20k batch).
  Full loads page on the primary-key btree instead; only incremental mode pages
  by `(created_at, id)`, where the time predicate is unavoidable.

The extract from Postgres over the SSH tunnel is now the only bottleneck.

## Schema

Fact tables mirror their Postgres sources (enums cast to `VARCHAR`, `jsonb` kept
as `JSON`). Two dimension tables are fully re-snapshotted on every run:
`dim_ventures` (68 rows) and `dim_users` (47 rows).

`_etl_watermark` tracks `table_name`, `max_time`, `row_count`, `last_run`,
`last_mode`.

### Views

**Product usage** — `v_dau`, `v_wau`, `v_feature_daily`,
`v_feature_adoption_weekly`, `v_user_activity`, `v_top_paths`

**Per-venture** — `v_venture_activity_daily`, `v_venture_totals`

**Engineering** — `v_commits_repo_weekly`, `v_commits_author_daily`,
`v_commit_hour_heatmap`, `v_pr_throughput_weekly`

**Claude / agents** — `v_claude_daily`, `v_claude_by_actor`,
`v_claude_sessions`, `v_agent_actions_daily`

**Issues / notifications** — `v_issue_activity_daily`, `v_notifications_daily`

## Example queries

Open the database:

```bash
python3 -c "import duckdb; duckdb.connect('data/flowos-analytics.duckdb')"
# or: duckdb data/flowos-analytics.duckdb
```

**Top 5 repos by commits in the last 30 days**

```sql
SELECT repo_full_name, count(*) AS commits,
       count(DISTINCT author_login) AS authors,
       sum(additions) AS added, sum(deletions) AS removed
FROM github_commits
WHERE day >= (SELECT max(day) FROM github_commits) - INTERVAL 30 DAY
GROUP BY 1 ORDER BY commits DESC LIMIT 5;
```

**Daily active users, last 14 days**

```sql
SELECT day, dau, events
FROM v_dau
WHERE day >= (SELECT max(day) FROM v_dau) - INTERVAL 14 DAY
ORDER BY day DESC;
```

**Feature adoption — top features this month vs last**

```sql
SELECT feature_key,
       count(*) FILTER (WHERE created_at >= date_trunc('month', now())) AS this_month,
       count(*) FILTER (WHERE created_at <  date_trunc('month', now())) AS earlier,
       count(DISTINCT user_id) AS users
FROM feature_events
GROUP BY 1 ORDER BY this_month DESC LIMIT 10;
```

**Claude token burn per day**

```sql
SELECT day, sum(events) AS events, sum(total_tokens) AS tokens,
       sum(cache_read_tokens) AS cache_reads
FROM v_claude_daily
GROUP BY 1 ORDER BY day DESC LIMIT 14;
```

**Most active ventures**

```sql
SELECT venture_name, stage, feature_events, distinct_users, agent_actions
FROM v_venture_totals
ORDER BY feature_events DESC LIMIT 10;
```

## Activity rollups (DuckDB → vector brain)

`scripts/flowos-activity-rollups.py` reads the analytics plane and writes
**periodic summaries** into the `flowos` brain as `source_kind =
flowos_activity_rollup`. Prose with the real numbers inline, so it both reads
and embeds well:

> In ISO week 2026-W30, the week of Monday 20 July 2026 (2026-07-20 to
> 2026-07-26) the onestudio-co/agents repository (onestudio-co) saw 130 commits
> from 6 contributors on 6 active days, +669,896/-6,394 lines …

### Families and volume

| sourceRef | scope | count |
|---|---|---|
| `rollup:commits:<repo>:<isoweek>` | weekly per-repo | 145 |
| `rollup:person-commits:<person>:<isoweek>` | weekly per-person | 143 |
| `rollup:claude:<user_id>:<isoweek>` | weekly per-person AI usage | 136 |
| `rollup:commits:studio:<isoweek>` | weekly studio-wide | 13 |
| `rollup:claude:studio:<isoweek>` | weekly studio-wide AI | 7 |
| `rollup:commits:org:<org>:<month>` | monthly per-org | 21 |
| `rollup:studio:<month>` | monthly studio-wide | 3 |
| `rollup:claude:attribution-limits` | what the AI data cannot say | 1 |

**469 memories, not 670,000.** Per-repo and per-person weekly rollups are capped
at `--top` (12) per week with a `--min-commits` floor (3). Everything under the
cap is still counted inside the weekly studio and monthly org rollups, so no
activity is silently dropped — only its per-repo detail.

Two conventions matter:

- `validAt` = the **end** of the period, so the memory is timestamped when it
  became true. Periods the data does not fully cover are labelled *"period still
  partial"* inline, so a half-week never reads as a slowdown.
- Content spells the week **both ways** — `2026-W30` *and* "the week of Monday
  20 July 2026". Recall is vector + BM25 and nobody phrases a question as
  "2026-W30"; without the natural-language form, period-specific queries
  retrieve an arbitrary week. This was measured, not assumed.

### Graph wiring

Each rollup is linked to the repo and person entities it names via
`memory_entities` — the table `Store.expandEntities` walks for 1-hop spreading
activation. Recall results then carry `viaEntity`, e.g. asking about
`sentra-intel/sentra` pulls in that repo's other weekly rollups. 1,466 links
across 158 entities.

`person -[USED_AI_ON]-> repo` edges are also written, with an open-edge check
before insert (re-runs UPDATE in place). **21 edges, and they are weak on
purpose**: `claude_activity_events` has `user_id` but **no** `venture_id` and
**no** repo column, and the one indirect signal, `cwd`, is populated on 1,160 of
58,898 events (2.0%) — all of them between 2026-06-16 and 2026-06-18, after
which the emitter stopped sending it. Each edge's `fact` states that window. A
`rollup:claude:attribution-limits` memory says the same thing in prose so
"which venture burned the most Claude tokens" gets an honest *cannot answer*
instead of a confident wrong number.

### Running it

```bash
export FLOWOS_DSN='postgresql://…/onestudio_hub'     # read-only; login→person map
export CABRAIN_TOKEN=cbt_…
export CABRAIN_DSN='postgresql://cabrain:…/cabrain'

python3 scripts/flowos-activity-rollups.py --dry-run   # build + print, write nothing
python3 scripts/flowos-activity-rollups.py             # retain + wire the graph
```

Re-running is safe: `sourceRef` is stable and Retain treats a set `sourceRef` as
an identity, so exactly one **live** row per rollup survives (the previous
version is soft-invalidated, never deleted). Run it after each
`flowos-analytics.py --since auto`.

`FLOWOS_DSN` is used only for `users.github_usernames` — one human commits under
several logins (`fadymondy`, `togo`, …) and counts must be merged on the
resolved person or the same name appears twice in one "most active" list.

### What rollups still cannot answer

- **Relative time.** "Last week" has no meaning to a vector index; it returns an
  arbitrary week. Resolve the date first, then ask "the week of 20 July 2026".
- **Exact totals for anything outside a rollup's cap.** Recall retrieves; it
  does not sum. For a precise number, query DuckDB.
- **AI usage per venture or repo** — see the attribution note above.
- **Bot vs human.** `author_login` values like `claude` and `dependabot[bot]`
  are carried through as-is and appear as "people" in commit rollups.

## Measured performance

Full load of all 9 event tables: **670,072 rows in 352s** (~1,900 rows/s,
bounded by the Postgres read over the SSH tunnel). Resulting file: 64 MB.
Incremental `--since auto` over the four smaller tables: 2,078 rows in 4.4s.

Query latency against the loaded database:

| Query | Time |
|---|---|
| Top 5 repos by commits, last 30 days | 20 ms |
| DAU, last 14 days (union of 561k event rows) | 86 ms |
| Top 10 features by events + distinct users/sessions | 40 ms |
| Claude token burn per day | 42 ms |
| Top 10 ventures by activity | 11 ms |
| PR throughput + median merge latency | 23 ms |
| Full scan of all 465,990 `feature_events` | 39 ms |

## Verification

The load was checked against production rather than assumed correct. All 9
tables matched exactly on row count, distinct `id` count, and value sums —
e.g. `sum(engagement_ms)` = 16,627,400,255,678 and
`sum(total_tokens)` = 8,890,374,732 in both systems.

Idempotency was verified by running `--since auto` and a deliberately wide
`--since 2026-07-01` overlap four times in total: row counts stayed identical to
distinct-id counts, i.e. zero duplicates.

### Data caveats found during verification

- `feature_events.engagement_ms` sums to an implausibly large number
  (~4.6M hours across 466k events). This is faithful to the source — the ETL
  reproduces it exactly — so it is a **producer-side** issue in how the column is
  emitted (it looks cumulative rather than per-event). Do not treat
  `engagement_ms` as a per-event dwell time without checking the emitter.
- Median PR time-to-merge is 1.5-14 **minutes**: ~80% of PRs (1,332 of 1,669)
  merge within an hour, which is why `v_pr_throughput_weekly` reports minutes.
  These are largely automated merges, not human review cycles.
- `feature_events.user_id` has only 39 distinct non-null values, and some
  feature keys (`github.realtime_sync`) have no user attribution at all.
