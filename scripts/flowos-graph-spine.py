#!/usr/bin/env python3
"""
Close the remaining holes in the flowos venture spine.

The pipeline the studio actually wants to walk is

    portfolio -> ventures -> repos -> members -> activity[claude/github]
              -> feeds -> goals -> roadmap -> code base -> docs

Most of it was already there (scripts/flowos-graph-edges.py builds the FK spine,
scripts/flowos-graph-repo-links.py hangs the repo island off ventures and
portfolios, scripts/code-index.py hangs code_module/doc off repos). Three hops
were missing entirely and one was 81% dead:

  ACTIVITY  470 `flowos_activity_rollup` memories existed (weekly per-repo,
            weekly per-person, weekly Claude, monthly per-org, studio-wide) but
            there was no `activity` entity and no edge, so "what did this member
            do last week" or "how busy is this repo" was not walkable — only
            searchable. We mint ONE activity entity per (subject, period) and
            hang it off the person(s) and repo(s) the rollup already names.

  FEEDS     1,264 posts existed as memories, and person -AUTHORED_IN-> venture
            existed as an aggregate, but "the feed of venture X" was not a node.
            We deliberately do NOT mint 1,264 post entities: the answer to "show
            me the feed" is a bounded stream, so one `feed` node per venture (54)
            plus a studio feed, with every post memory attached to it via
            memory_entities, answers it at 1/23rd of the node cost.

  MEETINGS  183 of 224 meetings had no live edge at all. Only 41 events have
            attendee rows and only 31 carry a venture_id — but ALL 224 have an
            owner_id, which was simply never used. person -ORGANIZED-> meeting
            connects every one of them.

  ONTOLOGY  entity_types/edge_types are what the Graph Explorer lists (see
            OntologyHandler in plugins/brain/internal/brain/graph.go). `doc`
            (420 entities), `code_module` (222) and the DOC_IN / CODE_IN /
            REPO_IN / DISCUSSED_IN relations (861 live edges) were never
            registered by the builders that created them, so they were invisible
            in that view. Rather than patch each builder, the last step here
            RECONCILES the registry against reality: anything present in
            `entities`/`entity_edges` and missing from the registry is
            registered. That makes the bug self-healing whoever causes it next.

Why an activity ENTITY per (subject, period) and not per subject
---------------------------------------------------------------
A single "activity of X" node would force every temporal question through
memory metadata filtering. One node per (subject, period) puts the period in the
graph itself — the edge's valid_from is the period end — so "Sentra's activity
last week" is a plain edge scan with a time predicate, which is what the temporal
edge model is for. It costs 470 nodes (+20% on 2,323), each one a distinct,
citable, time-boxed fact rather than a row-level dribble.

Derived, not re-derived
-----------------------
The activity and feed edges are built from links the ingest pipeline ALREADY
wrote (memory_entities on the rollup memories) plus FlowOS FKs, so there is no
second copy of the name-matching heuristics to drift. Every edge written carries
metadata.derivation and the source signal, and points at the single memory that
backs it where one exists.

Idempotent. entity_edges' unique key includes valid_from, so a plain upsert would
create a second LIVE edge on every run — every write here checks for an existing
OPEN edge (valid_to IS NULL) first. Retirement is soft; nothing is deleted.

Credentials come from the environment only (this file is committed):
  FLOWOS_DSN         postgresql://…/onestudio_hub   READ-ONLY, every read in a tx
  CABRAIN_DSN        postgresql://…/cabrain
  CABRAIN_NAMESPACE  default: flowos

Usage:
  python3 scripts/flowos-graph-spine.py --dry-run     # plan + census, write nothing
  python3 scripts/flowos-graph-spine.py               # build (cron-safe, re-runnable)
  python3 scripts/flowos-graph-spine.py --trace Sentra --trace Prism   # prove hops
"""
import argparse
import json
import os
import re
import sys
from collections import defaultdict
from urllib.parse import urlparse

import pg8000.native as pg

NS = os.environ.get("CABRAIN_NAMESPACE", os.environ.get("FLOWOS_NAMESPACE", "flowos"))

# New pieces of the ontology this script is responsible for. The reconcile step
# below picks up anything else that exists in the data but was never declared.
ENTITY_TYPES = [
    ("activity", "A time-boxed activity rollup for a repo, person, org or the studio"),
    ("feed", "The post stream of a venture (or of the studio)"),
    ("doc", "A documentation file inside a repo"),
    ("code_module", "A top-level source directory inside a repo"),
]
EDGE_TYPES = [
    ("ACTIVITY_OF", "activity rollup is about this person", "activity", "person"),
    ("ACTIVITY_IN", "activity rollup covers work in this repo", "activity", "repo"),
    ("FEED_OF", "feed is the post stream of a venture", "feed", "venture"),
    ("FEED_IN", "studio-level feed sits in a portfolio", "feed", "portfolio"),
    ("POSTED_IN", "person posts into a feed", "person", "feed"),
    ("ORGANIZED", "person owns/organised a meeting", "person", "meeting"),
    ("DOC_IN", "doc file lives in a repo", "doc", "repo"),
    ("CODE_IN", "code module lives in a repo", "code_module", "repo"),
    ("REPO_IN", "repo sits in a portfolio (via its GitHub org)", "repo", "portfolio"),
    # DISCUSSED_IN is genuinely polymorphic (person->issue 872, person->repo 58),
    # so it is left to the endpoint-type inference below rather than declared.
    ("DISCUSSED_IN", "person took part in the discussion on an issue or a repo",
     None, None),
]


