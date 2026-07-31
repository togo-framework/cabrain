#!/usr/bin/env python3
"""
Provenance for the FK-derived flowos graph edges.

Why this exists
---------------
`entity_edges` rows built from FlowOS foreign keys (scripts/flowos-graph-typed.py
and scripts/flowos-graph-expand.py) used to be written with `metadata = {}` and no
`memory_id`, so the question "why does the brain believe this edge?" had no answer
for ~93% of the graph. Only the LLM-extracted edges carried a source_ref.

This module is the single source of truth for *which source record backs which
edge*. Both the creation scripts (stamping at INSERT time) and
scripts/backfill-edge-provenance.py (fixing rows already in the table) import it,
so the two can never drift.

Contract
--------
For every FK-derived edge family we re-run the same query the graph builder ran and
collect, per (src_entity, dst_entity, relation) triple, the set of SOURCE RECORDS it
was derived from. Then:

* exactly ONE source record, and that record has a memory  -> hard link:
  memory_id + episode_id + metadata.source_ref.
* exactly ONE source record but the source table is never ingested as a memory
  (venture_members has no `db:` memory) -> no link is invented; metadata records
  `derived_from` and why it is unlinked.
* MANY source records (an ASSIGNED_TO edge summarising 40 issues, a COMMITS_TO
  edge summarising 900 commits, ATTENDED_WITH from repeated co-attendance)
  -> no single memory exists, so metadata records `derived_from`, `aggregate:true`,
  the source count and a sample of the refs. We never point such an edge at one
  arbitrary member of the aggregate.

Read-only against FlowOS prod (every query runs inside BEGIN READ ONLY).
Credential-free: callers pass live connections; DSNs come from env in the CLIs.
"""
from collections import defaultdict

