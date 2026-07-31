#!/usr/bin/env python3
"""
FlowOS -> DuckDB analytics ETL.

Moves the high-volume FlowOS *event* tables (counting data, not semantic recall)
out of the production Postgres and into a local DuckDB file so that analytical
questions can be answered with NUMBERS, fast, without touching prod and without
polluting the vector brain. See docs/analytics.md for the why.

Source  : FlowOS production Postgres, read via STRICTLY READ-ONLY transactions.
Target  : data/flowos-analytics.duckdb (gitignored).

Credentials are NEVER stored in this file. The DSN is read from the environment:

    export FLOWOS_DSN='postgresql://user:pass@host:port/dbname'
    python3 scripts/flowos-analytics.py --full
    python3 scripts/flowos-analytics.py --since auto      # incremental
    python3 scripts/flowos-analytics.py --since 2026-07-01
    python3 scripts/flowos-analytics.py --views-only
    python3 scripts/flowos-analytics.py --stats

Incremental mode is keyed on each table's own time column and rows are upserted
by primary key (INSERT OR REPLACE), so re-running is idempotent -- it never
duplicates rows.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import sys
import time
import uuid
from urllib.parse import unquote, urlparse

try:
    import duckdb
except ImportError:  # pragma: no cover
    sys.exit("duckdb is not installed. Run: python3 -m pip install duckdb")

try:
    import pg8000.dbapi
except ImportError:  # pragma: no cover
    sys.exit("pg8000 is not installed. Run: python3 -m pip install pg8000")

try:
    import pyarrow as pa
except ImportError:  # pragma: no cover
    sys.exit("pyarrow is not installed. Run: python3 -m pip install pyarrow")


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DB = os.path.join(REPO_ROOT, "data", "flowos-analytics.duckdb")

# Overlap window re-pulled in `--since auto` mode so late-arriving / backfilled
# rows are not missed. Upsert-by-id makes the overlap free of side effects.
AUTO_LOOKBACK = dt.timedelta(days=2)


# --------------------------------------------------------------------------
# Table specifications
#
# `select` is the exact projection issued against Postgres. Enum (USER-DEFINED)
# columns are cast to ::text because they cannot be transported as-is, and
# reserved words are quoted. `ddl` is the matching DuckDB column list.
#
# Deliberately NOT copied:
#   claude_activity_events.prompt_text  -- free-form prompt bodies are semantic
#                                          content, not counting data. Length +
#                                          hash are kept so volume is measurable.
#   users.password_hash / calendar_token / email -- secrets & PII stay in prod.
# --------------------------------------------------------------------------

EVENT_TABLES = {
    "feature_events": {
        "time_col": "created_at",
        "select": [
            "id", "feature_key", '"event"', "user_id", "anon_id", "payload",
            "created_at", "session_id", "venture_id", "agent_id", '"path"',
            "referrer", "viewport_w", "viewport_h", "engagement_ms",
            "since_load_ms",
        ],
        "ddl": [
            ("id", "UUID"), ("feature_key", "VARCHAR"), ("event", "VARCHAR"),
            ("user_id", "UUID"), ("anon_id", "VARCHAR"), ("payload", "JSON"),
            ("created_at", "TIMESTAMPTZ"), ("session_id", "VARCHAR"),
            ("venture_id", "VARCHAR"), ("agent_id", "VARCHAR"),
            ("path", "VARCHAR"), ("referrer", "VARCHAR"),
            ("viewport_w", "INTEGER"), ("viewport_h", "INTEGER"),
            ("engagement_ms", "INTEGER"), ("since_load_ms", "INTEGER"),
        ],
    },
    "transition_events": {
        "time_col": "created_at",
        "select": [
            "id", "user_id", "session_id", '"event"', "payload", '"path"',
            "referrer", "user_agent", "viewport_w", "viewport_h",
            "since_load_ms", "created_at",
        ],
        "ddl": [
            ("id", "UUID"), ("user_id", "UUID"), ("session_id", "VARCHAR"),
            ("event", "VARCHAR"), ("payload", "JSON"), ("path", "VARCHAR"),
            ("referrer", "VARCHAR"), ("user_agent", "VARCHAR"),
            ("viewport_w", "INTEGER"), ("viewport_h", "INTEGER"),
            ("since_load_ms", "INTEGER"), ("created_at", "TIMESTAMPTZ"),
        ],
    },
    "claude_activity_events": {
        "time_col": "created_at",
        "select": [
            "id", "event_type::text", "actor_kind::text", "user_id", "agent_id",
            "token_id", "token_prefix", "session_id", "cwd", "prompt_length",
            "prompt_hash", "created_at", "input_tokens", "output_tokens",
            "cache_read_tokens", "cache_creation_tokens", "total_tokens",
            '"source"',
        ],
        "ddl": [
            ("id", "UUID"), ("event_type", "VARCHAR"), ("actor_kind", "VARCHAR"),
            ("user_id", "UUID"), ("agent_id", "VARCHAR"), ("token_id", "UUID"),
            ("token_prefix", "VARCHAR"), ("session_id", "VARCHAR"),
            ("cwd", "VARCHAR"), ("prompt_length", "INTEGER"),
            ("prompt_hash", "VARCHAR"), ("created_at", "TIMESTAMPTZ"),
            ("input_tokens", "INTEGER"), ("output_tokens", "INTEGER"),
            ("cache_read_tokens", "INTEGER"), ("cache_creation_tokens", "INTEGER"),
            ("total_tokens", "INTEGER"), ("source", "VARCHAR"),
        ],
    },
    "github_commits": {
        "time_col": "day",
        "select": [
            "id", "org_slug", "repo_full_name", "sha", "author_login",
            "authored_at", '"day"', '"hour"', "additions", "deletions",
            "synced_at", "message",
        ],
        "ddl": [
            ("id", "UUID"), ("org_slug", "VARCHAR"), ("repo_full_name", "VARCHAR"),
            ("sha", "VARCHAR"), ("author_login", "VARCHAR"),
            ("authored_at", "TIMESTAMPTZ"), ("day", "DATE"), ("hour", "SMALLINT"),
            ("additions", "INTEGER"), ("deletions", "INTEGER"),
            ("synced_at", "TIMESTAMPTZ"), ("message", "VARCHAR"),
        ],
    },
    "issue_activity": {
        "time_col": "created_at",
        "select": [
            "id", "issue_id", '"action"::text', "actor_kind::text",
            "actor_user_id", "actor_agent_id", "actor_external_id", "detail",
            "created_at",
        ],
        "ddl": [
            ("id", "UUID"), ("issue_id", "UUID"), ("action", "VARCHAR"),
            ("actor_kind", "VARCHAR"), ("actor_user_id", "UUID"),
            ("actor_agent_id", "VARCHAR"), ("actor_external_id", "UUID"),
            ("detail", "JSON"), ("created_at", "TIMESTAMPTZ"),
        ],
    },
    "notifications": {
        "time_col": "created_at",
        "select": [
            "id", "recipient_id", "actor_id", "kind", "link", "preview",
            "agent_id", "post_id", "comment_id", "read_at", "created_at",
        ],
        "ddl": [
            ("id", "UUID"), ("recipient_id", "UUID"), ("actor_id", "UUID"),
            ("kind", "VARCHAR"), ("link", "VARCHAR"), ("preview", "VARCHAR"),
            ("agent_id", "VARCHAR"), ("post_id", "UUID"), ("comment_id", "UUID"),
            ("read_at", "TIMESTAMPTZ"), ("created_at", "TIMESTAMPTZ"),
        ],
    },
    "agent_actions": {
        "time_col": "created_at",
        "select": [
            "id", "agent_id", "venture_id", '"trigger"::text', "kind",
            "target_ref", "summary", "payload", "created_at", "principal_id",
        ],
        "ddl": [
            ("id", "UUID"), ("agent_id", "VARCHAR"), ("venture_id", "VARCHAR"),
            ("trigger", "VARCHAR"), ("kind", "VARCHAR"), ("target_ref", "VARCHAR"),
            ("summary", "VARCHAR"), ("payload", "JSON"),
            ("created_at", "TIMESTAMPTZ"), ("principal_id", "UUID"),
        ],
    },
    "github_pull_requests": {
        "time_col": "created_at",
        "select": [
            "id", "org_slug", "repo_full_name", '"number"', "author_login",
            '"state"', "created_at", "merged_at", "closed_at", "first_review_at",
            "synced_at",
        ],
        "ddl": [
            ("id", "UUID"), ("org_slug", "VARCHAR"), ("repo_full_name", "VARCHAR"),
            ("number", "INTEGER"), ("author_login", "VARCHAR"),
            ("state", "VARCHAR"), ("created_at", "TIMESTAMPTZ"),
            ("merged_at", "TIMESTAMPTZ"), ("closed_at", "TIMESTAMPTZ"),
            ("first_review_at", "TIMESTAMPTZ"), ("synced_at", "TIMESTAMPTZ"),
        ],
    },
    "github_daily_metrics": {
        "time_col": "day",
        "select": [
            "id", '"day"', "org_slug", "repo_full_name", "login", "metric",
            '"count"', "synced_at",
        ],
        "ddl": [
            ("id", "UUID"), ("day", "DATE"), ("org_slug", "VARCHAR"),
            ("repo_full_name", "VARCHAR"), ("login", "VARCHAR"),
            ("metric", "VARCHAR"), ("count", "INTEGER"),
            ("synced_at", "TIMESTAMPTZ"),
        ],
    },
}

# Small dimension tables, fully reloaded every run so event rows can be labelled
# with human-readable names. No secrets, no PII beyond display names.
DIM_TABLES = {
    "dim_ventures": {
        "source": "ventures",
        "select": [
            "id", "portfolio_id", "name", "tagline", "stage::text",
            "kind::text", '"role"::text', "on_hold", "in_pipeline", "created_at",
        ],
        "ddl": [
            ("id", "VARCHAR"), ("portfolio_id", "VARCHAR"), ("name", "VARCHAR"),
            ("tagline", "VARCHAR"), ("stage", "VARCHAR"), ("kind", "VARCHAR"),
            ("role", "VARCHAR"), ("on_hold", "BOOLEAN"), ("in_pipeline", "BOOLEAN"),
            ("created_at", "TIMESTAMPTZ"),
        ],
    },
    "dim_users": {
        "source": "users",
        "select": [
            "id", "slug", "display_name", "position", "team::text",
            "account_type::text", "is_admin", "created_at", "deleted_at",
            "offboarded_at",
        ],
        "ddl": [
            ("id", "UUID"), ("slug", "VARCHAR"), ("display_name", "VARCHAR"),
            ("position", "VARCHAR"), ("team", "VARCHAR"),
            ("account_type", "VARCHAR"), ("is_admin", "BOOLEAN"),
            ("created_at", "TIMESTAMPTZ"), ("deleted_at", "TIMESTAMPTZ"),
            ("offboarded_at", "TIMESTAMPTZ"),
        ],
    },
}


# --------------------------------------------------------------------------
# Aggregate views
# --------------------------------------------------------------------------

VIEWS = {
    # ---- product usage -------------------------------------------------
    "v_feature_daily": """
        SELECT created_at::DATE                                AS day,
               feature_key,
               event,
               count(*)                                        AS events,
               count(DISTINCT user_id)                         AS users,
               count(DISTINCT session_id)                      AS sessions,
               sum(COALESCE(engagement_ms, 0)) / 1000.0        AS engagement_s
        FROM feature_events
        GROUP BY 1, 2, 3
    """,
    "v_dau": """
        SELECT day, count(DISTINCT user_id) AS dau, sum(events) AS events
        FROM (
            SELECT created_at::DATE AS day, user_id, count(*) AS events
            FROM feature_events WHERE user_id IS NOT NULL GROUP BY 1, 2
            UNION ALL
            SELECT created_at::DATE, user_id, count(*)
            FROM transition_events WHERE user_id IS NOT NULL GROUP BY 1, 2
        )
        GROUP BY 1
    """,
    "v_wau": """
        SELECT date_trunc('week', day)::DATE AS week,
               count(DISTINCT user_id)       AS wau
        FROM (
            SELECT created_at::DATE AS day, user_id FROM feature_events
            WHERE user_id IS NOT NULL
            UNION ALL
            SELECT created_at::DATE, user_id FROM transition_events
            WHERE user_id IS NOT NULL
        )
        GROUP BY 1
    """,
    "v_feature_adoption_weekly": """
        SELECT date_trunc('week', created_at)::DATE AS week,
               feature_key,
               count(*)                             AS events,
               count(DISTINCT user_id)              AS users
        FROM feature_events
        GROUP BY 1, 2
    """,
    "v_user_activity": """
        SELECT f.user_id,
               u.display_name,
               u.slug,
               u.team,
               count(*)                        AS events,
               count(DISTINCT f.created_at::DATE) AS active_days,
               min(f.created_at)               AS first_seen,
               max(f.created_at)               AS last_seen
        FROM feature_events f
        LEFT JOIN dim_users u ON u.id = f.user_id
        WHERE f.user_id IS NOT NULL
        GROUP BY 1, 2, 3, 4
    """,
    "v_top_paths": """
        SELECT path,
               count(*)                   AS events,
               count(DISTINCT user_id)    AS users,
               count(DISTINCT session_id) AS sessions
        FROM feature_events
        WHERE path IS NOT NULL
        GROUP BY 1
    """,

    # ---- per-venture ----------------------------------------------------
    "v_venture_activity_daily": """
        SELECT day, venture_id, venture_name, sum(events) AS events, source
        FROM (
            SELECT f.created_at::DATE AS day, f.venture_id,
                   v.name AS venture_name, count(*) AS events,
                   'feature_events' AS source
            FROM feature_events f
            LEFT JOIN dim_ventures v ON v.id = f.venture_id
            WHERE f.venture_id IS NOT NULL
            GROUP BY 1, 2, 3
            UNION ALL
            SELECT a.created_at::DATE, a.venture_id, v.name, count(*),
                   'agent_actions'
            FROM agent_actions a
            LEFT JOIN dim_ventures v ON v.id = a.venture_id
            WHERE a.venture_id IS NOT NULL
            GROUP BY 1, 2, 3
        )
        GROUP BY day, venture_id, venture_name, source
    """,
    "v_venture_totals": """
        SELECT v.id                     AS venture_id,
               v.name                   AS venture_name,
               v.stage,
               v.kind,
               COALESCE(fe.events, 0)   AS feature_events,
               COALESCE(fe.users, 0)    AS distinct_users,
               COALESCE(aa.actions, 0)  AS agent_actions,
               COALESCE(aa.agents, 0)   AS distinct_agents,
               greatest(COALESCE(fe.last_seen, '1970-01-01'::TIMESTAMPTZ),
                        COALESCE(aa.last_seen, '1970-01-01'::TIMESTAMPTZ)) AS last_activity
        FROM dim_ventures v
        LEFT JOIN (
            SELECT venture_id, count(*) AS events,
                   count(DISTINCT user_id) AS users, max(created_at) AS last_seen
            FROM feature_events WHERE venture_id IS NOT NULL GROUP BY 1
        ) fe ON fe.venture_id = v.id
        LEFT JOIN (
            SELECT venture_id, count(*) AS actions,
                   count(DISTINCT agent_id) AS agents, max(created_at) AS last_seen
            FROM agent_actions WHERE venture_id IS NOT NULL GROUP BY 1
        ) aa ON aa.venture_id = v.id
    """,

    # ---- engineering / github ------------------------------------------
    "v_commits_repo_weekly": """
        SELECT date_trunc('week', day)::DATE AS week,
               org_slug,
               repo_full_name,
               count(*)                      AS commits,
               count(DISTINCT author_login)  AS authors,
               sum(COALESCE(additions, 0))   AS additions,
               sum(COALESCE(deletions, 0))   AS deletions
        FROM github_commits
        GROUP BY 1, 2, 3
    """,
    "v_commits_author_daily": """
        SELECT day, author_login, count(*) AS commits,
               count(DISTINCT repo_full_name) AS repos,
               sum(COALESCE(additions, 0)) AS additions,
               sum(COALESCE(deletions, 0)) AS deletions
        FROM github_commits
        GROUP BY 1, 2
    """,
    "v_commit_hour_heatmap": """
        SELECT dayofweek(day) AS dow, hour, count(*) AS commits
        FROM github_commits
        WHERE hour IS NOT NULL
        GROUP BY 1, 2
    """,
    "v_pr_throughput_weekly": """
        SELECT date_trunc('week', created_at)::DATE AS week,
               repo_full_name,
               count(*)                                          AS opened,
               count(*) FILTER (WHERE merged_at IS NOT NULL)      AS merged,
               count(*) FILTER (WHERE first_review_at IS NOT NULL) AS reviewed,
               -- minutes, not hours: ~80% of these PRs merge inside an hour,
               -- so hour granularity rounds the whole metric to zero.
               median(date_diff('minute', created_at, merged_at))
                   FILTER (WHERE merged_at IS NOT NULL)           AS median_min_to_merge,
               median(date_diff('minute', created_at, first_review_at))
                   FILTER (WHERE first_review_at IS NOT NULL)     AS median_min_to_review
        FROM github_pull_requests
        GROUP BY 1, 2
    """,

    # ---- claude / agent usage -------------------------------------------
    "v_claude_daily": """
        SELECT created_at::DATE                      AS day,
               actor_kind,
               event_type,
               count(*)                              AS events,
               count(DISTINCT session_id)            AS sessions,
               count(DISTINCT COALESCE(CAST(user_id AS VARCHAR), agent_id)) AS actors,
               sum(COALESCE(total_tokens, 0))        AS total_tokens,
               sum(COALESCE(input_tokens, 0))        AS input_tokens,
               sum(COALESCE(output_tokens, 0))       AS output_tokens,
               sum(COALESCE(cache_read_tokens, 0))   AS cache_read_tokens,
               sum(COALESCE(cache_creation_tokens, 0)) AS cache_creation_tokens
        FROM claude_activity_events
        GROUP BY 1, 2, 3
    """,
    "v_claude_by_actor": """
        SELECT COALESCE(u.display_name, c.agent_id,
                        CAST(c.user_id AS VARCHAR), 'unknown') AS actor,
               c.actor_kind,
               count(*)                           AS events,
               count(DISTINCT c.session_id)       AS sessions,
               count(DISTINCT c.created_at::DATE) AS active_days,
               sum(COALESCE(c.total_tokens, 0))   AS total_tokens,
               max(c.created_at)                  AS last_seen
        FROM claude_activity_events c
        LEFT JOIN dim_users u ON u.id = c.user_id
        GROUP BY 1, 2
    """,
    "v_claude_sessions": """
        SELECT session_id,
               min(created_at)                  AS started_at,
               max(created_at)                  AS ended_at,
               date_diff('minute', min(created_at), max(created_at)) AS duration_min,
               count(*)                         AS events,
               sum(COALESCE(total_tokens, 0))   AS total_tokens,
               any_value(cwd)                   AS cwd
        FROM claude_activity_events
        WHERE session_id IS NOT NULL
        GROUP BY 1
    """,
    "v_agent_actions_daily": """
        SELECT a.created_at::DATE AS day, a.agent_id, a.trigger, a.kind,
               v.name AS venture_name, count(*) AS actions
        FROM agent_actions a
        LEFT JOIN dim_ventures v ON v.id = a.venture_id
        GROUP BY 1, 2, 3, 4, 5
    """,

    # ---- issues & notifications -----------------------------------------
    "v_issue_activity_daily": """
        SELECT created_at::DATE AS day, action, actor_kind,
               count(*) AS events, count(DISTINCT issue_id) AS issues
        FROM issue_activity
        GROUP BY 1, 2, 3
    """,
    "v_notifications_daily": """
        SELECT created_at::DATE AS day, kind,
               count(*)                                    AS sent,
               count(*) FILTER (WHERE read_at IS NOT NULL) AS read,
               round(100.0 * count(*) FILTER (WHERE read_at IS NOT NULL)
                     / nullif(count(*), 0), 1)             AS read_pct
        FROM notifications
        GROUP BY 1, 2
    """,
}


# --------------------------------------------------------------------------
# Helpers
# --------------------------------------------------------------------------

def log(msg: str) -> None:
    print(f"[{dt.datetime.now():%H:%M:%S}] {msg}", flush=True)


def connect_pg():
    """Connect to FlowOS prod using the DSN from $FLOWOS_DSN."""
    dsn = os.environ.get("FLOWOS_DSN")
    if not dsn:
        sys.exit(
            "FLOWOS_DSN is not set.\n"
            "  export FLOWOS_DSN='postgresql://user:pass@host:port/dbname'"
        )
    u = urlparse(dsn)
    if u.scheme not in ("postgres", "postgresql"):
        sys.exit(f"FLOWOS_DSN must be a postgres:// URL, got scheme {u.scheme!r}")
    kwargs = dict(
        host=u.hostname or "localhost",
        port=u.port or 5432,
        database=(u.path or "/").lstrip("/"),
        user=unquote(u.username or ""),
        password=unquote(u.password or ""),
    )
    if not kwargs["database"]:
        sys.exit("FLOWOS_DSN is missing the database name")
    return pg8000.dbapi.connect(**kwargs)


def coerce(value):
    """Make a Postgres value safe to hand to DuckDB's parameter binder."""
    if isinstance(value, uuid.UUID):
        return str(value)
    if isinstance(value, (dict, list)):
        return json.dumps(value, default=str)
    return value