# ------------------------------------------------------------------ plumbing
def _conn(dsn):
    u = urlparse(dsn)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=(u.path or "/").lstrip("/"))


def clean(x, n=180):
    return re.sub(r"\s+", " ", str(x or "")).strip()[:n]


class Writer:
    """Entity/edge writer that is a no-op under --dry-run but still counts."""

    def __init__(self, db, apply_):
        self.db, self.apply = db, apply_
        self.ents = defaultdict(int)
        self.edges = defaultdict(int)
        self.already = defaultdict(int)     # an OPEN edge already exists
        self.unresolved = defaultdict(int)  # an endpoint could not be resolved
        self.links = 0
        self._dry_ent = 0

    def entity(self, name, etype, summary, metadata):
        self.ents[etype] += 1
        if not self.apply:
            # Look the node up anyway so a dry run on an already-built graph can
            # still tell "would create" from "already live" on the edges.
            r = self.db.run("SELECT id FROM entities WHERE namespace=:ns AND name=:n",
                            ns=NS, n=name[:500])
            if r:
                return r[0][0]
            self._dry_ent += 1
            return f"dry:{self._dry_ent}"
        r = self.db.run("""
            INSERT INTO entities (namespace, name, entity_type, summary, metadata)
            VALUES (:ns,:n,:t,:s,:m::jsonb)
            ON CONFLICT (namespace, name) DO UPDATE
              SET entity_type = COALESCE(entities.entity_type, EXCLUDED.entity_type),
                  summary     = COALESCE(EXCLUDED.summary, entities.summary),
                  metadata    = entities.metadata || EXCLUDED.metadata
            RETURNING id""", ns=NS, n=name[:500], t=etype, s=clean(summary, 900),
            m=json.dumps(metadata, default=str))
        return r[0][0]

    def edge(self, src, dst, rel, fact, meta, when=None, memory_id=None,
             episode_id=None, weight=None):
        if not src or not dst or src == dst:
            self.unresolved[rel] += 1
            return 0
        real = not (str(src).startswith("dry:") or str(dst).startswith("dry:"))
        if real and self.db.run(
                """SELECT 1 FROM entity_edges WHERE namespace=:ns AND src_id=:s
                     AND dst_id=:d AND relation=:r AND valid_to IS NULL LIMIT 1""",
                ns=NS, s=src, d=dst, r=rel):
            self.already[rel] += 1
            return 0
        self.edges[rel] += 1
        if not self.apply:
            return 1
        self.db.run("""
            INSERT INTO entity_edges (namespace, src_id, dst_id, relation, fact,
                                      weight, valid_from, memory_id, episode_id, metadata)
            VALUES (:ns,:s,:d,:r,:f, COALESCE(:w::real, 1.0),
                    COALESCE(:vf::timestamptz, now()),
                    :m::uuid, :e::uuid, :meta::jsonb)
            ON CONFLICT (namespace,src_id,dst_id,relation,valid_from) DO NOTHING""",
            ns=NS, s=src, d=dst, r=rel, f=clean(fact, 900), w=weight, vf=when,
            m=memory_id, e=episode_id, meta=json.dumps(meta, default=str))
        return 1

    def link(self, memory_id, entity_id):
        if not memory_id or not entity_id or str(entity_id).startswith("dry:"):
            return
        self.links += 1
        if self.apply:
            self.db.run("""INSERT INTO memory_entities (memory_id, entity_id)
                           VALUES (:m::uuid,:e::uuid) ON CONFLICT DO NOTHING""",
                        m=memory_id, e=entity_id)


def load_entities(b):
    """(entity_type, source_id) -> id, plus name -> id and id -> (name, type)."""
    by_src, by_name, info = {}, {}, {}
    for eid, et, name, sid in b.run("""SELECT id, entity_type, name, metadata->>'source_id'
                                         FROM entities WHERE namespace=:ns""", ns=NS):
        info[eid] = (name, et)
        by_name[(et, name.strip().lower())] = eid
        if sid:
            by_src[(et, str(sid))] = eid
    return by_src, by_name, info


