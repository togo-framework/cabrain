#!/usr/bin/env python3
"""
Per-person weekly PRODUCTIVITY roll-ups: git output + Claude/AI usage + cost, in
ONE memory per person per ISO week.

Why this exists
---------------
The brain already had git activity and Claude activity as SEPARATE roll-ups, and
neither carried cost. So "how proactive is this person — what did they ship, how
much AI did they burn doing it, and what did that cost?" could not be answered
without joining two memories by hand, and the cost half did not exist at all.

Cost model
----------
claude_activity_events has token counts but NO cost column and NO model column, so
cost here is an ESTIMATE, not billing truth. Rates are per million tokens and are
overridable from the environment; the defaults are Sonnet-class list prices:

    PRICE_INPUT_PER_MTOK        3.00
    PRICE_OUTPUT_PER_MTOK      15.00
    PRICE_CACHE_READ_PER_MTOK   0.30
    PRICE_CACHE_WRITE_PER_MTOK  3.75

Getting the cache-read rate right matters more than anything else here: 97% of all
tokens in this dataset are cache reads, so pricing them at the input rate would
overstate spend by roughly an order of magnitude. Every memory states the rates it
used and that the figure is an estimate, so a reader can never mistake it for an
invoice.

Attribution caveat, carried deliberately into the text: Claude usage attributes to
a PERSON reliably, but not to a venture or repo — cwd is present on only 2% of
events. Do not let a reader infer per-project AI spend from this.

Environment:
  FLOWOS_DSN     postgresql://…/onestudio_hub   (READ-ONLY; reads run in a txn)
  CABRAIN_TOKEN  brain API token
  CABRAIN_API_URL (default https://cabrain.fadymondy.com)
  WEEKS          how many ISO weeks back to emit   (default 8)
"""
import json
import os
import subprocess
import sys
from urllib.parse import urlparse

import pg8000.native as pg

API = os.environ.get("CABRAIN_API_URL", "https://cabrain.fadymondy.com").rstrip("/")
TOK = os.environ["CABRAIN_TOKEN"]
NS = os.environ.get("FLOWOS_NAMESPACE", "flowos")
WEEKS = int(os.environ.get("WEEKS", "8"))

P_IN = float(os.environ.get("PRICE_INPUT_PER_MTOK", "3.00"))
P_OUT = float(os.environ.get("PRICE_OUTPUT_PER_MTOK", "15.00"))
P_CR = float(os.environ.get("PRICE_CACHE_READ_PER_MTOK", "0.30"))
P_CW = float(os.environ.get("PRICE_CACHE_WRITE_PER_MTOK", "3.75"))


def cost(inp, out, cr, cw):
    return (inp * P_IN + out * P_OUT + cr * P_CR + cw * P_CW) / 1_000_000


def m(n):
    """Human-scale a token count so the sentence stays readable."""
    n = n or 0
    if n >= 1_000_000_000:
        return f"{n/1_000_000_000:.2f}B"
    if n >= 1_000_000:
        return f"{n/1_000_000:.1f}M"
    if n >= 1_000:
        return f"{n/1_000:.1f}k"
    return str(n)


def retain(body, ref, valid_at, importance=0.6):
    payload = {"namespace": NS, "content": body, "sourceKind": "flowos_productivity_rollup",
               "sourceRef": ref, "metadata": {"type": "productivity"},
               "validAt": valid_at, "importanceHint": importance}
    r = subprocess.run(["curl", "-s", "-m", "90", "-X", "POST", f"{API}/api/brain/retain",
                        "-H", f"X-Cabrain-Token: {TOK}", "-H", "Content-Type: application/json",
                        "-d", json.dumps(payload)], capture_output=True, text=True)
    try:
        return json.loads(r.stdout).get("decision", "?")
    except Exception:
        return f"ERR {r.stdout[:60]}"


