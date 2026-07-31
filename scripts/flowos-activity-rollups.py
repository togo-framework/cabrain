#!/usr/bin/env python3
"""
FlowOS activity ROLLUPS -> CaBrain vector brain.

The raw event tables live in DuckDB (see docs/analytics.md) because they are
counting data, not semantic content. That split is right, but it left the brain
unable to answer *any* activity question -- "who was most active last week?",
"which repo was hottest in July?" -- because the numbers sit in a file nothing
consults.

This script closes that gap without re-drowning recall: it reads the DuckDB
analytics plane and writes a few hundred PERIODIC SUMMARIES (prose, with the real
numbers inline) into the flowos brain, then wires each one to the repo / person
entities it concerns so entity expansion can reach it.

Rollup families (all source_kind = flowos_activity_rollup):

  rollup:commits:<repo>:<isoweek>          weekly per-repo engineering activity
  rollup:person-commits:<login>:<isoweek>  weekly per-person commit activity
  rollup:claude:<user_id>:<isoweek>        weekly per-person Claude Code usage
  rollup:commits:studio:<isoweek>          weekly studio-wide engineering summary
  rollup:claude:studio:<isoweek>           weekly studio-wide Claude summary
  rollup:commits:org:<org>:<month>         monthly per-org engineering summary
  rollup:studio:<month>                    monthly studio-wide summary

Per-repo and per-person weekly rollups are capped to the top N of each week
(--top, default 12) with a minimum commit floor (--min-commits, default 3) so the
volume stays in the hundreds. Everything below the cap is still represented by
the weekly studio and monthly org rollups.

Idempotent: sourceRef is stable and the Retain endpoint treats a set sourceRef as
an identity (no similarity write-decision), so re-running UPDATEs in place.
memory_entities links are ON CONFLICT DO NOTHING; USED_AI_ON edges do an
open-edge check before inserting.

Credentials come from the environment -- never hard-code them:

    export CABRAIN_API=https://cabrain.fadymondy.com
    export CABRAIN_TOKEN=cbt_...
    export CABRAIN_DSN=postgresql://cabrain:***@host:5432/cabrain
    python3 scripts/flowos-activity-rollups.py --dry-run
    python3 scripts/flowos-activity-rollups.py
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import subprocess
import sys
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlparse

import duckdb
import pg8000.native as pg

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DB = os.path.join(REPO_ROOT, "data", "flowos-analytics.duckdb")
NS = os.environ.get("FLOWOS_NAMESPACE", "flowos")
SOURCE_KIND = "flowos_activity_rollup"


# --------------------------------------------------------------- write gating
# /api/brain/retain does NOT mutate a row in place: a repeat sourceRef SUPERSEDES
# (soft-retires the old row, inserts a new one with a NEW id). Two consequences
# make blind re-retaining unsafe on a timer:
#   * memories grows by one retired row per rollup per run, and
#   * memory_entities rows still point at the OLD id, so the rollup silently
#     drops out of the entity graph.
# Only the current week/month actually change between runs, so we hash each
# rollup's content and re-retain ONLY what really moved. Same idea as the
# sha-gating in code-index.py, and it works with no brain DSN.
def content_hash(rec: dict) -> str:
    import hashlib
    return hashlib.sha256(rec["content"].encode("utf-8")).hexdigest()[:16]


def load_hashes(path: str) -> dict:
    try:
        with open(path) as fh:
            return json.load(fh).get("hashes", {})
    except Exception:
        return {}


def save_hashes(path: str, hashes: dict) -> None:
    tmp = path + ".tmp"
    os.makedirs(os.path.dirname(os.path.abspath(path)) or ".", exist_ok=True)
    with open(tmp, "w") as fh:
        json.dump({"updated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                   "hashes": hashes}, fh)
    os.replace(tmp, path)
MONTHS = ["", "January", "February", "March", "April", "May", "June", "July",
          "August", "September", "October", "November", "December"]


# ---------------------------------------------------------------- formatting
def n(x) -> str:
    return f"{int(x or 0):,}"


def tokens(x) -> str:
    x = int(x or 0)
    if x >= 1_000_000_000:
        return f"{x/1_000_000_000:.2f}B"
    if x >= 1_000_000:
        return f"{x/1_000_000:.1f}M"
    if x >= 1_000:
        return f"{x/1_000:.1f}k"
    return str(x)


def isoweek(monday: dt.date) -> str:
    y, w, _ = monday.isocalendar()
    return f"{y}-W{w:02d}"


def week_end(monday: dt.date) -> dt.date:
    return monday + dt.timedelta(days=6)


def month_end(first: dt.date) -> dt.date:
    nxt = (first.replace(day=28) + dt.timedelta(days=4)).replace(day=1)
    return nxt - dt.timedelta(days=1)


def end_ts(d: dt.date) -> str:
    return dt.datetime(d.year, d.month, d.day, 23, 59, 59,
                       tzinfo=dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def listing(pairs, unit="", limit=4) -> str:
    """'X (120), Y (55)' from [(name, count), ...]."""
    out = [f"{p} ({n(c)}{unit})" for p, c in pairs[:limit]]
    return ", ".join(out)


# ------------------------------------------------------------------ brain io
class Brain:
    """Retain goes over HTTPS; the graph work needs a direct brain DSN.

    CABRAIN_DSN is OPTIONAL. The brain Postgres is not reachable from every host
    that can run this job (the deployed timer box reaches the API but not the
    database), so with no DSN we still refresh every rollup's *content* through
    the API and simply skip the entity linking / USED_AI_ON edges. `self.db is
    None` is the flag for that degraded-but-useful mode.
    """

    def __init__(self, api: str, token: str, dsn: str = ""):
        self.api, self.token = api.rstrip("/"), token
        self.db = None
        if dsn:
            u = urlparse(dsn)
            self.db = pg.Connection(user=u.username, password=u.password,
                                    host=u.hostname, port=u.port or 5432,
                                    database=(u.path or "/").lstrip("/"))
            self.db.run("SET search_path = public")

    def retain(self, rec: dict) -> str | None:
        """POST one rollup. curl, not urllib: Cloudflare 403s the urllib UA."""
        body = json.dumps(rec)
        p = subprocess.run(
            ["curl", "-sS", "--fail-with-body", "-m", "120", "-X", "POST",
             f"{self.api}/api/brain/retain",
             "-H", f"X-Cabrain-Token: {self.token}",
             "-H", "Content-Type: application/json",
             "--data-binary", "@-"],
            input=body, capture_output=True, text=True)
        if p.returncode != 0:
            print(f"  ! retain failed {rec['sourceRef']}: "
                  f"{p.stdout[:200]}{p.stderr[:200]}", file=sys.stderr)
            return None
        try:
            return json.loads(p.stdout)["id"]
        except Exception:
            print(f"  ! bad retain response {rec['sourceRef']}: {p.stdout[:200]}",
                  file=sys.stderr)
            return None


def load_entities(b: Brain):
    """(entity_type -> {source_id: id}), plus person name -> id."""
    by_type: dict[str, dict[str, str]] = defaultdict(dict)
    by_name: dict[str, str] = {}
    for eid, name, etype, meta in b.db.run(
            "SELECT id, name, entity_type, metadata FROM entities WHERE namespace = :ns",
            ns=NS):
        sid = (meta or {}).get("source_id")
        if sid:
            by_type[etype][str(sid)] = eid
        if etype == "person":
            by_name[name.strip().lower()] = eid
    return by_type, by_name


# ------------------------------------------------------------------- rollups
def build(duck, gh_login_to_user: dict, weeks: int, top: int, min_commits: int):
    """Return a list of (record, [entity source keys]) ready to retain."""
    q = lambda s, **p: duck.execute(s, p).fetchall()

    max_day = q("SELECT max(day)::VARCHAR FROM github_commits")[0][0]
    max_day = dt.date.fromisoformat(max_day)
    first_monday = max_day - dt.timedelta(days=max_day.weekday()) \
        - dt.timedelta(weeks=weeks - 1)

    # login -> (display_name, user_id); unmapped logins keep the raw login.
    def person(login: str) -> tuple[str, str | None]:
        u = gh_login_to_user.get((login or "").lower())
        return (u["name"], u["id"]) if u else (login or "unknown", None)

    def merge_people(login_counts):
        """[(login, c), ...] -> [(display_name, c), ...] summed & sorted.

        Several github logins map to the same human (users.github_usernames is
        an array), so counts must be merged AFTER resolving, or the same person
        shows up twice in one 'most active' list.
        """
        agg = defaultdict(int)
        for login, c in login_counts:
            agg[person(login)[0]] += int(c)
        return sorted(agg.items(), key=lambda x: -x[1])

    def wl(wk: dt.date) -> str:
        """Human + ISO week label.

        Both spellings matter: recall is vector + BM25, and a bare "2026-W30"
        token is not something a question is usually phrased with, so the
        natural-language form ("the week of Monday 20 July 2026") is what
        actually makes a period-specific query retrievable.
        """
        return (f"ISO week {isoweek(wk)}, the week of Monday "
                f"{wk.day} {MONTHS[wk.month]} {wk.year} ({wk} to {week_end(wk)})")

    def partial(end: dt.date) -> str:
        """Flag periods the data does not fully cover yet, so a half-week is
        never read as a real slowdown."""
        return (f" (period still partial — data covers through {max_day})"
                if end > max_day else "")

    out: list[tuple[dict, list]] = []

    def add(ref, content, period, scope, valid, ents, extra=None, end=None):
        if end is not None:
            content += partial(end)
        md = {"type": "activity-rollup", "period": period, "scope": scope}
        md.update(extra or {})
        out.append(({
            "namespace": NS, "content": content, "sourceKind": SOURCE_KIND,
            "sourceRef": ref, "metadata": md, "validAt": valid,
            "importanceHint": 0.55,
        }, ents))

    # ---- 1. weekly per-repo commits -------------------------------------
    rows = q("""
        SELECT date_trunc('week', day)::DATE AS wk, repo_full_name, org_slug,
               count(*) c, count(DISTINCT author_login) a,
               sum(COALESCE(additions,0)) adds, sum(COALESCE(deletions,0)) dels,
               count(DISTINCT day) ndays
        FROM github_commits WHERE day >= $lo GROUP BY 1,2,3
    """, lo=first_monday)
    contrib = defaultdict(list)
    for wk, repo, login, c, a, d in q("""
        SELECT date_trunc('week', day)::DATE, repo_full_name, author_login,
               count(*) c, sum(COALESCE(additions,0)), sum(COALESCE(deletions,0))
        FROM github_commits WHERE day >= $lo GROUP BY 1,2,3 ORDER BY c DESC
    """, lo=first_monday):
        contrib[(wk, repo)].append((login, c, a, d))

    per_week = defaultdict(list)
    for r in rows:
        per_week[r[0]].append(r)
    for wk, rs in per_week.items():
        # name tiebreak keeps the top-N cutoff deterministic across runs:
        # DuckDB group-by order is not stable, so ties at the cap would
        # otherwise leave orphaned rollups behind on a re-run.
        rs.sort(key=lambda r: (-r[3], r[1]))
        for repo, org, c, a, add_, del_, days in [
                (r[1], r[2], r[3], r[4], r[5], r[6], r[7])
                for r in rs[:top] if r[3] >= min_commits]:
            cs = contrib[(wk, repo)]
            names = merge_people([(l, cc) for l, cc, _, _ in cs])
            content = (
                f"In {wl(wk)} the "
                f"{repo} repository ({org}) saw {n(c)} commits from {a} "
                f"contributor(s) on {days} active day(s), +{n(add_)}/-{n(del_)} "
                f"lines (net {n(add_-del_)}). Most active: "
                f"{listing(names)}. "
                f"Weekly GitHub commit activity rollup for {repo}."
            )
            ents = [("repo", repo)] + [("person-name", nm) for nm, _ in names[:4]]
            add(f"rollup:commits:{repo}:{isoweek(wk)}", content, "week",
                f"repo:{repo}", end_ts(week_end(wk)), ents,
                {"repo": repo, "org": org, "week": isoweek(wk), "commits": c},
                end=week_end(wk))

    # ---- 2. weekly per-person commits ------------------------------------
    # Aggregated in Python, not SQL, because one human can commit under several
    # github logins (users.github_usernames is an array) and DuckDB has no
    # knowledge of that mapping.
    prepo = defaultdict(lambda: defaultdict(int))   # (wk, name) -> repo -> commits
    pstat = defaultdict(lambda: [0, 0, 0, set(), set()])  # c, adds, dels, days, logins
    for wk, login, repo, dday, c, a, dl in q("""
        SELECT date_trunc('week', day)::DATE, author_login, repo_full_name, day,
               count(*) c, sum(COALESCE(additions,0)), sum(COALESCE(deletions,0))
        FROM github_commits WHERE day >= $lo GROUP BY 1,2,3,4
    """, lo=first_monday):
        name = person(login)[0]
        k = (wk, name)
        prepo[k][repo] += c
        st = pstat[k]
        st[0] += c
        st[1] += int(a or 0)
        st[2] += int(dl or 0)
        st[3].add(dday)
        st[4].add(login)

    pw = defaultdict(list)
    for (wk, name), st in pstat.items():
        pw[wk].append((name, st))
    for wk, rs in pw.items():
        rs.sort(key=lambda r: (-r[1][0], r[0]))
        for name, (c, add_, del_, days, logins) in [
                r for r in rs[:top] if r[1][0] >= min_commits]:
            uid = None
            for lg in logins:
                uid = uid or person(lg)[1]
            who = name if logins == {name} else \
                f"{name} (github {', '.join(sorted(logins))})"
            rl = sorted(prepo[(wk, name)].items(), key=lambda x: -x[1])
            content = (
                f"In {wl(wk)} {who} "
                f"made {n(c)} commits across {len(rl)} repo(s) on {len(days)} "
                f"active day(s), +{n(add_)}/-{n(del_)} lines. Busiest repos: "
                f"{listing(rl)}. Weekly commit activity rollup for {name}."
            )
            ents = [("person-name", name)] + [("repo", r) for r, _ in rl[:4]]
            if uid:
                ents.append(("person-id", uid))
            add(f"rollup:person-commits:{name}:{isoweek(wk)}", content, "week",
                f"person:{name}", end_ts(week_end(wk)), ents,
                {"person": name, "githubLogins": sorted(logins),
                 "week": isoweek(wk), "commits": c}, end=week_end(wk))

    # ---- 3. weekly per-person Claude usage --------------------------------
    crows = q("""
        SELECT date_trunc('week', c.created_at)::DATE wk,
               CAST(c.user_id AS VARCHAR) uid,
               COALESCE(u.display_name, CAST(c.user_id AS VARCHAR)) nm,
               count(*) e,
               count(*) FILTER (WHERE c.event_type='prompt') prompts,
               count(*) FILTER (WHERE c.event_type='turn')   turns,
               count(DISTINCT c.session_id) sessions,
               count(DISTINCT c.created_at::DATE) ndays,
               sum(COALESCE(c.total_tokens,0)) tok,
               sum(COALESCE(c.input_tokens,0)) inp,
               sum(COALESCE(c.output_tokens,0)) outp,
               sum(COALESCE(c.cache_read_tokens,0)) cached
        FROM claude_activity_events c
        LEFT JOIN dim_users u ON u.id = c.user_id
        WHERE c.created_at::DATE >= $lo
        GROUP BY 1,2,3
    """, lo=first_monday)
    cw = defaultdict(list)
    for r in crows:
        cw[r[0]].append(r)
    for wk, rs in cw.items():
        rs.sort(key=lambda r: (-r[3], r[2]))
        rank = {r[1]: i + 1 for i, r in enumerate(rs)}
        for wkd, uid, nm, e, pr, tu, ses, days, tok, inp, outp, cache in rs:
            if e < 5:
                continue
            content = (
                f"In {wl(wk)} {nm} used "
                f"Claude Code for {n(e)} events ({n(pr)} prompts, {n(tu)} turns) "
                f"across {n(ses)} session(s) on {days} active day(s), burning "
                f"{tokens(tok)} tokens ({tokens(inp)} in, {tokens(outp)} out, "
                f"{tokens(cache)} cache reads). That ranked {nm} #{rank[uid]} of "
                f"{len(rs)} Claude users in the studio that week. Weekly AI / "
                f"Claude Code usage rollup for {nm}."
            )
            add(f"rollup:claude:{uid}:{isoweek(wk)}", content, "week",
                f"person:{nm}", end_ts(week_end(wk)),
                [("person-id", uid), ("person-name", nm)],
                {"person": nm, "userId": uid, "week": isoweek(wk),
                 "claudeEvents": e, "totalTokens": int(tok or 0),
                 "rank": rank[uid], "signal": "claude"}, end=week_end(wk))

    # ---- 4. weekly studio-wide engineering + AI ---------------------------
    for wk, c, ppl, repos, add_, del_ in q("""
        SELECT date_trunc('week', day)::DATE wk, count(*),
               count(DISTINCT author_login), count(DISTINCT repo_full_name),
               sum(COALESCE(additions,0)), sum(COALESCE(deletions,0))
        FROM github_commits WHERE day >= $lo GROUP BY 1 ORDER BY 1
    """, lo=first_monday):
        rs = sorted(per_week[wk], key=lambda r: -r[3])
        top_repos = [(r[1], r[3]) for r in rs[:5]]
        top_ppl = sorted(((nm, st[0]) for (w2, nm), st in pstat.items()
                          if w2 == wk), key=lambda x: -x[1])[:5]
        orgs = sorted(defaultdict_sum((r[2], r[3]) for r in per_week[wk]).items(),
                      key=lambda x: -x[1])[:4]
        cl = q("""SELECT count(*), count(DISTINCT session_id),
                         count(DISTINCT user_id), sum(COALESCE(total_tokens,0))
                  FROM claude_activity_events
                  WHERE date_trunc('week', created_at)::DATE = $w""", w=wk)[0]
        ai = ""
        if cl[0]:
            ai = (f" In the same week the studio ran {n(cl[0])} Claude Code "
                  f"events across {n(cl[1])} sessions by {cl[2]} people, "
                  f"{tokens(cl[3])} tokens.")
        content = (
            f"Studio-wide engineering activity for {wl(wk)}: "
            f"{n(c)} commits by {ppl} people across "
            f"{repos} repositories in the FlowOS GitHub orgs, "
            f"+{n(add_)}/-{n(del_)} lines. Most active people: "
            f"{listing(top_ppl)}. Busiest repos: {listing(top_repos)}. "
            f"Busiest orgs: {listing(orgs)}.{ai} "
            f"Weekly studio activity rollup — who was most active this week."
        )
        ents = ([("person-name", p) for p, _ in top_ppl[:5]]
                + [("repo", r) for r, _ in top_repos[:5]])
        add(f"rollup:commits:studio:{isoweek(wk)}", content, "week", "studio",
            end_ts(week_end(wk)), ents,
            {"week": isoweek(wk), "commits": c, "people": ppl},
            end=week_end(wk))

    # ---- 5. weekly studio-wide Claude -------------------------------------
    for wk, e, ses, act, tok, cache in q("""
        SELECT date_trunc('week', created_at)::DATE wk, count(*),
               count(DISTINCT session_id), count(DISTINCT user_id),
               sum(COALESCE(total_tokens,0)), sum(COALESCE(cache_read_tokens,0))
        FROM claude_activity_events
        WHERE created_at::DATE >= $lo GROUP BY 1 ORDER BY 1
    """, lo=first_monday):
        rs = sorted(cw.get(wk, []), key=lambda r: -r[3])
        tops = [(r[2], r[3]) for r in rs[:5]]
        toptok = sorted(((r[2], r[8]) for r in rs), key=lambda x: -x[1])[:3]
        content = (
            f"Studio-wide Claude Code / AI usage for {wl(wk)}: "
            f"{n(e)} events across {n(ses)} sessions "
            f"by {act} people, {tokens(tok)} total tokens ({tokens(cache)} of "
            f"them cache reads). Heaviest users by events: {listing(tops)}. "
            f"By tokens: " + ", ".join(f"{p} ({tokens(t)})" for p, t in toptok) +
            ". Weekly AI usage rollup — who uses Claude the most."
        )
        add(f"rollup:claude:studio:{isoweek(wk)}", content, "week",
            "studio-ai", end_ts(week_end(wk)),
            [("person-name", p) for p, _ in tops],
            {"week": isoweek(wk), "claudeEvents": e, "signal": "claude"},
            end=week_end(wk))

    # ---- 6. monthly per-org ------------------------------------------------
    for m, org, c, ppl, repos, add_, del_ in q("""
        SELECT date_trunc('month', day)::DATE m, org_slug, count(*),
               count(DISTINCT author_login), count(DISTINCT repo_full_name),
               sum(COALESCE(additions,0)), sum(COALESCE(deletions,0))
        FROM github_commits GROUP BY 1,2 ORDER BY 1,3 DESC
    """):
        tr = q("""SELECT repo_full_name, count(*) c FROM github_commits
                  WHERE org_slug=$o AND date_trunc('month', day)::DATE=$m
                  GROUP BY 1 ORDER BY 2 DESC LIMIT 5""", o=org, m=m)
        tp = q("""SELECT author_login, count(*) c FROM github_commits
                  WHERE org_slug=$o AND date_trunc('month', day)::DATE=$m
                  GROUP BY 1 ORDER BY 2 DESC LIMIT 12""", o=org, m=m)
        tp = merge_people(tp)[:5]
        label = f"{MONTHS[m.month]} {m.year}"
        content = (
            f"In {label} the {org} GitHub org saw {n(c)} commits from {ppl} "
            f"contributor(s) across {repos} repositories, +{n(add_)}/-{n(del_)} "
            f"lines. Busiest repos: {listing(tr)}. Most active people: "
            f"{listing(tp)}. Monthly engineering activity rollup for {org}."
        )
        ents = ([("repo", r) for r, _ in tr]
                + [("person-name", p) for p, _ in tp[:4]])
        add(f"rollup:commits:org:{org}:{m:%Y-%m}", content, "month",
            f"org:{org}", end_ts(month_end(m)), ents,
            {"org": org, "month": f"{m:%Y-%m}", "commits": c},
            end=month_end(m))

    # ---- 7. monthly studio-wide -------------------------------------------
    for m, c, ppl, repos, add_, del_ in q("""
        SELECT date_trunc('month', day)::DATE m, count(*),
               count(DISTINCT author_login), count(DISTINCT repo_full_name),
               sum(COALESCE(additions,0)), sum(COALESCE(deletions,0))
        FROM github_commits GROUP BY 1 ORDER BY 1
    """):
        tp = q("""SELECT author_login, count(*) c FROM github_commits
                  WHERE date_trunc('month', day)::DATE=$m
                  GROUP BY 1 ORDER BY 2 DESC LIMIT 15""", m=m)
        tp = merge_people(tp)[:6]
        tr = q("""SELECT repo_full_name, count(*) c FROM github_commits
                  WHERE date_trunc('month', day)::DATE=$m
                  GROUP BY 1 ORDER BY 2 DESC LIMIT 6""", m=m)
        to = q("""SELECT org_slug, count(*) c FROM github_commits
                  WHERE date_trunc('month', day)::DATE=$m
                  GROUP BY 1 ORDER BY 2 DESC""", m=m)
        cl = q("""SELECT count(*), count(DISTINCT session_id),
                         count(DISTINCT user_id), sum(COALESCE(total_tokens,0))
                  FROM claude_activity_events
                  WHERE date_trunc('month', created_at)::DATE=$m""", m=m)[0]
        pr = q("""SELECT count(*), count(*) FILTER (WHERE merged_at IS NOT NULL)
                  FROM github_pull_requests
                  WHERE date_trunc('month', created_at)::DATE=$m""", m=m)[0]
        iss = q("""SELECT count(*), count(DISTINCT issue_id) FROM issue_activity
                   WHERE date_trunc('month', created_at)::DATE=$m""", m=m)[0]
        label = f"{MONTHS[m.month]} {m.year}"
        ai = (f" Claude Code usage that month: {n(cl[0])} events, {n(cl[1])} "
              f"sessions, {cl[2]} people, {tokens(cl[3])} tokens."
              if cl[0] else " No Claude Code usage was recorded that month.")
        content = (
            f"FlowOS studio activity summary for {label}: {n(c)} commits by "
            f"{ppl} people across {repos} repositories and {len(to)} GitHub "
            f"orgs, +{n(add_)}/-{n(del_)} lines. Most active people: "
            f"{listing(tp)}. Busiest repos: {listing(tr)}. Orgs by commits: "
            f"{listing(to)}. {n(pr[0])} pull requests opened ({n(pr[1])} "
            f"merged); {n(iss[0])} issue activity events on {n(iss[1])} issues."
            f"{ai} Monthly studio-wide activity rollup."
        )
        ents = ([("person-name", p) for p, _ in tp[:5]]
                + [("repo", r) for r, _ in tr[:5]])
        add(f"rollup:studio:{m:%Y-%m}", content, "month", "studio",
            end_ts(month_end(m)), ents,
            {"month": f"{m:%Y-%m}", "commits": c, "people": ppl},
            end=month_end(m))

    return out


def defaultdict_sum(pairs):
    d = defaultdict(int)
    for k, v in pairs:
        d[k] += v
    return d


# --------------------------------------------------------------- ai edges
def used_ai_on(duck, b: Brain, by_type, by_name, apply: bool):
    """person -[USED_AI_ON]-> repo, derived from claude_activity_events.cwd.

    claude_activity_events has NO venture_id or repo column. The ONLY linkage
    available is `cwd`, which is populated on a small minority of events, so the
    edge fact states the coverage explicitly rather than implying full
    attribution.
    """
    import re
    repos = {r[0] for r in duck.execute(
        "SELECT DISTINCT repo_full_name FROM github_commits").fetchall()}
    byname = defaultdict(list)
    for r in repos:
        byname[r.split("/")[-1].lower()].append(r)

    agg = defaultdict(lambda: [0, 0, None])
    rows = duck.execute("""
        SELECT CAST(c.user_id AS VARCHAR), u.display_name, c.cwd, count(*),
               sum(COALESCE(c.total_tokens,0)), max(c.created_at)::DATE::VARCHAR
        FROM claude_activity_events c
        LEFT JOIN dim_users u ON u.id = c.user_id
        WHERE c.cwd IS NOT NULL GROUP BY 1,2,3
    """).fetchall()
    total = duck.execute("SELECT count(*) FROM claude_activity_events").fetchone()[0]
    lo, hi, with_cwd = duck.execute("""
        SELECT min(created_at)::DATE::VARCHAR, max(created_at)::DATE::VARCHAR, count(*)
        FROM claude_activity_events WHERE cwd IS NOT NULL""").fetchone()
    window = (f"cwd is only emitted for {n(with_cwd)} of {n(total)} Claude events "
              f"({100.0*with_cwd/total:.1f}%), all between {lo} and {hi}")
    for uid, name, cwd, ev, tok, last in rows:
        base = re.split(r"[\\/]", cwd.rstrip("/\\"))[-1].lower()
        cand = byname.get(base)
        if not cand or len(cand) != 1:
            continue
        k = (uid, name or uid, cand[0])
        agg[k][0] += ev
        agg[k][1] += int(tok or 0)
        agg[k][2] = max(agg[k][2] or "", last)

    made = updated = skipped = 0
    for (uid, name, repo), (ev, tok, last) in sorted(agg.items(),
                                                     key=lambda x: -x[1][0]):
        src = by_type["person"].get(uid) or by_name.get(name.strip().lower())
        dst = by_type["repo"].get(repo)
        if not src or not dst:
            skipped += 1
            continue
        fact = (f"{name} ran {n(ev)} Claude Code events ({tokens(tok)} tokens) "
                f"with the working directory set to {repo}, last on {last}. "
                f"Derived from claude_activity_events.cwd — {window}. Evidence "
                f"that this person used AI on this repo, NOT a complete or "
                f"comparable measure of how much.")
        if not apply:
            made += 1
            continue
        cur = b.db.run("""SELECT id FROM entity_edges
                          WHERE namespace=:ns AND src_id=:s AND dst_id=:d
                            AND relation='USED_AI_ON' AND valid_to IS NULL""",
                       ns=NS, s=src, d=dst)
        if cur:
            b.db.run("""UPDATE entity_edges SET fact=:f, weight=:w,
                        metadata=:m::jsonb WHERE id=:i""",
                     f=fact, w=float(ev), i=cur[0][0],
                     m=json.dumps({"derived_from": "claude_activity_events.cwd",
                                   "events": ev, "tokens": tok,
                                   "coverage": "cwd-only",
                                   "cwd_window": f"{lo}..{hi}"}))
            updated += 1
        else:
            b.db.run("""INSERT INTO entity_edges
                          (namespace, src_id, dst_id, relation, fact, weight,
                           valid_from, metadata)
                        VALUES (:ns,:s,:d,'USED_AI_ON',:f,:w,:vf,:m::jsonb)
                        ON CONFLICT DO NOTHING""",
                     ns=NS, s=src, d=dst, f=fact, w=float(ev),
                     vf=dt.datetime.fromisoformat(last).replace(
                         tzinfo=dt.timezone.utc),
                     m=json.dumps({"derived_from": "claude_activity_events.cwd",
                                   "events": ev, "tokens": tok,
                                   "coverage": "cwd-only",
                                   "cwd_window": f"{lo}..{hi}"}))
            made += 1
    return made, updated, skipped, len(agg), total, window


def github_login_map() -> dict:
    """github login -> {name, id}, from FlowOS prod `users.github_usernames`.

    One human owns several logins there, so commit counts have to be merged on
    the resolved person, not the login. Read STRICTLY read-only. If FLOWOS_DSN
    is not set, falls back to a cached JSON file at $FLOWOS_GH_MAP; with
    neither, rollups fall back to raw github logins.
    """
    out: dict[str, dict] = {}
    dsn = os.environ.get("FLOWOS_DSN")
    if dsn:
        u = urlparse(dsn)
        c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                          port=u.port or 5432, database=(u.path or "/").lstrip("/"))
        try:
            c.run("BEGIN READ ONLY")
            rows = c.run("""SELECT id, display_name, github_usernames FROM users
                            WHERE github_usernames IS NOT NULL""")
            c.run("ROLLBACK")
        finally:
            c.close()
        for uid, name, logins in rows:
            for lg in logins or []:
                out[lg.lower()] = {"name": name, "id": str(uid)}
        return out
    mapfile = os.environ.get("FLOWOS_GH_MAP")
    if mapfile and os.path.exists(mapfile):
        for uid, v in json.load(open(mapfile)).items():
            for lg in v.get("gh") or []:
                out[lg.lower()] = {"name": v["name"], "id": uid}
    return out


def family(ref: str) -> str:
    p = ref.split(":")
    if p[1] == "commits":
        return {"studio": "weekly-studio-commits",
                "org": "monthly-org-commits"}.get(p[2], "weekly-repo-commits")
    if p[1] == "claude":
        return "weekly-studio-claude" if p[2] == "studio" else "weekly-person-claude"
    if p[1] == "person-commits":
        return "weekly-person-commits"
    return "monthly-studio"


# ------------------------------------------------------------------- main
def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--weeks", type=int, default=16,
                    help="how many trailing weeks of weekly rollups (default 16)")
    ap.add_argument("--top", type=int, default=12,
                    help="top N repos / people per week (default 12)")
    ap.add_argument("--min-commits", type=int, default=3)
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--dry-run", action="store_true",
                    help="build and print the rollups, write nothing")
    ap.add_argument("--limit", type=int, help="only retain the first N (smoke test)")
    ap.add_argument("--state",
                    default=os.environ.get("ROLLUPS_STATE",
                                           os.path.join(REPO_ROOT, "data",
                                                        "rollups-state.json")),
                    help="content-hash file used to skip unchanged rollups")
    ap.add_argument("--force", action="store_true",
                    help="re-retain every rollup even if its text is unchanged")
    args = ap.parse_args()

    duck = duckdb.connect(args.db, read_only=True)

    api = os.environ.get("CABRAIN_API", "https://cabrain.fadymondy.com")
    token = os.environ.get("CABRAIN_TOKEN", "")
    dsn = os.environ.get("CABRAIN_DSN", "")
    if not args.dry_run and not token:
        sys.exit("CABRAIN_TOKEN must be set (see module docstring)")

    gh_map = github_login_map()

    recs = build(duck, gh_map, args.weeks, args.top, args.min_commits)
    kinds = defaultdict(int)
    for r, _ in recs:
        kinds[family(r["sourceRef"])] += 1
    print(f"built {len(recs)} rollups: {dict(kinds)}")

    if args.dry_run:
        for r, e in recs[:6]:
            print(f"\n--- {r['sourceRef']}  validAt={r['validAt']}\n{r['content']}"
                  f"\n    entities: {e}")
        return 0

    b = Brain(api, token, dsn)
    if b.db is None:
        print("no CABRAIN_DSN -> content refresh only "
              "(entity links and USED_AI_ON edges skipped)")
    by_type, by_name = (load_entities(b) if b.db is not None
                        else (defaultdict(dict), {}))

    todo = recs[:args.limit] if args.limit else recs

    # skip rollups whose text is byte-identical to what we last wrote
    hashes = {} if args.force else load_hashes(args.state)
    fresh = {r["sourceRef"]: content_hash(r) for r, _ in todo}
    changed = [(r, e) for r, e in todo if hashes.get(r["sourceRef"]) != fresh[r["sourceRef"]]]
    print(f"{len(changed)} of {len(todo)} rollups changed since the last run"
          f"{' (--force)' if args.force else ''}")
    todo = changed

    ids: list[tuple[str, list]] = []
    written: set[str] = set()

    def work(item):
        rec, ents = item
        mid = b.retain(rec)
        return (mid, ents, rec["sourceRef"]) if mid else None

    with ThreadPoolExecutor(max_workers=args.workers) as ex:
        for i, res in enumerate(ex.map(work, todo), 1):
            if res:
                ids.append((res[0], res[1]))
                written.add(res[2])
            if i % 50 == 0:
                print(f"  retained {i}/{len(todo)} ...", flush=True)
    print(f"retained {len(ids)}/{len(todo)} changed rollups")

    # Record the hash only for rollups that actually landed, so a failed retain
    # is retried next run instead of being skipped forever.
    for ref in written:
        hashes[ref] = fresh[ref]
    save_hashes(args.state, hashes)

    # memory -> entity links (this is what Store.expandEntities traverses)
    links = 0
    for mid, ents in (ids if b.db is not None else []):
        seen = set()
        for kind, key in ents:
            if kind == "repo":
                eid = by_type["repo"].get(key)
            elif kind == "person-id":
                eid = by_type["person"].get(key)
            else:
                eid = by_name.get(str(key).strip().lower())
            if eid and eid not in seen:
                seen.add(eid)
                b.db.run("""INSERT INTO memory_entities (memory_id, entity_id)
                            VALUES (:m::uuid, :e) ON CONFLICT DO NOTHING""",
                         m=mid, e=eid)
                links += 1
    print(f"memory_entities links upserted: {links}")

    if b.db is not None:
        made, upd, skip, pairs, tot, window = used_ai_on(duck, b, by_type, by_name, True)
        print(f"USED_AI_ON edges: {made} new, {upd} updated, {skip} unresolved "
              f"(from {pairs} cwd-derived person/repo pairs; {window})")
    else:
        window = used_ai_on(duck, b, by_type, by_name, False)[5]

    # One memory that states the AI-attribution limit, so a question the data
    # cannot answer gets an honest answer instead of a confident wrong one.
    caveat = (
        "AI / Claude Code usage in FlowOS can be attributed to a PERSON but not "
        "reliably to a venture or repo. claude_activity_events has user_id, "
        f"session_id and token counts but no venture_id and no repo column; {window}. "
        "Per-person weekly AI usage rollups are therefore exact, while "
        "person->repo USED_AI_ON graph edges are cwd-derived evidence from that "
        "short window only and must not be read as a ranking of AI use per "
        "project. Questions like 'which venture consumed the most Claude tokens' "
        "cannot be answered from the current data.")
    mid = b.retain({"namespace": NS, "content": caveat, "sourceKind": SOURCE_KIND,
                    "sourceRef": "rollup:claude:attribution-limits",
                    "metadata": {"type": "activity-rollup", "period": "n/a",
                                 "scope": "meta", "signal": "claude"},
                    "validAt": dt.datetime.now(dt.timezone.utc)
                               .strftime("%Y-%m-%dT%H:%M:%SZ"),
                    "importanceHint": 0.7})
    print(f"attribution-limits memory: {mid}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