# DuckDB type -> Arrow type. Batches are handed to DuckDB as Arrow tables
# (columnar, zero-copy) rather than row-at-a-time parameter binding: measured
# ~143k rows/s vs ~79 rows/s for executemany against a PRIMARY KEY table.
# The explicit schema also stops Arrow inferring `null` for all-NULL columns.
ARROW_TYPES = {
    "UUID": lambda: pa.string(),      # cast to UUID by DuckDB on insert
    "VARCHAR": lambda: pa.string(),
    "JSON": lambda: pa.string(),      # cast to JSON by DuckDB on insert
    "TIMESTAMPTZ": lambda: pa.timestamp("us", tz="UTC"),
    "DATE": lambda: pa.date32(),
    "INTEGER": lambda: pa.int32(),
    "SMALLINT": lambda: pa.int16(),
    "BIGINT": lambda: pa.int64(),
    "BOOLEAN": lambda: pa.bool_(),
}


def ddl_for(name: str, cols) -> str:
    """Table DDL. Deliberately NO PRIMARY KEY on the fact tables.

    DuckDB's ART index makes constrained inserts orders of magnitude slower and
    buys nothing here: de-duplication is done with a hash anti-join against the
    incoming batch (incremental mode) or by truncating first (full reload).
    """
    body = ", ".join(f'"{c}" {t}' for c, t in cols)
    return f'CREATE TABLE IF NOT EXISTS "{name}" ({body})'


