#!/usr/bin/env python3
"""
Build the TYPED flowos graph — nodes with entity_type and directed
subject->RELATION->object edges — from FlowOS production foreign keys, WITH
PROVENANCE stamped in the same run.

This supersedes the two throwaway builders (flowos_graph2.py / flowos_graph3.py)
that produced the current graph. They inserted edges with `metadata = {}` and no
`memory_id`, so "why does the brain believe this?" was unanswerable for ~93% of
the graph and needed a separate backfill. Here every edge written is immediately
stamped by scripts/flowos_edge_provenance.py:

  * one defensible source record -> memory_id + episode_id + metadata.source_ref
  * an aggregate (many issues / commits / co-attendances, or a source table that
    is never ingested as a memory) -> metadata records `derived_from`,
    `aggregate`, `source_count` and a sample of refs. No link is invented.

Idempotent: nodes upsert on (namespace,name); an edge is written only when no OPEN
(valid_to IS NULL) edge exists for the same (src,dst,relation) triple — the unique
key includes valid_from, so a plain upsert would create a duplicate live edge on
every run. Read-only against FlowOS prod.

Environment (never hard-code credentials — this file is committed):
  FLOWOS_DSN         postgresql://flowos:***@<tunnel>:15433/onestudio_hub  (READ ONLY)
  CABRAIN_DSN        postgresql://cabrain:***@<host>:5432/cabrain
  CABRAIN_NAMESPACE  default: flowos
"""
import os
import re
import sys
from collections import defaultdict
from urllib.parse import urlparse

import pg8000.native as pg

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from flowos_edge_provenance import build_plan, apply_plan  # noqa: E402

NS = os.environ.get("CABRAIN_NAMESPACE", "flowos")


def _conn(dsn):
    u = urlparse(dsn)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=u.path.lstrip("/"))


def clean(x, n=90):
    return re.sub(r"\s+", " ", str(x or "")).strip()[:n] or "(untitled)"


ENTITY_TYPES = [
    ("venture", "A product/company in the OneStudio portfolio"),
    ("person", "A human member of the studio"),
    ("portfolio", "A grouping of ventures"),
    ("agent", "A domain-expert AI agent"),
    ("repo", "A source repository"),
    ("learning", "A captured lesson / postmortem insight"),
    ("goal", "A personal or venture goal"),
    ("roadmap", "A period plan for a venture"),
    ("okr", "An OKR snapshot or target"),
    ("campaign", "A marketing campaign"),
    ("channel", "A discussion channel"),
    ("meeting", "A calendar event or recorded meeting"),
]
EDGE_TYPES = [
    ("BELONGS_TO", "venture is part of a portfolio", "venture", "portfolio"),
    ("MEMBER_OF", "person is a member of a venture", "person", "venture"),
    ("OWNS", "person owns a responsibility in a venture", "person", "venture"),
    ("ASSIGNED_TO", "person is assigned work on a venture", "person", "venture"),
    ("AUTHORED_IN", "person authored content in a venture", "person", "venture"),
    ("DEPENDS_ON", "venture depends on another venture", "venture", "venture"),
    ("LEADS_BD_FOR", "person owns the BD pipeline of a venture", "person", "venture"),
    ("EXPERT_FOR", "agent is the domain expert for a venture", "agent", "venture"),
    ("WORKS_ON", "agent is a member of a venture team", "agent", "venture"),
    ("COMMITS_TO", "person commits code to a repo", "person", "repo"),
    ("REPO_OF", "repo is the codebase of a venture", "repo", "venture"),
    ("APPLIES_TO", "learning applies to a venture", "learning", "venture"),
    ("RECORDED", "person recorded a learning", "person", "learning"),
    ("PURSUES", "person pursues a goal", "person", "goal"),
    ("GOAL_FOR", "goal targets a venture", "goal", "venture"),
    ("PLANS", "roadmap plans a venture period", "roadmap", "venture"),
    ("TARGETS", "OKR targets a venture", "okr", "venture"),
    ("PROMOTES", "campaign promotes a venture", "campaign", "venture"),
    ("RUNS_CAMPAIGN", "person owns a marketing campaign", "person", "campaign"),
    ("CHANNEL_OF", "channel belongs to a venture", "channel", "venture"),
    ("ATTENDED", "person attended a meeting", "person", "meeting"),
    ("MEETING_ABOUT", "meeting concerns a venture", "meeting", "venture"),
    ("ATTENDED_WITH", "two people were in the same meeting", "person", "person"),
]