# ------------------------------------------------------------ 1. ACTIVITY
def build_activity(b, w):
    """activity entities + ACTIVITY_OF / ACTIVITY_IN, from the rollup memories.

    The rollup builder (scripts/flowos-activity-rollups.py) already resolved each
    rollup to the person/repo entities it names and wrote memory_entities rows.
    We reuse exactly those links, so the graph can never disagree with the recall
    surface about who a rollup is about.
    """
    rows = b.run("""
        SELECT m.id::text, m.source_ref, m.valid_at, m.metadata, m.content, m.episode_id::text
          FROM memories m
         WHERE m.namespace=:ns AND m.source_kind='flowos_activity_rollup'
           AND m.invalid_at IS NULL""", ns=NS)
    targets = defaultdict(list)
    for mid, et, name in b.run("""
        SELECT me.memory_id::text, e.entity_type, e.name
          FROM memory_entities me
          JOIN entities e ON e.id = me.entity_id
          JOIN memories m ON m.id = me.memory_id
         WHERE m.namespace=:ns AND m.source_kind='flowos_activity_rollup'""", ns=NS):
        targets[mid].append((et, name))
    ent_ids = {}
    for eid, et, name in b.run(
            "SELECT id, entity_type, name FROM entities WHERE namespace=:ns", ns=NS):
        ent_ids[(et, name)] = eid

    made = 0
    unmodelled = defaultdict(int)
    for mid, ref, valid_at, md, content, epi in rows:
        md = md or {}
        scope = md.get("scope") or ""
        period = md.get("period") or "week"
        signal = "claude" if md.get("signal") == "claude" else "commits"
        label = md.get("week") or md.get("month") or ""
        if scope in ("meta", "") or not label:
            unmodelled[scope or "(no scope)"] += 1
            continue

        if scope.startswith("repo:"):
            subject, name = scope[5:], f"{scope[5:]} · {label} commits"
            summary = f"Weekly GitHub commit activity for the {subject} repository in {label}."
        elif scope.startswith("person:"):
            subject = scope[7:]
            name = f"{subject} · {label} {'Claude' if signal == 'claude' else 'commits'}"
            summary = (f"Weekly {'Claude Code / AI usage' if signal == 'claude' else 'commit'}"
                       f" activity for {subject} in {label}.")
        elif scope.startswith("org:"):
            subject, name = scope[4:], f"{scope[4:]} · {label} commits"
            summary = f"Monthly engineering activity for the {subject} GitHub org in {label}."
        elif scope in ("studio", "studio-ai"):
            subject = "FlowOS studio"
            kind = "Claude" if scope == "studio-ai" else ("activity" if period == "month" else "commits")
            name, summary = f"FlowOS studio · {label} {kind}", \
                f"Studio-wide {kind} rollup for {label}."
        else:
            unmodelled[scope] += 1
            continue

        emeta = {"kind": "activity-rollup", "source_ref": ref, "scope": scope,
                 "period": period, "signal": signal, "window": label,
                 "subject": subject}
        for k in ("commits", "claudeEvents", "totalTokens", "people", "rank"):
            if md.get(k) is not None:
                emeta[k] = md[k]
        aid = w.entity(name, "activity", summary, emeta)
        made += 1
        w.link(mid, aid)

        base = {"derivation": "rollup", "via": "memory_entities:flowos_activity_rollup",
                "source_ref": ref, "period": period, "signal": signal,
                "window": label, "scope": scope, "aggregate": False}
        for et, tname in targets.get(mid, []):
            dst = ent_ids.get((et, tname))
            if et == "person":
                w.edge(aid, dst, "ACTIVITY_OF",
                       f"{name} is activity of {tname} — {tname} is named as a subject of "
                       f"the {label} {signal} rollup {ref}.",
                       dict(base, target="person"), when=valid_at,
                       memory_id=mid, episode_id=epi,
                       weight=float(md.get("commits") or md.get("claudeEvents") or 0))
            elif et == "repo":
                w.edge(aid, dst, "ACTIVITY_IN",
                       f"{name} covers work in the {tname} repository, per the {label} "
                       f"{signal} rollup {ref}.",
                       dict(base, target="repo"), when=valid_at,
                       memory_id=mid, episode_id=epi,
                       weight=float(md.get("commits") or 0))

    # The rollup builder re-retains all ~470 rollups on every sync, which mints
    # NEW memory rows and invalidates the old ones. An edge written on a previous
    # run therefore points at a memory that is no longer live. metadata.source_ref
    # is stable, so re-point every open ACTIVITY_* edge at the current live memory
    # for its source_ref. Without this the provenance link rots silently.
    restamped = 0
    if w.apply:
        restamped = len(w.db.run("""
            UPDATE entity_edges g
               SET memory_id = m.id, episode_id = COALESCE(m.episode_id, g.episode_id)
              FROM (SELECT DISTINCT ON (source_ref) source_ref, id, episode_id
                      FROM memories
                     WHERE namespace=:ns AND source_kind='flowos_activity_rollup'
                       AND invalid_at IS NULL
                     ORDER BY source_ref, valid_at DESC, ingested_at DESC) m
             WHERE g.namespace=:ns AND g.valid_to IS NULL
               AND g.relation IN ('ACTIVITY_OF','ACTIVITY_IN')
               AND g.metadata->>'source_ref' = m.source_ref
               AND g.memory_id IS DISTINCT FROM m.id
            RETURNING g.id""", ns=NS))
    return made, dict(unmodelled), restamped


