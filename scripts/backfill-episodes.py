#!/usr/bin/env python3
"""
Populate public.episodes and link memories/edges back to them.

Why this matters: an episode is the RAW INGESTED UNIT (a post, an issue, a
transcript). Graphiti's contract is that every derived memory and every extracted
edge traces back to the episode it came from, so a fact can be *explained* by its
source. The table shipped in the schema but nothing ever wrote to it, so
`memories.episode_id` and `entity_edges.episode_id` were dead columns.

Source of truth: memories whose source_ref looks like 'db:<kind>:<id>' — those are
the rows imported from the FlowOS production database, i.e. real source records.
One episode per distinct source_ref; when a ref has several memory rows (re-ingest
/ supersession) the LIVE, newest row supplies the body.

Idempotent: episodes has UNIQUE(namespace, source_ref) → ON CONFLICT DO UPDATE, and
the link UPDATEs are no-ops once applied.

Environment:
  CABRAIN_DSN        postgresql://user:pass@host:port/cabrain   (required)
  CABRAIN_NAMESPACE  default: flowos
  EPISODE_REF_LIKE   default: db:%

Never hard-code credentials here — this file is committed.
"""
import os
from urllib.parse import urlparse

import pg8000.native as pg

NS = os.environ.get("CABRAIN_NAMESPACE", "flowos")
REF_LIKE = os.environ.get("EPISODE_REF_LIKE", "db:%")


def brain():
    u = urlparse(os.environ["CABRAIN_DSN"])
    c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=u.path.lstrip("/"))
    c.run("SET search_path = public")
    return c


# A short human label: "Post · <first line of the body, squeezed to 90 chars>".
LABEL = r"""
  initcap(replace(split_part(source_ref, ':', 2), '-', ' ')) || ' · ' ||
  left(btrim(regexp_replace(split_part(content, E'\n', 1), '\s+', ' ', 'g')), 90)
"""

INSERT = f"""
WITH pick AS (
  SELECT DISTINCT ON (source_ref)
         source_ref, content, source_kind, valid_at,
         {LABEL} AS label
    FROM memories
   WHERE namespace = :ns AND source_ref LIKE :ref
   ORDER BY source_ref, (invalid_at IS NULL) DESC, valid_at DESC, ingested_at DESC
)
INSERT INTO episodes (id, namespace, name, body, source_kind, source_ref,
                      occurred_at, ingested_at, metadata)
SELECT gen_random_uuid(), :ns, label, content, source_kind, source_ref,
       valid_at, now(), jsonb_build_object('derived_from', 'memories')
  FROM pick
ON CONFLICT (namespace, source_ref) DO UPDATE
   SET name        = EXCLUDED.name,
       body        = EXCLUDED.body,
       source_kind = EXCLUDED.source_kind,
       occurred_at = EXCLUDED.occurred_at
RETURNING 1
"""

LINK_MEMORIES = """
UPDATE memories m
   SET episode_id = e.id
  FROM episodes e
 WHERE e.namespace = m.namespace
   AND e.source_ref = m.source_ref
   AND m.namespace = :ns
   AND m.source_ref LIKE :ref
   AND m.episode_id IS DISTINCT FROM e.id
RETURNING 1
"""

LINK_EDGES = """
UPDATE entity_edges ee
   SET episode_id = m.episode_id
  FROM memories m
 WHERE m.id = ee.memory_id
   AND ee.namespace = :ns
   AND m.episode_id IS NOT NULL
   AND ee.episode_id IS DISTINCT FROM m.episode_id
RETURNING 1
"""


def n(rows):
    return len(rows or [])


def main():
    c = brain()
    refs = c.run("""SELECT count(DISTINCT source_ref) FROM memories
                     WHERE namespace = :ns AND source_ref LIKE :ref""",
                 ns=NS, ref=REF_LIKE)[0][0]
    print(f"[episodes] namespace={NS} distinct source_refs matching {REF_LIKE!r}: {refs}")

    print(f"[episodes] upserted rows: {n(c.run(INSERT, ns=NS, ref=REF_LIKE))}")
    print(f"[episodes] memories linked (changed): {n(c.run(LINK_MEMORIES, ns=NS, ref=REF_LIKE))}")
    print(f"[episodes] edges linked (changed):    {n(c.run(LINK_EDGES, ns=NS))}")

    for label, sql in [
        ("episodes total", "SELECT count(*) FROM episodes WHERE namespace = :ns"),
        ("memories with episode_id",
         "SELECT count(*) FROM memories WHERE namespace = :ns AND episode_id IS NOT NULL"),
        ("edges with episode_id",
         "SELECT count(*) FROM entity_edges WHERE namespace = :ns AND episode_id IS NOT NULL"),
        ("edges with memory_id",
         "SELECT count(*) FROM entity_edges WHERE namespace = :ns AND memory_id IS NOT NULL"),
    ]:
        print(f"[verify] {label}: {c.run(sql, ns=NS)[0][0]}")


if __name__ == "__main__":
    main()