# ---------------------------------------------------------------------------
# Edge families.
#
#   relation        the entity_edges.relation value
#   src / dst       entity_type of each endpoint (matches entities.entity_type)
#   table           the FlowOS table the edge was derived from
#   ref_kind        memories.source_ref kind, i.e. db:<ref_kind>:<id>.
#                   None  -> that table is never ingested as a memory, so no
#                            single-record link is possible by construction.
#   sql             rows of (src_source_id, dst_source_id, source_record_id)
#   symmetric       relation has no meaningful direction (stored order is arbitrary)
# ---------------------------------------------------------------------------
FAMILIES = [
    dict(relation="BELONGS_TO", src="venture", dst="portfolio",
         table="ventures", ref_kind="venture",
         sql="SELECT id, portfolio_id, id FROM ventures WHERE portfolio_id IS NOT NULL"),

    dict(relation="MEMBER_OF", src="person", dst="venture",
         table="venture_members", ref_kind=None,
         sql="""SELECT member_id, venture_id, id FROM venture_members
                 WHERE member_type::text = 'human'"""),

    dict(relation="WORKS_ON", src="agent", dst="venture",
         table="venture_members", ref_kind=None,
         sql="""SELECT member_id, venture_id, id FROM venture_members
                 WHERE member_type::text = 'agent'"""),

    dict(relation="OWNS", src="person", dst="venture",
         table="venture_responsibilities", ref_kind="responsibility",
         sql="SELECT member_id, venture_id, id FROM venture_responsibilities"),

    dict(relation="ASSIGNED_TO", src="person", dst="venture",
         table="issues", ref_kind="issue",
         sql="""SELECT assignee_id, venture_id, id FROM issues
                 WHERE assignee_id IS NOT NULL AND venture_id IS NOT NULL"""),

    dict(relation="AUTHORED_IN", src="person", dst="venture",
         table="posts", ref_kind="post",
         sql="""SELECT author_id, venture_id, id FROM posts
                 WHERE author_id IS NOT NULL AND venture_id IS NOT NULL"""),

    dict(relation="DEPENDS_ON", src="venture", dst="venture",
         table="venture_dependencies", ref_kind="dependency",
         sql="SELECT from_venture_id, to_venture_id, id FROM venture_dependencies"),

    # bd_deals are ingested keyed by the VENTURE (see flowos-sync.py pipeline_deals:
    # `SELECT d.venture_id, ...` -> db:pipeline:<venture_id>), not by deal id.
    dict(relation="LEADS_BD_FOR", src="person", dst="venture",
         table="bd_deals", ref_kind="pipeline",
         sql="""SELECT owner_user_id, venture_id, venture_id FROM bd_deals
                 WHERE owner_user_id IS NOT NULL"""),

    dict(relation="EXPERT_FOR", src="agent", dst="venture",
         table="harvester_items", ref_kind="harvested",
         sql="""SELECT linked_agent_id, linked_venture_id, id FROM harvester_items
                 WHERE linked_venture_id IS NOT NULL AND linked_agent_id IS NOT NULL"""),

    dict(relation="APPLIES_TO", src="learning", dst="venture",
         table="issue_learnings", ref_kind="learning",
         sql="""SELECT id, venture_id, id FROM issue_learnings
                 WHERE venture_id IS NOT NULL"""),

    dict(relation="RECORDED", src="person", dst="learning",
         table="issue_learnings", ref_kind="learning",
         sql="""SELECT created_by_user_id, id, id FROM issue_learnings
                 WHERE created_by_user_id IS NOT NULL"""),

    dict(relation="PURSUES", src="person", dst="goal",
         table="user_goals", ref_kind="goal",
         sql="SELECT user_id, id, id FROM user_goals WHERE user_id IS NOT NULL"),

    dict(relation="GOAL_FOR", src="goal", dst="venture",
         table="venture_goals", ref_kind="goal",
         sql="SELECT id, venture_id, id FROM venture_goals WHERE venture_id IS NOT NULL"),

    dict(relation="PLANS", src="roadmap", dst="venture",
         table="roadmaps", ref_kind="roadmap",
         sql="SELECT id, venture_id, id FROM roadmaps WHERE venture_id IS NOT NULL"),

    dict(relation="TARGETS", src="okr", dst="venture",
         table="venture_okr_snapshots", ref_kind="okr",
         sql="""SELECT id, venture_id, id FROM venture_okr_snapshots
                 WHERE venture_id IS NOT NULL"""),
    dict(relation="TARGETS", src="okr", dst="venture",
         table="venture_okr_targets", ref_kind="okr",
         sql="""SELECT id, venture_id, id FROM venture_okr_targets
                 WHERE venture_id IS NOT NULL"""),

    dict(relation="PROMOTES", src="campaign", dst="venture",
         table="marketing_campaigns", ref_kind="marketing",
         sql="""SELECT id, venture_id, id FROM marketing_campaigns
                 WHERE venture_id IS NOT NULL"""),
    dict(relation="RUNS_CAMPAIGN", src="person", dst="campaign",
         table="marketing_campaigns", ref_kind="marketing",
         sql="""SELECT owner_id, id, id FROM marketing_campaigns
                 WHERE owner_id IS NOT NULL"""),

    dict(relation="CHANNEL_OF", src="channel", dst="venture",
         table="channels", ref_kind="channel",
         sql="""SELECT id, venture_id, id FROM channels
                 WHERE archived_at IS NULL AND venture_id IS NOT NULL"""),

    dict(relation="ATTENDED", src="person", dst="meeting",
         table="calendar_event_attendees", ref_kind="calendar-event",
         sql="""SELECT user_id, event_id, event_id FROM calendar_event_attendees
                 WHERE user_id IS NOT NULL"""),

    dict(relation="MEETING_ABOUT", src="meeting", dst="venture",
         table="calendar_events", ref_kind="calendar-event",
         sql="SELECT id, venture_id, id FROM calendar_events WHERE venture_id IS NOT NULL"),

    # Co-attendance: derived by pairing attendees of the same event. Direction is
    # arbitrary, and a pair that met repeatedly has many source events.
    dict(relation="ISSUE_IN", src="issue", dst="venture",
         table="issues", ref_kind="issue",
         sql="SELECT id, venture_id, id FROM issues WHERE venture_id IS NOT NULL"),

    dict(relation="DISCUSSED_IN", src="person", dst="issue",
         table="issue_comments", ref_kind="issue-thread",
         sql="""SELECT author_user_id, issue_id, issue_id FROM issue_comments
                 WHERE author_user_id IS NOT NULL AND length(COALESCE(body_md,'')) >= 40"""),

    dict(relation="AGENT_DISCUSSED_IN", src="agent", dst="issue",
         table="issue_comments", ref_kind="issue-thread",
         sql="""SELECT as_agent_id, issue_id, issue_id FROM issue_comments
                 WHERE as_agent_id IS NOT NULL AND length(COALESCE(body_md,'')) >= 40"""),

    # agent_actions rows are never ingested one-per-row (they are rolled up per
    # agent x venture x week), so no single source record can back this edge.
    dict(relation="ACTED_ON", src="agent", dst="venture",
         table="agent_actions", ref_kind=None,
         sql="""SELECT agent_id, venture_id, id FROM agent_actions
                 WHERE venture_id IS NOT NULL"""),

    dict(relation="AUTHORED_PR_IN", src="person", dst="repo",
         table="github_pull_requests", ref_kind=None,
         sql="""SELECT u.id, pr.repo_full_name, pr.id
                  FROM github_pull_requests pr
                  JOIN users u ON lower(pr.author_login) =
                       ANY (SELECT lower(x) FROM unnest(u.github_usernames) x)
                 WHERE pr.author_login IS NOT NULL"""),

    dict(relation="ATTENDED_WITH", src="person", dst="person", symmetric=True,
         table="calendar_event_attendees", ref_kind="calendar-event",
         sql="""SELECT a.user_id, c.user_id, a.event_id
                  FROM calendar_event_attendees a
                  JOIN calendar_event_attendees c
                    ON c.event_id = a.event_id AND a.user_id < c.user_id"""),
]