# --------------------------------------------------------------- 2. FEEDS
def build_feeds(s, b, w):
    """One feed node per venture with posts (+ a studio feed), not 1,264 posts.

    A post's venture is `posts.venture_id`, falling back to the venture of the
    channel it was posted in. Everything else (the #studio channel, DM-less posts
    with no channel) is studio-level and lands in one studio feed hung off the
    One Studio portfolio, so it is still reachable from the top of the chain.
    """
    posts = s.run("""
        SELECT p.id::text, COALESCE(p.venture_id, ch.venture_id) AS vid,
               p.author_id::text, p.created_at, p.type::text, ch.venture_id IS NOT NULL
                 AND p.venture_id IS NULL AS via_channel
          FROM posts p LEFT JOIN channels ch ON ch.id = p.channel_id""")
    vnames = {r[0]: r[1] for r in s.run("SELECT id, name FROM ventures")}
    vport = {r[0]: r[1] for r in s.run("SELECT id, portfolio_id FROM ventures")}
    pnames = {str(r[0]): (r[1] or r[2]) for r in
              s.run("SELECT id, display_name, email FROM users")}
    portfolios = {r[0]: r[1] for r in s.run("SELECT id, initcap(name) FROM portfolios")}

    by_src, by_name, _ = load_entities(b)
    # memory id per post, so the feed node expands to the real posts on recall
    memof = {r[0]: r[1] for r in b.run("""
        SELECT DISTINCT ON (source_ref) source_ref, id::text FROM memories
         WHERE namespace=:ns AND source_kind='flowos_post'
         ORDER BY source_ref, (invalid_at IS NULL) DESC, valid_at DESC""", ns=NS)}

    STUDIO = "__studio__"
    agg = defaultdict(lambda: {"n": 0, "lo": None, "hi": None, "authors": defaultdict(int),
                               "posts": [], "via_channel": 0, "types": defaultdict(int)})
    for pid, vid, aid, created, ptype, via_ch in posts:
        key = vid if vid and vid in vnames else STUDIO
        a = agg[key]
        a["n"] += 1
        a["posts"].append(pid)
        a["types"][ptype] += 1
        if via_ch:
            a["via_channel"] += 1
        if aid:
            a["authors"][aid] += 1
        if created:
            a["lo"] = min(a["lo"] or created, created)
            a["hi"] = max(a["hi"] or created, created)

    # Two prod ventures can share a display name ('Seed-Stage PDPL Starter Kit'
    # is both pdpl-starter-kit and studio-compliance-kit). Entities are keyed on
    # name, so an undisambiguated "<name> feed" would silently merge two
    # ventures' post streams into one node. Suffix the slug when, and only when,
    # the name is ambiguous.
    dupe = {n for n, c in
            defaultdict(int, {k: sum(1 for x in vnames.values() if x == k)
                              for k in vnames.values()}).items() if c > 1}

    made, unresolved_v, unresolved_p, live_names = 0, [], 0, set()
    for key, a in sorted(agg.items(), key=lambda x: -x[1]["n"]):
        studio = key is STUDIO or key == STUDIO
        vname = "FlowOS studio" if studio else vnames[key]
        name = f"{vname} feed" + ("" if studio or vname not in dupe else f" ({key})")
        live_names.add(name)
        window = (f"{a['lo']:%Y-%m-%d} to {a['hi']:%Y-%m-%d}" if a["lo"] else "unknown window")
        top = sorted(a["types"].items(), key=lambda x: -x[1])[:3]
        summary = (f"The {vname} feed: {a['n']} posts ({', '.join(f'{n} {t}' for t, n in top)}) "
                   f"by {len(a['authors'])} author(s), {window}."
                   + ("" if studio else f" {a['via_channel']} of them are attributed via "
                                        f"the channel they were posted in."))
        fid = w.entity(name, "feed", summary,
                       {"kind": "feed", "venture_id": None if studio else key,
                        "posts": a["n"], "authors": len(a["authors"]),
                        "window": window, "scope": "studio" if studio else "venture"})
        made += 1

        base = {"derivation": "fk", "derived_from": "posts",
                "via": "posts.venture_id + channels.venture_id",
                "aggregate": True, "source_count": a["n"]}
        if studio:
            pid_ = "one-studio" if "one-studio" in portfolios else next(iter(portfolios), None)
            dst = by_name.get(("portfolio", (portfolios.get(pid_) or "").strip().lower())) \
                or by_name.get(("portfolio", f"{portfolios.get(pid_)} (portfolio)".strip().lower()))
            if not w.edge(fid, dst, "FEED_IN",
                          f"The studio-wide feed ({a['n']} posts with no venture of their own, "
                          f"mostly the #studio channel) sits in the "
                          f"{portfolios.get(pid_)} portfolio.",
                          dict(base, portfolio=pid_), when=a["hi"]) and not dst:
                unresolved_v.append("(studio -> portfolio)")
        else:
            dst = by_src.get(("venture", key))
            if dst:
                w.edge(fid, dst, "FEED_OF",
                       f"The {vname} feed is the post stream of the {vname} venture: "
                       f"{a['n']} posts by {len(a['authors'])} author(s), {window}.",
                       dict(base, venture_id=key, portfolio_id=vport.get(key)),
                       when=a["hi"])
            else:
                # No venture entity for this venture id. Known cause: two prod
                # ventures share a display name and entities are keyed on name,
                # so one of the pair never got a node. We do NOT merge the feed
                # into the surviving twin — that would silently attribute one
                # venture's posts to another. Report it instead.
                unresolved_v.append(f"{key} ({vname})")

        for auth, n in sorted(a["authors"].items(), key=lambda x: -x[1]):
            src = by_src.get(("person", auth)) or \
                by_name.get(("person", (pnames.get(auth) or "").strip().lower()))
            if not src:
                unresolved_p += 1
                continue
            w.edge(src, fid, "POSTED_IN",
                   f"{pnames.get(auth, auth)} wrote {n} of the {a['n']} posts in the "
                   f"{vname} feed ({window}).",
                   dict(base, posts_by_author=n), when=a["hi"], weight=float(n))

        for pid in a["posts"]:
            w.link(memof.get(f"db:post:{pid}"), fid)
    return made, unresolved_v, unresolved_p, live_names