def to_arrow(spec: dict, rows: list) -> "pa.Table":
    """Transpose fetched rows into a typed Arrow table."""
    ddl = spec["ddl"]
    data = {}
    for i, (col, duck_type) in enumerate(ddl):
        arrow_type = ARROW_TYPES[duck_type]()
        data[col] = pa.array([coerce(r[i]) for r in rows], type=arrow_type)
    return pa.table(data)


def insert_batch(duck, table: str, spec: dict, rows: list, dedupe: bool) -> None:
    """Bulk-load one batch, optionally replacing rows with matching ids."""
    batch = to_arrow(spec, rows)
    duck.register("_batch", batch)
    try:
        if dedupe:
            # The Arrow batch carries `id` as VARCHAR (UUIDs are transported as
            # strings), so the anti-join needs an explicit cast to the column's
            # declared type -- DuckDB refuses to compare UUID with VARCHAR.
            id_type = dict(spec["ddl"])["id"]
            duck.execute(
                f'DELETE FROM "{table}" '
                f'WHERE id IN (SELECT CAST(id AS {id_type}) FROM _batch)'
            )
        cols = ", ".join(f'"{c}"' for c, _ in spec["ddl"])
        duck.execute(f'INSERT INTO "{table}" ({cols}) SELECT {cols} FROM _batch')
    finally:
        duck.unregister("_batch")