# COMMITS_TO and REPO_OF need a little Python (login -> user, github_url -> slug),
# so they are built by dedicated helpers below rather than a plain SQL family.


def _rows(src, sql):
    return src.run(sql)


def collect_sources(src, ns="flowos"):
    """(src_source_key, dst_source_key, relation) -> dict of provenance facts.

    Keys are ((entity_type, source_id), (entity_type, source_id), relation).
    Values: {"table":…, "ref_kind":…, "refs": [source_ref, …] (deduped, ordered),
             "n": <number of distinct source records>, "extra": {…}}
    """
    out = {}

    def add(skey, dkey, rel, table, ref_kind, source_id, symmetric=False, extra=None):
        if skey[1] is None or dkey[1] is None:
            return
        k = (skey, dkey, rel)
        e = out.get(k)
        if e is None:
            e = out[k] = {"table": table, "ref_kind": ref_kind, "refs": [],
                          "seen": set(), "extra": dict(extra or {}), "symmetric": symmetric}
        elif extra:
            e["extra"].update(extra)
        sid = str(source_id)
        if sid not in e["seen"]:
            e["seen"].add(sid)
            if ref_kind:
                e["refs"].append(f"db:{ref_kind}:{sid}")
            else:
                e["refs"].append(None)

    for fam in FAMILIES:
        rel, sk, dk = fam["relation"], fam["src"], fam["dst"]
        for a, b, sid in _rows(src, fam["sql"]):
            add((sk, str(a)), (dk, str(b)), rel, fam["table"], fam["ref_kind"], sid,
                symmetric=fam.get("symmetric", False))

    # ---- COMMITS_TO: pure aggregate over github_commits (no per-commit memory) --
    login2user = {}
    for uid, logins in src.run("SELECT id, github_usernames FROM users WHERE github_usernames IS NOT NULL"):
        for lg in (logins or []):
            login2user[str(lg).lower()] = str(uid)
    repos = set()
    for repo, login, n, last in src.run("""SELECT repo_full_name, author_login, count(*), max(day)
                                             FROM github_commits GROUP BY 1,2"""):
        repos.add(repo)
        uid = login2user.get(str(login).lower())
        if not uid:
            continue
        k = (("person", uid), ("repo", str(repo)), "COMMITS_TO")
        out[k] = {"table": "github_commits", "ref_kind": None, "refs": [], "seen": set(),
                  "symmetric": False,
                  "extra": {"commits": int(n), "last_commit_day": str(last),
                            "repo": str(repo), "author_login": str(login)}}
        out[k]["n_override"] = int(n)

    # ---- REPO_OF: derived from ventures.github_url, one venture row per edge ----
    for vid, url in src.run("SELECT id, github_url FROM ventures WHERE github_url IS NOT NULL"):
        slug = str(url).rstrip("/").split("github.com/")[-1]
        if slug in repos:
            add(("repo", slug), ("venture", str(vid)), "REPO_OF", "ventures", "venture", vid)

    for v in out.values():
        v["n"] = v.pop("n_override", len(v["seen"]))
        v.pop("seen", None)
    return out


def _uniq_ref(v):
    refs = [r for r in v["refs"] if r]
    return refs[0] if len(refs) == 1 and v["n"] == 1 else None