# ------------------------------------------------------------- 3. MEETINGS
def build_meetings(s, b, w, title_match=True):
    """Un-strand the meetings.

    Every calendar_event has an owner_id (224/224) — that FK was simply never
    walked, which is why 183 meetings had no live edge. Attendance covers only
    the 41 events that have attendee rows, and venture_id only 31.

    A second, weaker pass reads the venture out of the event TITLE. It only fires
    when a distinctive token (the venture slug or its collapsed name, >=4 chars)
    matches exactly one venture, so 'Soft Skills Discussion' does not become
    'flow-skills'. Those edges are stamped via='title-match' and are the only
    ones here that are a guess rather than a foreign key.
    """
    events = s.run("""SELECT id::text, title, owner_id::text, start_at, venture_id
                        FROM calendar_events""")
    vent = {r[0]: r[1] for r in s.run("SELECT id, name FROM ventures")}
    pnames = {str(r[0]): (r[1] or r[2]) for r in
              s.run("SELECT id, display_name, email FROM users")}
    by_src, by_name, _ = load_entities(b)
    memof = {r[0]: r[1] for r in b.run("""
        SELECT DISTINCT ON (source_ref) source_ref, id::text FROM memories
         WHERE namespace=:ns AND source_kind='flowos_calendar'
         ORDER BY source_ref, (invalid_at IS NULL) DESC, valid_at DESC""", ns=NS)}

    # distinctive token -> venture id (skip tokens shared by several ventures)
    def collapse(x):
        return re.sub(r"[^a-z0-9]", "", (x or "").lower())
    tok2v = defaultdict(set)
    for vid, nm in vent.items():
        for t in {collapse(vid), collapse(nm)}:
            if len(t) >= 4:
                tok2v[t].add(vid)
    tok2v = {t: next(iter(v)) for t, v in tok2v.items() if len(v) == 1}

    organized, about, no_owner, no_meeting = 0, 0, 0, 0
    for eid, title, owner, start, vid in events:
        m_ent = by_src.get(("meeting", eid))
        if not m_ent:
            no_meeting += 1
            continue
        mem = memof.get(f"db:calendar-event:{eid}")
        if owner:
            src = by_src.get(("person", owner)) or \
                by_name.get(("person", (pnames.get(owner) or "").strip().lower()))
            if src:
                organized += w.edge(
                    src, m_ent, "ORGANIZED",
                    f"{pnames.get(owner, owner)} owns the calendar event "
                    f"\"{clean(title, 80)}\""
                    + (f" on {start:%Y-%m-%d}" if start else "") + ".",
                    {"derivation": "fk", "derived_from": "calendar_events.owner_id",
                     "via": "calendar_events.owner_id", "aggregate": False,
                     "source_ref": f"db:calendar-event:{eid}"},
                    when=start, memory_id=mem)
            else:
                no_owner += 1
        if vid or not title_match:
            continue        # explicit venture_id is already MEETING_ABOUT'd
        toks = {t: tok2v[t] for t in re.findall(r"[a-z0-9]+", (title or "").lower())
                if t in tok2v}
        # also allow the collapsed whole title to contain a collapsed venture name
        # ("One Studio-Stand up" -> onestudio), which word splitting misses
        ct = collapse(title)
        toks.update({t: v for t, v in tok2v.items() if len(t) >= 5 and t in ct})
        hits = set(toks.values())
        if len(hits) == 1:
            v = next(iter(hits))
            dst = by_src.get(("venture", v))
            about += w.edge(
                m_ent, dst, "MEETING_ABOUT",
                f"The meeting \"{clean(title, 80)}\" names the {vent[v]} venture in its "
                f"title. calendar_events.venture_id is NULL for this event, so this is a "
                f"title match, not a recorded link.",
                {"derivation": "heuristic", "via": "title-match",
                 "derived_from": "calendar_events.title", "confidence": "medium",
                 "matched_token": sorted(t for t, x in toks.items() if x == v),
                 "aggregate": False, "source_ref": f"db:calendar-event:{eid}"},
                when=start, memory_id=mem)
    return organized, about, no_owner, no_meeting


