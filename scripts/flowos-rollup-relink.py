#!/usr/bin/env python3
"""Re-attach activity rollups to their person / repo entities.

WHY THIS EXISTS
---------------
/api/brain/retain does not update a row in place: a repeat sourceRef SUPERSEDES
— the old row is soft-retired and a NEW row with a NEW id is inserted. The
memory_entities rows still point at the OLD id, so the refreshed rollup silently
falls out of the entity graph and stops being reachable by 1-hop expansion and
by the graph spine.

flowos-activity-rollups.py repairs that itself, but only when it has a brain
DSN. The copy that actually runs on a timer (/opt/cabrain-analytics on the
Proxmox box) does NOT have one — the brain Postgres is not reachable from
there — so every rollup it refreshes comes back orphaned. This script closes
that loop from the workspace, where the brain DSN does work.

It needs no FlowOS access and no analytics DB: everything it needs is already in
each rollup's own metadata (scope, repo, person, ranking, zeroCommit).

    export CABRAIN_DSN=postgresql://cabrain:***@host:5432/cabrain
    python3 scripts/flowos-rollup-relink.py [--dry-run]
"""
from __future__ import annotations

import argparse
import os
import sys
from urllib.parse import urlparse

import pg8000.native as pg

NS = os.environ.get("CABRAIN_NAMESPACE", os.environ.get("FLOWOS_NAMESPACE", "flowos"))
SOURCE_KIND = "flowos_activity_rollup"


def conn(dsn: str):
    u = urlparse(dsn)
    c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=(u.path or "/").lstrip("/"))
    c.run("SET search_path = public")
    return c


def wanted_entities(md: dict) -> tuple[set[str], set[str]]:
    """(person display names, repo full names) this rollup is about."""
    people: set[str] = set()
    repos: set[str] = set()
    scope = str(md.get("scope") or "")
    if scope.startswith("person:"):
        people.add(scope[7:])
    elif scope.startswith("repo:"):
        repos.add(scope[5:])
    if md.get("person"):
        people.add(str(md["person"]))
    if md.get("repo"):
        repos.add(str(md["repo"]))
    # The bilingual leaderboard carries the whole ranking, including the
    # zero-commit members — who are exactly what "least active" asks for and
    # must stay attached, not be dropped for having no commits.
    for row in md.get("ranking") or []:
        if isinstance(row, dict) and row.get("person"):
            people.add(str(row["person"]))
    for nm in md.get("zeroCommit") or []:
        people.add(str(nm))
    return people, repos


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--limit", type=int, default=0,
                    help="cap how many people/repos are linked per rollup (0 = all)")
    args = ap.parse_args()

    dsn = os.environ.get("CABRAIN_DSN", "")
    if not dsn:
        sys.exit("CABRAIN_DSN must be set")
    b = conn(dsn)

    by_name: dict[str, str] = {}
    by_repo: dict[str, str] = {}
    for eid, name, etype in b.run(
            "SELECT id, name, entity_type FROM entities WHERE namespace = :ns",
            ns=NS):
        if etype == "person":
            by_name[name.strip().lower()] = eid
        elif etype == "repo":
            by_repo[name.strip().lower()] = eid

    rows = b.run("""
        SELECT m.id, m.metadata
        FROM memories m
        WHERE m.namespace = :ns AND m.source_kind = :sk AND m.invalid_at IS NULL
    """, ns=NS, sk=SOURCE_KIND)

    made = 0
    touched = 0
    unresolved: set[str] = set()
    for mid, md in rows:
        people, repos = wanted_entities(md or {})
        if args.limit:
            people = set(sorted(people)[:args.limit])
            repos = set(sorted(repos)[:args.limit])
        eids = set()
        for p in people:
            eid = by_name.get(p.strip().lower())
            if eid:
                eids.add(eid)
            else:
                unresolved.add("person:" + p)
        for r in repos:
            eid = by_repo.get(r.strip().lower())
            if eid:
                eids.add(eid)
            else:
                unresolved.add("repo:" + r)
        if not eids:
            continue
        have = {r[0] for r in b.run(
            "SELECT entity_id FROM memory_entities WHERE memory_id = :m", m=mid)}
        missing = eids - have
        if not missing:
            continue
        touched += 1
        if args.dry_run:
            made += len(missing)
            continue
        for eid in missing:
            b.run("""INSERT INTO memory_entities (memory_id, entity_id)
                     VALUES (:m, :e) ON CONFLICT DO NOTHING""", m=mid, e=eid)
            made += 1

    print(f"rollup relink: {made} memory_entities links "
          f"{'would be added' if args.dry_run else 'added'} across {touched} rollups; "
          f"{len(unresolved)} name(s) had no entity")
    if unresolved:
        print("  unresolved sample: " + ", ".join(sorted(unresolved)[:8]))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