def build_plan(src_conn, brain_conn, ns="flowos", sample=8):
    """Resolve provenance into concrete per-edge updates.

    Returns (plan, stats) where plan maps entity_edges.id -> (memory_id, episode_id,
    metadata_patch dict).  Only edges that currently have no memory_id are touched,
    so LLM-extracted provenance is never overwritten.
    """
    b = brain_conn
    ent = {}       # (entity_type, source_id) -> entity uuid
    for eid, et, sid in b.run("""SELECT id, entity_type, metadata->>'source_id'
                                   FROM entities
                                  WHERE namespace = :ns AND metadata ? 'source_id'""", ns=ns):
        ent[(et, str(sid))] = eid

    # newest live memory per source_ref (same precedence as backfill-episodes.py)
    mem = {r[0]: r[1] for r in b.run("""
        SELECT DISTINCT ON (source_ref) source_ref, id::text
          FROM memories
         WHERE namespace = :ns AND source_ref LIKE 'db:%'
         ORDER BY source_ref, (invalid_at IS NULL) DESC, valid_at DESC, ingested_at DESC""", ns=ns)}
    epi = {r[0]: r[1] for r in b.run(
        "SELECT source_ref, id::text FROM episodes WHERE namespace = :ns", ns=ns)}

    # existing edges lacking provenance, keyed by (src,dst,relation)
    edge_ids = defaultdict(list)
    for eid, s, d, rel in b.run("""SELECT id, src_id, dst_id, relation FROM entity_edges
                                    WHERE namespace = :ns AND memory_id IS NULL""", ns=ns):
        edge_ids[(s, d, rel)].append(eid)

    sources = collect_sources(src_conn, ns)

    plan, stats = {}, defaultdict(int)
    per_relation = defaultdict(lambda: defaultdict(int))
    for (skey, dkey, rel), v in sources.items():
        s_ent, d_ent = ent.get(skey), ent.get(dkey)
        if not s_ent or not d_ent:
            stats["no_entity"] += 1
            continue
        ids = list(edge_ids.get((s_ent, d_ent, rel), []))
        if v.get("symmetric"):
            # direction is arbitrary for these, and the builder emitted whichever
            # order the attendee list happened to produce — stamp both.
            ids += edge_ids.get((d_ent, s_ent, rel), [])
        if not ids:
            stats["no_edge"] += 1
            continue

        ref = _uniq_ref(v)
        meta = {"derivation": "fk", "derived_from": v["table"]}
        meta.update(v["extra"])
        mid = eid_ = None
        if ref:
            mid = mem.get(ref)
            eid_ = epi.get(ref)
            if mid:
                meta["source_ref"] = ref
                meta["aggregate"] = False
                kind = "linked"
            else:
                meta["aggregate"] = False
                meta["unlinked_reason"] = f"no memory ingested for {ref}"
                kind = "no_memory"
        elif v["ref_kind"] is None:
            meta["aggregate"] = v["n"] > 1 or v["table"] == "github_commits"
            meta["source_count"] = v["n"]
            meta["unlinked_reason"] = f"{v['table']} is not ingested as memories"
            kind = "aggregate_no_ref_kind" if meta["aggregate"] else "no_ref_kind"
        else:
            meta["aggregate"] = True
            meta["source_count"] = v["n"]
            meta["source_refs"] = [r for r in v["refs"] if r][:sample]
            meta["unlinked_reason"] = f"aggregates {v['n']} rows of {v['table']}"
            kind = "aggregate"

        for eid in ids:
            plan[eid] = (mid, eid_, meta)
            stats[kind] += 1
            per_relation[rel][kind] += 1
    return plan, stats, per_relation


def apply_plan(brain_conn, plan, ns="flowos"):
    """Write the plan. Idempotent: re-running is a no-op once applied."""
    import json
    n_link = n_meta = 0
    for eid, (mid, epid, meta) in plan.items():
        r = brain_conn.run("""
            UPDATE entity_edges
               SET memory_id  = COALESCE(:m::uuid, memory_id),
                   episode_id = COALESCE(:e::uuid, episode_id),
                   metadata   = metadata || :meta::jsonb
             WHERE id = :id AND namespace = :ns
               AND (metadata @> :meta::jsonb) IS NOT TRUE
            RETURNING (:m::uuid IS NOT NULL)""",
            m=mid, e=epid, meta=json.dumps(meta, default=str), id=eid, ns=ns)
        if r:
            n_meta += 1
            if r[0][0]:
                n_link += 1
    return n_link, n_meta