# ------------------------------------------------------------- 4. ONTOLOGY
def reconcile_ontology(b, apply_):
    """Declare our own types, then register anything real but undeclared.

    GET /api/brain/graph/ontology lists entity_types/edge_types, NOT the distinct
    values actually present, so a builder that mints entities without registering
    the type makes it invisible in the Graph Explorer. This closes that gap for
    every builder at once.
    """
    added = []
    if apply_:
        for n_, d_ in ENTITY_TYPES:
            b.run("""INSERT INTO entity_types (namespace,name,description) VALUES (:ns,:n,:d)
                     ON CONFLICT (namespace,name) DO UPDATE SET description=EXCLUDED.description""",
                  ns=NS, n=n_, d=d_)
        for n_, d_, st, dt in EDGE_TYPES:
            b.run("""INSERT INTO edge_types (namespace,name,description,src_type,dst_type)
                     VALUES (:ns,:n,:d,:st,:dt)
                     ON CONFLICT (namespace,name) DO UPDATE SET description=EXCLUDED.description,
                       src_type=COALESCE(EXCLUDED.src_type, edge_types.src_type),
                       dst_type=COALESCE(EXCLUDED.dst_type, edge_types.dst_type)""",
                  ns=NS, n=n_, d=d_, st=st, dt=dt)

    for (et, n) in b.run("""SELECT entity_type, count(*) FROM entities
                             WHERE namespace=:ns AND entity_type IS NOT NULL
                               AND entity_type NOT IN (SELECT name FROM entity_types WHERE namespace=:ns)
                             GROUP BY 1 ORDER BY 2 DESC""", ns=NS):
        added.append(("entity_type", et, n))
        if apply_:
            b.run("""INSERT INTO entity_types (namespace,name,description) VALUES (:ns,:n,:d)
                     ON CONFLICT (namespace,name) DO NOTHING""",
                  ns=NS, n=et, d=f"auto-registered by flowos-graph-spine.py ({n} entities)")
    for (rel, n) in b.run("""SELECT relation, count(*) FROM entity_edges
                              WHERE namespace=:ns AND valid_to IS NULL
                                AND relation NOT IN (SELECT name FROM edge_types WHERE namespace=:ns)
                              GROUP BY 1 ORDER BY 2 DESC""", ns=NS):
        added.append(("edge_type", rel, n))
        if apply_:
            b.run("""INSERT INTO edge_types (namespace,name,description) VALUES (:ns,:n,:d)
                     ON CONFLICT (namespace,name) DO NOTHING""",
                  ns=NS, n=rel, d=f"auto-registered by flowos-graph-spine.py ({n} live edges)")

    # src_type/dst_type on the LLM-extracted relations were never filled in, so
    # the Graph Explorer showed "? -> ?" for a third of them. Infer them from the
    # live edges: the dominant endpoint type if it covers >=95%, otherwise every
    # type that occurs, pipe-separated, so a polymorphic relation reads honestly
    # instead of claiming a shape it does not have.
    obs = defaultdict(lambda: (defaultdict(int), defaultdict(int)))
    for rel, st, dt, n in b.run("""
        SELECT g.relation, s.entity_type, d.entity_type, count(*)
          FROM entity_edges g JOIN entities s ON s.id = g.src_id
                              JOIN entities d ON d.id = g.dst_id
         WHERE g.namespace=:ns AND g.valid_to IS NULL GROUP BY 1,2,3""", ns=NS):
        a, c = obs[rel]
        a[st] += n
        c[dt] += n

    def dominant(d):
        tot = sum(d.values()) or 1
        top, n = max(d.items(), key=lambda x: x[1])
        return top if n / tot >= 0.95 else "|".join(
            k for k, _ in sorted(d.items(), key=lambda x: -x[1]))
    typed = 0
    for rel, (srcs, dsts) in obs.items():
        st, dt = dominant(srcs), dominant(dsts)
        r = b.run("""SELECT src_type, dst_type FROM edge_types
                      WHERE namespace=:ns AND name=:n""", ns=NS, n=rel)
        if r and (r[0][0], r[0][1]) == (st, dt):
            continue
        typed += 1
        added.append(("endpoint types", rel, f"{st} -> {dt}"))
        if apply_:
            b.run("""UPDATE edge_types SET src_type=:s, dst_type=:d
                      WHERE namespace=:ns AND name=:n""", ns=NS, n=rel, s=st, d=dt)
    return added


