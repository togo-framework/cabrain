#!/usr/bin/env python3
"""
Backfill provenance onto FK-derived entity_edges in the flowos brain.

The typed-graph builders originally inserted edges with metadata {} and no
memory_id, so only the LLM-extracted edges could answer "why does the brain
believe this?". This walks every FK family (see flowos_edge_provenance.FAMILIES),
finds the source record behind each edge and, where a single defensible source
record exists, sets memory_id + episode_id + metadata.source_ref. Aggregate edges
(many source rows, or a source table that is never ingested as a memory) get
`derived_from` / `aggregate` metadata instead — no link is invented.

Usage:
  CABRAIN_DSN=postgresql://…  FLOWOS_DSN=postgresql://…  \
    python3 scripts/backfill-edge-provenance.py [--apply]

Without --apply it prints the plan and changes nothing.
Never hard-code credentials here — this file is committed.
"""
import os
import sys
from urllib.parse import urlparse

import pg8000.native as pg

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from flowos_edge_provenance import FAMILIES, build_plan, apply_plan  # noqa: E402

NS = os.environ.get("CABRAIN_NAMESPACE", "flowos")


def _conn(dsn):
    u = urlparse(dsn)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=u.path.lstrip("/"))


def counts(b):
    row = b.run("""SELECT count(*), count(memory_id), count(episode_id),
                          count(*) FILTER (WHERE metadata::text <> '{}')
                     FROM entity_edges WHERE namespace = :ns""", ns=NS)[0]
    return dict(zip(("edges", "memory_id", "episode_id", "metadata"), row))


def main():
    apply = "--apply" in sys.argv
    s = _conn(os.environ["FLOWOS_DSN"])
    b = _conn(os.environ["CABRAIN_DSN"])
    s.run("BEGIN READ ONLY")
    b.run("SET search_path = public")
    b.run("SET statement_timeout = 0")

    before = counts(b)
    print(f"[before] {before}")

    plan, stats, per_relation = build_plan(s, b, NS)
    s.run("ROLLBACK")

    print(f"\n[plan] edges covered: {len(plan)}")
    for k, v in sorted(stats.items(), key=lambda kv: -kv[1]):
        print(f"   {k:24} {v}")
    print("\n[plan] per relation (kind: n)")
    for rel in sorted(per_relation):
        print(f"   {rel:16} " + ", ".join(f"{k}:{v}" for k, v in sorted(per_relation[rel].items())))

    if not apply:
        print("\n(dry run — pass --apply to write)")
        return
    n_link, n_meta = apply_plan(b, plan, NS)
    print(f"\n[apply] rows updated: {n_meta}   of which hard-linked to a memory: {n_link}")

    # Anything still bare is an edge no CURRENT FlowOS row supports (e.g. a
    # membership that was later removed). Don't invent a source and don't delete
    # it — say so in the metadata so it is still explainable.
    # Scoped to the relations only WE build — other writers (the LLM extractor, the
    # code-graph pass) put their own edges in this table and must not be labelled
    # "unsupported" just because no FlowOS FK backs them. COMMITS_TO / REPO_OF are
    # deliberately excluded: the code-graph pass emits them too.
    rels = sorted({f["relation"] for f in FAMILIES})
    orphans = b.run("""
        UPDATE entity_edges SET metadata = metadata || jsonb_build_object(
                 'derivation', 'fk', 'derived_from', 'unknown', 'aggregate', false,
                 'unlinked_reason', 'no current FlowOS row supports this triple')
         WHERE namespace = :ns AND memory_id IS NULL AND metadata::text = '{}'
           AND relation = ANY(:rels)
        RETURNING 1""", ns=NS, rels=rels)
    print(f"[apply] unsupported (stale) edges annotated: {len(orphans or [])}")
    print(f"[after] {counts(b)}")


if __name__ == "__main__":
    main()