def ensure_schema(duck) -> None:
    for name, spec in EVENT_TABLES.items():
        duck.execute(ddl_for(name, spec["ddl"]))
    for name, spec in DIM_TABLES.items():
        duck.execute(ddl_for(name, spec["ddl"]))
    duck.execute("""
        CREATE TABLE IF NOT EXISTS _etl_watermark (
            table_name VARCHAR PRIMARY KEY,
            max_time   TIMESTAMPTZ,
            row_count  BIGINT,
            last_run   TIMESTAMPTZ,
            last_mode  VARCHAR
        )
    """)


def parse_ts(text: str | None):
    """Parse DuckDB's VARCHAR rendering of a TIMESTAMPTZ / DATE.

    Timestamps are read as strings rather than native objects so the script has
    no runtime dependency on pytz (which DuckDB requires to materialise
    TIMESTAMPTZ values into Python).
    """
    if not text:
        return None
    t = text.strip()
    # '+00' / '+00:00' tail -> normalise so %z can read it
    for fmt in ("%Y-%m-%d %H:%M:%S.%f%z", "%Y-%m-%d %H:%M:%S%z",
                "%Y-%m-%d %H:%M:%S.%f", "%Y-%m-%d %H:%M:%S", "%Y-%m-%d"):
        try:
            v = dt.datetime.strptime(t, fmt)
            return v if v.tzinfo else v.replace(tzinfo=dt.timezone.utc)
        except ValueError:
            continue
    if len(t) >= 3 and (t[-3] in "+-") and ":" not in t[-3:]:
        return parse_ts(t + ":00")
    return None