def retire_stale_feeds(b, w, live_names, apply_):
    """Soft-retire feed nodes that this run no longer produces.

    A venture rename, or a venture whose posts all moved, leaves a feed node
    behind whose edges would otherwise stay live and lie. Retirement is soft
    (valid_to = now()); nothing is deleted.
    """
    stale = b.run("""SELECT id, name FROM entities
                      WHERE namespace=:ns AND entity_type='feed' AND NOT (name = ANY(:n))""",
                  ns=NS, n=list(live_names))
    n = 0
    for eid, name in stale:
        cnt = b.run("""SELECT count(*) FROM entity_edges WHERE namespace=:ns
                        AND valid_to IS NULL AND (src_id=:e OR dst_id=:e)""", ns=NS, e=eid)[0][0]
        n += cnt
        if apply_ and cnt:
            b.run("""UPDATE entity_edges SET valid_to = now()
                      WHERE namespace=:ns AND valid_to IS NULL AND (src_id=:e OR dst_id=:e)""",
                  ns=NS, e=eid)
            b.run("""UPDATE entities SET summary = 'SUPERSEDED — this feed node is no '
                     'longer produced by flowos-graph-spine.py; its edges are retired. '
                     || COALESCE(summary,'') WHERE id=:e AND summary NOT LIKE 'SUPERSEDED%%'""",
                  e=eid)
    return [x[1] for x in stale], n


def census(b):
    print("\n=== ontology census (what GET /graph/ontology will list) ===")
    print("-- entity types")
    tot = 0
    for name, n in b.run("""
        SELECT t.name, (SELECT count(*) FROM entities e
                         WHERE e.namespace=t.namespace AND e.entity_type=t.name)
          FROM entity_types t WHERE t.namespace=:ns ORDER BY 2 DESC, 1""", ns=NS):
        tot += n
        print(f"   {name:14} {n}")
    print(f"   {'TOTAL':14} {tot}")
    orphan = b.run("""SELECT count(*) FROM entities WHERE namespace=:ns
                        AND (entity_type IS NULL OR entity_type NOT IN
                             (SELECT name FROM entity_types WHERE namespace=:ns))""", ns=NS)[0][0]
    print(f"   entities with an unregistered/NULL type: {orphan}")

    print("-- relations (live edges)")
    tot = 0
    for name, st, dt, n in b.run("""
        SELECT t.name, COALESCE(t.src_type,'?'), COALESCE(t.dst_type,'?'),
               (SELECT count(*) FROM entity_edges e WHERE e.namespace=t.namespace
                  AND e.relation=t.name AND e.valid_to IS NULL)
          FROM edge_types t WHERE t.namespace=:ns ORDER BY 4 DESC, 1""", ns=NS):
        tot += n
        print(f"   {name:16} {n:6}   {st} -> {dt}")
    print(f"   {'TOTAL':16} {tot:6}")
    orphan = b.run("""SELECT count(*) FROM entity_edges WHERE namespace=:ns AND valid_to IS NULL
                        AND relation NOT IN (SELECT name FROM edge_types WHERE namespace=:ns)""",
                   ns=NS)[0][0]
    print(f"   live edges with an unregistered relation: {orphan}")


# ---------------------------------------------------------------- 5. TRACE
HOPS = [
    ("repos", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
              "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='REPO_OF' AND g.dst_id=:v"),
    ("members", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
                "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='MEMBER_OF' AND g.dst_id=:v"),
    ("goals", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
              "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='GOAL_FOR' AND g.dst_id=:v"),
    ("roadmaps", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
                 "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='PLANS' AND g.dst_id=:v"),
    ("okrs", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
             "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='TARGETS' AND g.dst_id=:v"),
    ("learnings", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
                  "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='APPLIES_TO' AND g.dst_id=:v"),
    ("channels", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
                 "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='CHANNEL_OF' AND g.dst_id=:v"),
    ("meetings", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
                 "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='MEETING_ABOUT' AND g.dst_id=:v"),
    ("agents", "SELECT e.id FROM entity_edges g JOIN entities e ON e.id=g.src_id "
               "WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation='WORKS_ON' AND g.dst_id=:v"),
]