def main():
    u = urlparse(os.environ["FLOWOS_DSN"])
    s = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=u.path.lstrip("/"))
    s.run("BEGIN READ ONLY")

    # Git side, keyed by ISO week. github_commits.author_login is a GitHub handle;
    # users.github_usernames is a text[] of the handles a person owns, so the join
    # has to go through unnest — matching on display_name would silently drop
    # everyone whose handle differs from their name, which is most of them.
    git = s.run("""
      SELECT COALESCE(u.display_name, c.author_login) AS person,
             to_char(c.authored_at,'IYYY-"W"IW')      AS wk,
             count(*)                                  AS commits,
             count(DISTINCT c.repo_full_name)          AS repos,
             count(DISTINCT c.day)                     AS active_days,
             COALESCE(sum(c.additions),0)              AS adds,
             COALESCE(sum(c.deletions),0)              AS dels,
             max(c.authored_at)                        AS last_at
      FROM github_commits c
      LEFT JOIN users u ON u.deleted_at IS NULL
             AND lower(c.author_login) = ANY (SELECT lower(g) FROM unnest(u.github_usernames) g)
      WHERE c.authored_at >= now() - make_interval(weeks => :w)
      GROUP BY 1,2""", w=WEEKS)

    ai = s.run("""
      SELECT COALESCE(u.display_name, e.user_id::text) AS person,
             to_char(e.created_at,'IYYY-"W"IW')        AS wk,
             count(*)                                   AS events,
             count(DISTINCT e.session_id)               AS sessions,
             COALESCE(sum(e.input_tokens),0)            AS inp,
             COALESCE(sum(e.output_tokens),0)           AS outp,
             COALESCE(sum(e.cache_read_tokens),0)       AS cr,
             COALESCE(sum(e.cache_creation_tokens),0)   AS cw,
             COALESCE(sum(e.total_tokens),0)            AS tot
      FROM claude_activity_events e
      LEFT JOIN users u ON u.id = e.user_id
      WHERE e.created_at >= now() - make_interval(weeks => :w) AND e.user_id IS NOT NULL
      GROUP BY 1,2""", w=WEEKS)

    # week -> the last day covered, so valid_at is never stamped into the future.
    # The activity roll-ups used the period END, which for the current partial week
    # is a future date, and every recall bounded by until=now then excluded the
    # newest week — the exact failure that made "last week" look empty.
    wk_end = {r[0]: r[1] for r in s.run("""
      SELECT to_char(d,'IYYY-"W"IW'), max(d)::text FROM (
        SELECT generate_series(now() - make_interval(weeks => :w), now(), '1 day')::date d
      ) x GROUP BY 1""", w=WEEKS)}
    s.run("ROLLBACK")

    gi = {(r[0], r[1]): r for r in git}
    ax = {(r[0], r[1]): r for r in ai}
    keys = sorted(set(gi) | set(ax), key=lambda k: (k[1], k[0]))

    rates = (f"Cost is an ESTIMATE from token counts at ${P_IN:.2f}/${P_OUT:.2f} per MTok "
             f"input/output and ${P_CR:.2f}/${P_CW:.2f} per MTok cache read/write; "
             f"claude_activity_events records no cost and no model, so this is not billing truth.")

    written, spend = 0, {}
    for person, wk in keys:
        g, a = gi.get((person, wk)), ax.get((person, wk))
        end = wk_end.get(wk)
        if not end:
            continue
        parts = [f"Productivity of {person} in ISO week {wk} (through {end})."]
        if g:
            parts.append(f"Git: {g[2]} commit(s) across {g[3]} repo(s) on {g[4]} active day(s), "
                         f"+{g[5]:,}/-{g[6]:,} lines.")
        else:
            parts.append("Git: no commits this week.")
        if a:
            c = cost(a[4], a[5], a[6], a[7])
            spend[wk] = spend.get(wk, 0) + c
            parts.append(f"AI (Claude Code): {a[2]} event(s) in {a[3]} session(s), {m(a[8])} total tokens "
                         f"({m(a[4])} input, {m(a[5])} output, {m(a[6])} cache read, {m(a[7])} cache write). "
                         f"Estimated cost ${c:,.2f}.")
            if g and g[2]:
                parts.append(f"Ratio: ${c/g[2]:,.2f} of AI per commit, {a[8]//max(1,g[2]):,} tokens per commit.")
        else:
            parts.append("AI (Claude Code): no recorded usage this week.")
        parts.append(rates)
        parts.append("Claude usage attributes to a person, NOT to a venture or repo "
                     "(cwd is present on only 2% of events), so do not read per-project AI spend from this.")
        ref = f"rollup:productivity:{person}:{wk}"
        d = retain(" ".join(parts), ref, f"{end}T23:59:00Z")
        written += 1
        if written % 25 == 0:
            print(f"  {written}/{len(keys)} …", flush=True)

    # Studio-wide weekly spend, so "what is AI costing us" is one recall away.
    for wk, c in sorted(spend.items()):
        end = wk_end.get(wk)
        people = len([1 for p, w in ax if w == wk])
        retain(f"Studio-wide Claude Code / AI spend for ISO week {wk} (through {end}): "
               f"estimated ${c:,.2f} across {people} people. {rates}",
               f"rollup:ai-spend:studio:{wk}", f"{end}T23:59:00Z", 0.75)
        written += 1

    print(f"\nwrote {written} memories over {len(keys)} person-weeks; "
          f"weekly studio AI spend: " + ", ".join(f"{w} ${c:,.0f}" for w, c in sorted(spend.items())))
    return 0


if __name__ == "__main__":
    sys.exit(main())