def get_watermark(duck, table: str):
    row = duck.execute(
        "SELECT CAST(max_time AS VARCHAR) FROM _etl_watermark WHERE table_name = ?",
        [table],
    ).fetchone()
    return parse_ts(row[0]) if row else None


def set_watermark(duck, table: str, mode: str) -> None:
    """Recompute the watermark entirely inside DuckDB (no Python round trip)."""
    spec = EVENT_TABLES.get(table) or DIM_TABLES.get(table)
    time_col = spec.get("time_col") if spec else None
    mx = f'max("{time_col}")' if time_col else "CAST(NULL AS TIMESTAMPTZ)"
    duck.execute("DELETE FROM _etl_watermark WHERE table_name = ?", [table])
    duck.execute(
        f'INSERT INTO _etl_watermark '
        f'SELECT ?, {mx}, count(*), now(), ? FROM "{table}"',
        [table, mode],
    )


# --------------------------------------------------------------------------
# Extract / load
# --------------------------------------------------------------------------

def load_event_table(pg, duck, table: str, spec: dict, since, batch: int,
                     limit: int | None) -> tuple[int, float]:
    """Keyset-paginated copy of one event table. Returns (rows, seconds).

    Two pagination strategies, because feature_events and transition_events have
    NO standalone index on created_at -- ordering by time there forces a full
    sort of ~465k rows on every batch (measured 8.8s vs 4.5s per 20k batch):

      full reload  -> keyset on `id` alone, which rides the primary-key btree
                      with no sort at all, and the table is truncated first so
                      appends need no de-duplication.
      incremental  -> keyset on (time_col, id) with a `time_col >= since`
                      predicate, which is the only ordering that can page a
                      time window correctly. Rows are upserted by id.
    """
    time_col = spec["time_col"]
    ddl_names = [c for c, _ in spec["ddl"]]
    proj = ", ".join(spec["select"])
    ti, ii = ddl_names.index(time_col), ddl_names.index("id")
    incremental = since is not None

    if not incremental:
        duck.execute(f'DELETE FROM "{table}"')  # full reload -> clean slate

    cur = pg.cursor()
    cur.execute("BEGIN READ ONLY")

    total, t0 = 0, time.time()
    last_t, last_id = since, None
    try:
        while True:
            take = batch if limit is None else min(batch, limit - total)
            if take <= 0:
                break

            if not incremental:
                if last_id is None:
                    sql = f"SELECT {proj} FROM {table} ORDER BY id LIMIT {take}"
                    params = ()
                else:
                    sql = (f"SELECT {proj} FROM {table} WHERE id > %s "
                           f"ORDER BY id LIMIT {take}")
                    params = (last_id,)
            elif last_id is None:
                sql = (f"SELECT {proj} FROM {table} WHERE \"{time_col}\" >= %s "
                       f'ORDER BY "{time_col}", id LIMIT {take}')
                params = (last_t,)
            else:
                sql = (f"SELECT {proj} FROM {table} "
                       f'WHERE "{time_col}" >= %s AND ("{time_col}", id) > (%s, %s) '
                       f'ORDER BY "{time_col}", id LIMIT {take}')
                params = (since, last_t, last_id)

            cur.execute(sql, params)
            rows = cur.fetchall()
            if not rows:
                break

            last_t, last_id = rows[-1][ti], rows[-1][ii]
            insert_batch(duck, table, spec, rows, dedupe=incremental)

            total += len(rows)
            if total % (batch * 5) == 0:
                log(f"    {table}: {total:,} rows ...")
            if len(rows) < take:
                break
    finally:
        cur.execute("ROLLBACK")
        cur.close()

    return total, time.time() - t0