def trace(b, needle):
    """Walk the owner's pipeline from one venture and print the row count per hop."""
    rows = b.run("""SELECT id, name FROM entities WHERE namespace=:ns AND entity_type='venture'
                     AND (lower(name) LIKE :q OR lower(metadata->>'source_id') LIKE :q)
                     ORDER BY length(name) LIMIT 1""", ns=NS, q=f"%{needle.lower()}%")
    if not rows:
        print(f"\n### {needle}: no venture entity matches")
        return
    v, vname = rows[0]
    print(f"\n### venture: {vname}")
    portfolio = b.run("""SELECT e.name FROM entity_edges g JOIN entities e ON e.id=g.dst_id
                          WHERE g.namespace=:ns AND g.valid_to IS NULL
                            AND g.relation='BELONGS_TO' AND g.src_id=:v""", ns=NS, v=v)
    print(f"   portfolio            {len(portfolio):5}  {[p[0] for p in portfolio]}")

    counts = {}
    for label, sql in HOPS:
        r = b.run(sql, ns=NS, v=v)
        counts[label] = [x[0] for x in r]
        print(f"   {label:20} {len(r):5}")

    repos = counts["repos"]
    if repos:
        for label, rel, direction in (("  code_modules", "CODE_IN", "src"),
                                      ("  docs", "DOC_IN", "src"),
                                      ("  committers", "COMMITS_TO", "src"),
                                      ("  repo activity", "ACTIVITY_IN", "src")):
            n = b.run(f"""SELECT count(DISTINCT g.{direction}_id) FROM entity_edges g
                           WHERE g.namespace=:ns AND g.valid_to IS NULL AND g.relation=:r
                             AND g.dst_id = ANY(:ids)""", ns=NS, r=rel, ids=repos)[0][0]
            print(f"   {label:20} {n:5}")
    members = counts["members"]
    if members:
        n = b.run("""SELECT count(DISTINCT g.src_id) FROM entity_edges g
                      WHERE g.namespace=:ns AND g.valid_to IS NULL
                        AND g.relation='ACTIVITY_OF' AND g.dst_id = ANY(:ids)""",
                  ns=NS, ids=members)[0][0]
        print(f"     member activity    {n:5}")
        n = b.run("""SELECT count(DISTINCT g.src_id) FROM entity_edges g
                      WHERE g.namespace=:ns AND g.valid_to IS NULL
                        AND g.relation='ACTIVITY_OF' AND g.dst_id = ANY(:ids)
                        AND g.valid_from > now() - interval '14 days'""",
                  ns=NS, ids=members)[0][0]
        print(f"     …last 14 days      {n:5}")

    feed = b.run("""SELECT e.id, e.name FROM entity_edges g JOIN entities e ON e.id=g.src_id
                     WHERE g.namespace=:ns AND g.valid_to IS NULL
                       AND g.relation='FEED_OF' AND g.dst_id=:v""", ns=NS, v=v)
    print(f"   feed                 {len(feed):5}")
    if feed:
        n = b.run("SELECT count(*) FROM memory_entities WHERE entity_id=:e", e=feed[0][0])[0][0]
        a = b.run("""SELECT count(*) FROM entity_edges WHERE namespace=:ns AND valid_to IS NULL
                      AND relation='POSTED_IN' AND dst_id=:e""", ns=NS, e=feed[0][0])[0][0]
        print(f"     posts in feed      {n:5}\n     feed authors       {a:5}")


# ------------------------------------------------------------------- main
def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dry-run", action="store_true", help="plan and count, write nothing")
    ap.add_argument("--no-title-match", action="store_true",
                    help="skip the heuristic meeting-title -> venture pass")
    ap.add_argument("--trace", action="append", default=[],
                    help="after building, walk the pipeline from this venture (repeatable)")
    ap.add_argument("--only", choices=["activity", "feeds", "meetings", "ontology"],
                    action="append", default=[])
    args = ap.parse_args()
    steps = set(args.only or ["activity", "feeds", "meetings", "ontology"])
    apply_ = not args.dry_run

    s, b = _conn(os.environ["FLOWOS_DSN"]), _conn(os.environ["CABRAIN_DSN"])
    b.run("SET search_path = public")
    b.run("SET statement_timeout = 0")
    s.run("BEGIN READ ONLY")          # FlowOS prod is shared and read-only to us
    w = Writer(b, apply_)

    if "activity" in steps:
        made, unmodelled, restamped = build_activity(b, w)
        print(f"[activity] rollup memories modelled: {made}"
              + (f"   not modelled: {unmodelled}" if unmodelled else "")
              + f"   edges re-pointed at the current live memory: {restamped}")
    if "feeds" in steps:
        made, uv, up, live_names = build_feeds(s, b, w)
        stale, retired = retire_stale_feeds(b, w, live_names, apply_)
        print(f"[feeds] feed nodes: {made}   authors unresolved: {up}"
              + (f"   NO VENTURE ENTITY for: {uv}" if uv else "")
              + (f"   stale feed nodes retired: {stale} ({retired} edges)" if stale else ""))
    if "meetings" in steps:
        org, about, no_owner, no_meeting = build_meetings(
            s, b, w, title_match=not args.no_title_match)
        print(f"[meetings] ORGANIZED: {org}   MEETING_ABOUT(title): {about}   "
              f"owner unresolved: {no_owner}   event without a meeting entity: {no_meeting}")
    s.run("ROLLBACK")

    print(f"\nentities written/updated: {dict(w.ents)}")
    print(f"edges written:            {dict(w.edges)}")
    print(f"edges already live (skipped — this is what makes a re-run a no-op): "
          f"{dict(w.already)}")
    print(f"edges skipped, endpoint unresolved: {dict(w.unresolved)}")
    print(f"memory_entities links upserted: {w.links}")

    if "ontology" in steps:
        added = reconcile_ontology(b, apply_)
        if added:
            print("\nontology gaps closed (real in the data, never declared):")
            for kind, name, n in added:
                print(f"   {kind:12} {name:16} {n}")
        else:
            print("\nontology: registry already matches the data")
    census(b)
    for t in args.trace:
        trace(b, t)
    if args.dry_run:
        print("\n(dry run — nothing was written)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