def main():
    s, b = _conn(os.environ["FLOWOS_DSN"]), _conn(os.environ["CABRAIN_DSN"])
    s.run("BEGIN READ ONLY")
    b.run("SET search_path = public")
    b.run("SET statement_timeout = 0")

    for n_, d_ in ENTITY_TYPES:
        b.run("""INSERT INTO entity_types (namespace,name,description) VALUES (:ns,:n,:d)
                 ON CONFLICT (namespace,name) DO UPDATE SET description=EXCLUDED.description""",
              ns=NS, n=n_, d=d_)
    for n_, d_, st, dt in EDGE_TYPES:
        b.run("""INSERT INTO edge_types (namespace,name,description,src_type,dst_type)
                 VALUES (:ns,:n,:d,:st,:dt)
                 ON CONFLICT (namespace,name) DO UPDATE SET description=EXCLUDED.description,
                   src_type=EXCLUDED.src_type, dst_type=EXCLUDED.dst_type""",
              ns=NS, n=n_, d=d_, st=st, dt=dt)

    # ---- core nouns -----------------------------------------------------------
    ventures = {r[0]: r[1] for r in s.run("SELECT id, name FROM ventures")}
    people = {r[0]: (r[1] or r[2]) for r in
              s.run("SELECT id, display_name, email FROM users WHERE deleted_at IS NULL")}
    portfolios = {r[0]: r[1] for r in s.run("SELECT id, initcap(name) FROM portfolios")}
    agents = {r[0]: r[1] for r in s.run("SELECT id, name FROM agents")}

    label, kinds = {}, {}
    for kind, tbl in (("venture", ventures), ("person", people),
                      ("portfolio", portfolios), ("agent", agents)):
        for k, v in tbl.items():
            label[(kind, str(k))] = (v or str(k)).strip()
            kinds[(kind, str(k))] = kind
    dupes = defaultdict(list)
    for key, nm_ in label.items():
        dupes[nm_].append(key)
    for nm_, keys in dupes.items():
        if len(keys) > 1:                        # same display name across types
            for kind, k in keys:
                label[(kind, k)] = f"{nm_} ({kind})"

    # Two source rows of the SAME type sharing a display name still collide on
    # entities' UNIQUE(namespace,name) — they would silently become ONE node and
    # every edge of the second row would be attributed to the first venture.
    # (Real case: ventures `studio-compliance-kit` and `pdpl-starter-kit` are both
    # named "Seed-Stage PDPL Starter Kit".) Skip the extras rather than write a
    # conflated edge; they need a disambiguating rename in FlowOS.
    # The winner must be whichever source_id the node in the brain ALREADY claims,
    # otherwise a re-run flips the node's identity and re-attributes its edges.
    already = {(et, nm2): str(sid) for nm2, et, sid in b.run(
        """SELECT name, entity_type, metadata->>'source_id' FROM entities
            WHERE namespace=:ns AND metadata ? 'source_id'""", ns=NS)}
    claimed, ambiguous = {}, []
    for key in sorted(label, key=lambda k: (already.get((kinds[k], label[k])) != k[1], k)):
        nk = (kinds[key], label[key])
        if nk in claimed:
            ambiguous.append((key, claimed[nk]))
        else:
            claimed[nk] = key
    for key, winner in ambiguous:
        del label[key]
    if ambiguous:
        print(f"  WARNING: {len(ambiguous)} source rows share a display name with another row of "
              f"the same type and were SKIPPED (would conflate two nodes):")
        for key, winner in ambiguous:
            print(f"     {key} collides with {winner} on name {label.get(winner)!r}")

    # Preload EVERY typed node already in the brain, keyed by (entity_type,
    # source_id). Without this, node() below sees the existing display name in
    # `taken`, renames to "<label> [<kind>]" and inserts a DUPLICATE node — which
    # then duplicates every edge hanging off it.
    ent, name_of = {}, {}
    for eid, nm_, et, meta in b.run("""SELECT id, name, entity_type, metadata FROM entities
                                        WHERE namespace=:ns AND metadata ? 'source_id'""", ns=NS):
        ent[(et, str((meta or {}).get("source_id")))] = eid
        name_of[(et, str((meta or {}).get("source_id")))] = nm_

    for key, nm_ in label.items():
        # NEVER clobber an existing node's metadata: source_id is that node's
        # identity, and rewriting it silently re-attributes every edge it carries.
        r = b.run("""INSERT INTO entities (namespace,name,entity_type,metadata)
                     VALUES (:ns,:n,:t, jsonb_build_object('source_id', :sid::text))
                     ON CONFLICT (namespace,name) DO UPDATE
                       SET entity_type = EXCLUDED.entity_type,
                           metadata = entities.metadata ||
                             (CASE WHEN entities.metadata ? 'source_id'
                                   THEN '{}'::jsonb ELSE EXCLUDED.metadata END)
                     RETURNING id""", ns=NS, n=nm_, t=kinds[key], sid=str(key[1]))
        ent[key] = r[0][0]
        name_of[key] = nm_

    taken = {r[0] for r in b.run("SELECT name FROM entities WHERE namespace=:ns", ns=NS)}

    def node(kind, sid, lbl):
        key = (kind, str(sid))
        if key in ent:
            return ent[key]
        nm_ = lbl
        if nm_ in taken:
            nm_, i = f"{lbl} [{kind}]", 2
            while nm_ in taken:
                nm_ = f"{lbl} [{kind} {i}]"
                i += 1
        r = b.run("""INSERT INTO entities (namespace,name,entity_type,metadata)
                     VALUES (:ns,:n,:t, jsonb_build_object('source_id', :sid::text))
                     ON CONFLICT (namespace,name) DO UPDATE SET entity_type=EXCLUDED.entity_type
                     RETURNING id""", ns=NS, n=nm_, t=kind, sid=str(sid))
        taken.add(nm_)
        ent[key] = r[0][0]
        name_of[key] = nm_
        return ent[key]

    edges = []

    def E(sk, dk, rel, fact, vf=None):
        sk, dk = (sk[0], str(sk[1])), (dk[0], str(dk[1]))
        if sk in ent and dk in ent and ent[sk] != ent[dk]:
            edges.append((ent[sk], ent[dk], rel, fact, vf))

    def nm(k, i):
        return name_of.get((k, str(i)), str(i))

    # ---- structural relations between the nouns -------------------------------
    for vid, pid in s.run("SELECT id, portfolio_id FROM ventures WHERE portfolio_id IS NOT NULL"):
        E(("venture", vid), ("portfolio", pid), "BELONGS_TO",
          f"{nm('venture', vid)} belongs to the {nm('portfolio', pid)} portfolio")
    for vid, mid, role, at in s.run("""SELECT venture_id, member_id, COALESCE(role::text,''), created_at
                                         FROM venture_members WHERE member_type::text='human'"""):
        E(("person", mid), ("venture", vid), "MEMBER_OF",
          f"{nm('person', mid)} is a member of {nm('venture', vid)}" + (f" as {role}" if role else ""), at)
    for vid, mid, at in s.run("""SELECT venture_id, member_id, created_at
                                   FROM venture_members WHERE member_type::text='agent'"""):
        E(("agent", mid), ("venture", vid), "WORKS_ON",
          f"{nm('agent', mid)} works on {nm('venture', vid)}", at)
    for vid, mid, owns, at in s.run("""SELECT venture_id, member_id, COALESCE(owns,''), created_at
                                         FROM venture_responsibilities"""):
        E(("person", mid), ("venture", vid), "OWNS",
          f"{nm('person', mid)} owns {owns or 'a responsibility'} in {nm('venture', vid)}", at)
    for vid, aid, at in s.run("""SELECT venture_id, assignee_id, created_at FROM issues
                                  WHERE assignee_id IS NOT NULL AND venture_id IS NOT NULL"""):
        E(("person", aid), ("venture", vid), "ASSIGNED_TO",
          f"{nm('person', aid)} is assigned issues in {nm('venture', vid)}", at)
    for vid, aid, at in s.run("""SELECT venture_id, author_id, created_at FROM posts
                                  WHERE author_id IS NOT NULL AND venture_id IS NOT NULL"""):
        E(("person", aid), ("venture", vid), "AUTHORED_IN",
          f"{nm('person', aid)} posts in {nm('venture', vid)}", at)
    for a2, b2, rel, at in s.run("""SELECT from_venture_id, to_venture_id,
                                            COALESCE(relation::text,'depends_on'), created_at
                                       FROM venture_dependencies"""):
        E(("venture", a2), ("venture", b2), "DEPENDS_ON",
          f"{nm('venture', a2)} {rel} {nm('venture', b2)}", at)
    for vid, oid in s.run("SELECT venture_id, owner_user_id FROM bd_deals WHERE owner_user_id IS NOT NULL"):
        E(("person", oid), ("venture", vid), "LEADS_BD_FOR",
          f"{nm('person', oid)} leads BD for {nm('venture', vid)}")
    for vid, aid in s.run("""SELECT linked_venture_id, linked_agent_id FROM harvester_items
                              WHERE linked_venture_id IS NOT NULL AND linked_agent_id IS NOT NULL"""):
        E(("agent", aid), ("venture", vid), "EXPERT_FOR",
          f"{nm('agent', aid)} is the domain expert used by {nm('venture', vid)}")

    # ---- github: repos + who commits to them (aggregate over github_commits) ---
    repo_rows = s.run("""SELECT repo_full_name, author_login, count(*), max(day)
                           FROM github_commits GROUP BY 1,2""")
    login2user = {}
    for uid, logins in s.run("SELECT id, github_usernames FROM users WHERE github_usernames IS NOT NULL"):
        for lg in (logins or []):
            login2user[str(lg).lower()] = str(uid)
    repos = {r[0] for r in repo_rows}
    for rp in repos:
        node("repo", rp, rp)
    for rp, login, n_, last in repo_rows:
        uid = login2user.get(str(login).lower())
        if uid:
            E(("person", uid), ("repo", rp), "COMMITS_TO",
              f"{nm('person', uid)} has {n_} commit(s) in {rp} (last {last})", last)
    for vid, url in s.run("SELECT id, github_url FROM ventures WHERE github_url IS NOT NULL"):
        slug = str(url).rstrip("/").split("github.com/")[-1]
        if slug in repos:
            E(("repo", slug), ("venture", vid), "REPO_OF", f"{slug} is the codebase of {nm('venture', vid)}")

    # ---- claude usage rolled onto the person node -----------------------------
    for uid, n_, _last in s.run("""SELECT user_id, count(*), max(created_at) FROM claude_activity_events
                                    WHERE user_id IS NOT NULL GROUP BY 1"""):
        key = ("person", str(uid))
        if key in ent:
            b.run("""UPDATE entities SET metadata = metadata || jsonb_build_object('claudeEvents', :n::text)
                     WHERE id=:id""", n=str(n_), id=ent[key])

    # ---- learnings / goals / roadmaps / OKRs / marketing / channels ------------
    for lid, vid, title, uid, at in s.run("""SELECT id, venture_id, title, created_by_user_id, created_at
                                               FROM issue_learnings"""):
        node("learning", lid, clean(title))
        if vid:
            E(("learning", lid), ("venture", vid), "APPLIES_TO",
              f"Learning '{clean(title,60)}' applies to {nm('venture', vid)}", at)
        if uid:
            E(("person", uid), ("learning", lid), "RECORDED",
              f"{nm('person', uid)} recorded '{clean(title,60)}'", at)
    for gid, uid, title, at in s.run("SELECT id, user_id, title, COALESCE(set_at,created_at) FROM user_goals"):
        node("goal", gid, clean(title))
        if uid:
            E(("person", uid), ("goal", gid), "PURSUES", f"{nm('person', uid)} pursues '{clean(title,60)}'", at)
    for gid, vid, title, at in s.run("SELECT id, venture_id, title, COALESCE(set_at,created_at) FROM venture_goals"):
        node("goal", gid, clean(title))
        if vid:
            E(("goal", gid), ("venture", vid), "GOAL_FOR",
              f"Goal '{clean(title,60)}' targets {nm('venture', vid)}", at)
    for rid, vid, cad, ps, at in s.run("""SELECT id, venture_id, COALESCE(cadence::text,'plan'),
                                                 period_start, created_at FROM roadmaps"""):
        lbl = f"{cad} roadmap {ps} · {nm('venture', vid)}" if vid else f"{cad} roadmap {ps}"
        node("roadmap", rid, clean(lbl))
        if vid:
            E(("roadmap", rid), ("venture", vid), "PLANS", f"{clean(lbl,70)} plans {nm('venture', vid)}", at)
    for oid, vid, lab, at in s.run("""SELECT id, venture_id, COALESCE(period_label,'period'), period_end
                                        FROM venture_okr_snapshots"""):
        node("okr", oid, clean(f"OKR {lab} · {nm('venture', vid)}"))
        if vid:
            E(("okr", oid), ("venture", vid), "TARGETS", f"OKR {clean(lab,40)} targets {nm('venture', vid)}", at)
    for oid, vid, qtr, at in s.run("SELECT id, venture_id, COALESCE(quarter,'Q'), created_at FROM venture_okr_targets"):
        node("okr", oid, clean(f"OKR target {qtr} · {nm('venture', vid)}"))
        if vid:
            E(("okr", oid), ("venture", vid), "TARGETS", f"OKR target {qtr} for {nm('venture', vid)}", at)
    for cid, vid, name, oid, at in s.run("SELECT id, venture_id, name, owner_id, created_at FROM marketing_campaigns"):
        node("campaign", cid, clean(name))
        if vid:
            E(("campaign", cid), ("venture", vid), "PROMOTES",
              f"Campaign '{clean(name,50)}' promotes {nm('venture', vid)}", at)
        if oid:
            E(("person", oid), ("campaign", cid), "RUNS_CAMPAIGN", f"{nm('person', oid)} runs '{clean(name,50)}'", at)
    for cid, slug, vid, at in s.run("""SELECT id, COALESCE(slug,name), venture_id, created_at
                                         FROM channels WHERE archived_at IS NULL"""):
        node("channel", cid, clean("#" + str(slug)))
        if vid:
            E(("channel", cid), ("venture", vid), "CHANNEL_OF",
              f"#{clean(slug,40)} is a channel of {nm('venture', vid)}", at)

    # ---- meetings + co-attendance --------------------------------------------
    att = defaultdict(list)
    for ev, uid in s.run("SELECT event_id, user_id FROM calendar_event_attendees WHERE user_id IS NOT NULL"):
        att[str(ev)].append(str(uid))
    for eid, title, vid, st in s.run("SELECT id, title, venture_id, start_at FROM calendar_events"):
        node("meeting", eid, clean(f"{clean(title,60)} · {str(st)[:10] if st else ''}"))
        if vid:
            E(("meeting", eid), ("venture", vid), "MEETING_ABOUT",
              f"Meeting '{clean(title,50)}' concerns {nm('venture', vid)}", st)
        who = att.get(str(eid), [])
        for uid in who:
            E(("person", uid), ("meeting", eid), "ATTENDED", f"{nm('person', uid)} attended '{clean(title,50)}'", st)
        for i in range(len(who)):
            for j in range(i + 1, len(who)):
                E(("person", who[i]), ("person", who[j]), "ATTENDED_WITH",
                  f"{nm('person', who[i])} and {nm('person', who[j])} were both in '{clean(title,40)}'", st)

    # ---- write, skipping any already-open triple ------------------------------
    seen, written = set(), 0
    for src_id, dst_id, rel, fact, vf in edges:
        k = (src_id, dst_id, rel)
        if k in seen:
            continue
        seen.add(k)
        if b.run("""SELECT 1 FROM entity_edges WHERE namespace=:ns AND src_id=:s AND dst_id=:d
                      AND relation=:r AND valid_to IS NULL LIMIT 1""", ns=NS, s=src_id, d=dst_id, r=rel):
            continue
        b.run("""INSERT INTO entity_edges (namespace,src_id,dst_id,relation,fact,valid_from)
                 VALUES (:ns,:s,:d,:r,:f, COALESCE(:vf::timestamptz, now()))
                 ON CONFLICT (namespace,src_id,dst_id,relation,valid_from) DO NOTHING""",
              ns=NS, s=src_id, d=dst_id, r=rel, f=fact, vf=vf.isoformat() if vf else None)
        written += 1
    print(f"  nodes: {len(ent)}   new edges written: {written}")

    # ---- provenance, in the SAME run (no separate backfill needed) ------------
    plan, stats, _ = build_plan(s, b, NS)
    s.run("ROLLBACK")
    n_link, n_meta = apply_plan(b, plan, NS)
    print(f"  provenance stamped: {n_meta} edges updated, {n_link} hard-linked to a memory")
    print("  " + ", ".join(f"{k}={v}" for k, v in sorted(stats.items())))

    for lbl, sql in [("edges", "SELECT count(*) FROM entity_edges WHERE namespace=:ns"),
                     ("… with memory_id",
                      "SELECT count(*) FROM entity_edges WHERE namespace=:ns AND memory_id IS NOT NULL"),
                     ("… with provenance metadata",
                      "SELECT count(*) FROM entity_edges WHERE namespace=:ns AND metadata ? 'derived_from'")]:
        print(f"  {lbl}: {b.run(sql, ns=NS)[0][0]}")


if __name__ == "__main__":
    main()