def load_dim_table(pg, duck, name: str, spec: dict) -> tuple[int, float]:
    """Full snapshot reload of a small dimension table."""
    proj = ", ".join(spec["select"])

    cur = pg.cursor()
    cur.execute("BEGIN READ ONLY")
    t0 = time.time()
    try:
        cur.execute(f"SELECT {proj} FROM {spec['source']}")
        rows = cur.fetchall()
    finally:
        cur.execute("ROLLBACK")
        cur.close()

    duck.execute(f'DELETE FROM "{name}"')
    if rows:
        insert_batch(duck, name, spec, rows, dedupe=False)
    return len(rows), time.time() - t0


def build_views(duck) -> None:
    for name, sql in VIEWS.items():
        duck.execute(f"CREATE OR REPLACE VIEW {name} AS {sql}")
    log(f"  built {len(VIEWS)} views")


def show_stats(duck) -> None:
    print(f"\n{'table':<26} {'rows':>10}  {'max_time':<28} last_run")
    print("-" * 96)
    rows = duck.execute("""
        SELECT table_name, row_count,
               CAST(max_time AS VARCHAR), strftime(last_run, '%Y-%m-%d %H:%M')
        FROM _etl_watermark ORDER BY row_count DESC
    """).fetchall()
    for t, n, mx, lr in rows:
        print(f"{t:<26} {n or 0:>10,}  {(mx or ''):<28} {lr}")
    total = sum((r[1] or 0) for r in rows)
    print("-" * 96)
    print(f"{'TOTAL':<26} {total:>10,}")
    views = duck.execute("""
        SELECT view_name FROM duckdb_views()
        WHERE NOT internal ORDER BY view_name
    """).fetchall()
    print(f"\n{len(views)} views: " + ", ".join(v[0] for v in views))


# --------------------------------------------------------------------------

def parse_since(value: str):
    for fmt in ("%Y-%m-%d", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%d %H:%M:%S"):
        try:
            return dt.datetime.strptime(value, fmt).replace(tzinfo=dt.timezone.utc)
        except ValueError:
            continue
    raise argparse.ArgumentTypeError(f"unparseable --since value: {value!r}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default=DEFAULT_DB, help=f"DuckDB file (default {DEFAULT_DB})")
    ap.add_argument("--since", help="'auto' (use stored watermark) or YYYY-MM-DD")
    ap.add_argument("--full", action="store_true", help="reload everything from scratch")
    ap.add_argument("--tables", help="comma-separated subset of event tables")
    ap.add_argument("--batch", type=int, default=20000, help="rows per fetch (default 20000)")
    ap.add_argument("--limit", type=int, help="max rows per table (bounded load)")
    ap.add_argument("--skip-dims", action="store_true", help="don't refresh dimension tables")
    ap.add_argument("--views-only", action="store_true", help="rebuild views, no extract")
    ap.add_argument("--stats", action="store_true", help="print load stats and exit")
    args = ap.parse_args()

    os.makedirs(os.path.dirname(os.path.abspath(args.db)), exist_ok=True)
    duck = duckdb.connect(args.db)
    ensure_schema(duck)

    if args.stats:
        show_stats(duck)
        return 0
    if args.views_only:
        build_views(duck)
        return 0

    tables = list(EVENT_TABLES)
    if args.tables:
        want = [t.strip() for t in args.tables.split(",") if t.strip()]
        bad = [t for t in want if t not in EVENT_TABLES]
        if bad:
            sys.exit(f"unknown table(s): {', '.join(bad)}")
        tables = want

    mode = "full" if args.full else (f"since={args.since}" if args.since else "full")
    log(f"target {args.db}  (mode: {mode})")

    pg = connect_pg()
    grand, t_start = 0, time.time()
    try:
        if not args.skip_dims:
            for name, spec in DIM_TABLES.items():
                n, secs = load_dim_table(pg, duck, name, spec)
                set_watermark(duck, name, "snapshot")
                log(f"  {name:<26} {n:>7,} rows  {secs:6.2f}s  (snapshot)")

        for table in tables:
            spec = EVENT_TABLES[table]
            since = None
            if not args.full and args.since:
                if args.since == "auto":
                    wm = get_watermark(duck, table)
                    if wm is not None:
                        since = wm - AUTO_LOOKBACK
                else:
                    since = parse_since(args.since)
                if spec["time_col"] == "day" and since is not None:
                    since = since.date()

            n, secs = load_event_table(pg, duck, table, spec, since,
                                       args.batch, args.limit)
            set_watermark(duck, table, mode)
            grand += n
            rate = n / secs if secs > 0 else 0
            tag = f"since {since}" if since else "full"
            log(f"  {table:<26} {n:>7,} rows  {secs:6.2f}s  "
                f"({rate:,.0f}/s, {tag})")
    finally:
        pg.close()

    log(f"extract done: {grand:,} rows in {time.time() - t_start:.1f}s")
    build_views(duck)
    duck.execute("CHECKPOINT")
    show_stats(duck)
    duck.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
