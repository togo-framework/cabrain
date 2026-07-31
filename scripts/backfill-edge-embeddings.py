#!/usr/bin/env python3
"""
Backfill entity_edges.embedding for the CaBrain graph.

Why this matters: every edge carries a human-readable `fact` sentence ("Sara owns
Sentra"). Until that sentence is embedded, relationships are only *traversable*,
never *searchable* — you cannot ask "who owns what?" and rank edges by meaning.
Graphiti embeds edge facts; the column existed here but nothing ever wrote to it.

Two embedding routes, auto-selected:

  A. DIRECT (preferred, in-cluster). TEI (bge-m3, 1024-dim) at $TEI_EMBEDDINGS_URL.
     Batched POST /embed. Only reachable from inside the stack network
     (`tei-embed` does not resolve from a dev workspace).

  B. VIA THE HOSTED BRAIN API (works from anywhere the API is reachable).
     There is no public /embed endpoint, so we use the retain surface: POST each
     fact to /api/brain/retain in a scratch namespace with a unique source_ref,
     which makes the service embed it and store the vector; we then copy the
     vector out of `memories` into `entity_edges` and delete the scratch rows.
     NOTE: passing source_ref is what makes this exact — Store.Retain bypasses the
     §4.1 similarity write-decision when a source_ref is present, so every fact
     gets its own row instead of being collapsed as a 0.97-similar NOOP.

Idempotent: only edges with a non-empty fact AND embedding IS NULL are processed,
and the scratch namespace is cleaned out at the end (and on re-run).

Environment:
  CABRAIN_DSN            postgresql://user:pass@host:port/cabrain      (required)
  CABRAIN_NAMESPACE      graph namespace to backfill        (default: flowos)
  TEI_EMBEDDINGS_URL     e.g. http://tei-embed:80           (route A, optional)
  CABRAIN_API_URL        e.g. https://cabrain.example.com   (route B)
  CABRAIN_TOKEN          cbt_... brain token                (route B)
  EMB_BATCH              rows per DB flush                  (default: 128)
  EMB_WORKERS            parallel HTTP calls for route B    (default: 8)

Never hard-code credentials here — this file is committed.
"""
import os
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlparse

import pg8000.native as pg
import requests

NS = os.environ.get("CABRAIN_NAMESPACE", "flowos")
SCRATCH_NS = os.environ.get("EMB_SCRATCH_NS", "__edge_embed")
BATCH = int(os.environ.get("EMB_BATCH", "128"))
WORKERS = int(os.environ.get("EMB_WORKERS", "8"))
TEI_URL = os.environ.get("TEI_EMBEDDINGS_URL", "").rstrip("/")
API_URL = os.environ.get("CABRAIN_API_URL", "").rstrip("/")
TOKEN = os.environ.get("CABRAIN_TOKEN", "")
# Cloudflare blocks the default python UA, so present a normal one.
UA = "Mozilla/5.0 (X11; Linux x86_64) cabrain-backfill/1.0"


def brain():
    u = urlparse(os.environ["CABRAIN_DSN"])
    c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=u.path.lstrip("/"))
    c.run("SET search_path = public")
    return c


def veclit(v):
    return "[" + ",".join(f"{x:.7g}" for x in v) + "]"


# ---------------------------------------------------------------- route A: TEI
def tei_available():
    if not TEI_URL:
        return False
    try:
        r = requests.post(TEI_URL + "/embed", json={"inputs": ["ping"]}, timeout=5)
        return r.status_code == 200
    except Exception:
        return False


def tei_embed(texts):
    r = requests.post(TEI_URL + "/embed", json={"inputs": texts}, timeout=120)
    r.raise_for_status()
    return r.json()


# ------------------------------------------------- route B: hosted retain surface
def api_retain(edge_id, fact):
    body = {"namespace": SCRATCH_NS, "content": fact,
            "source_kind": "edge_fact", "source_ref": f"edge:{edge_id}"}
    hdr = {"Content-Type": "application/json", "User-Agent": UA,
           "X-Cabrain-Token": TOKEN}
    for attempt in range(4):
        try:
            r = requests.post(API_URL + "/api/brain/retain", json=body,
                              headers=hdr, timeout=120)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(1.5 * (attempt + 1))
    return False


def scratch_cleanup(c):
    c.run("DELETE FROM memories WHERE namespace = :ns", ns=SCRATCH_NS)
    c.run("DELETE FROM memory_events WHERE namespace = :ns", ns=SCRATCH_NS)


def main():
    c = brain()
    total, todo = c.run(
        """SELECT count(*) FILTER (WHERE fact IS NOT NULL AND fact <> ''),
                  count(*) FILTER (WHERE fact IS NOT NULL AND fact <> '' AND embedding IS NULL)
           FROM entity_edges WHERE namespace = :ns""", ns=NS)[0]
    print(f"[edges] namespace={NS} with-fact={total} needing-embedding={todo}", flush=True)
    if todo == 0:
        print("[edges] nothing to do")
        return

    use_tei = tei_available()
    if use_tei:
        print(f"[route] DIRECT TEI at {TEI_URL}", flush=True)
    else:
        if not (API_URL and TOKEN):
            sys.exit("no embedding route: TEI unreachable and CABRAIN_API_URL/"
                     "CABRAIN_TOKEN unset. Run this in-cluster, or set the API vars.")
        print(f"[route] hosted brain API at {API_URL} "
              f"(scratch namespace {SCRATCH_NS}, {WORKERS} workers)", flush=True)
        scratch_cleanup(c)

    done = failed = 0
    t0 = time.time()
    while True:
        rows = c.run(
            """SELECT id::text, fact FROM entity_edges
               WHERE namespace = :ns AND fact IS NOT NULL AND fact <> ''
                 AND embedding IS NULL
               ORDER BY id LIMIT :lim""", ns=NS, lim=BATCH)
        if not rows:
            break

        if use_tei:
            vecs = tei_embed([f for _, f in rows])
            for (eid, _), v in zip(rows, vecs):
                c.run("UPDATE entity_edges SET embedding = CAST(:v AS vector) WHERE id = CAST(:id AS uuid)",
                      v=veclit(v), id=eid)
                done += 1
        else:
            with ThreadPoolExecutor(max_workers=WORKERS) as ex:
                oks = list(ex.map(lambda r: api_retain(r[0], r[1]), rows))
            # Copy the vectors the service just computed onto the edges.
            n = c.run(
                """UPDATE entity_edges e SET embedding = m.embedding
                     FROM memories m
                    WHERE m.namespace = :sns
                      AND m.source_ref = 'edge:' || e.id::text
                      AND m.embedding IS NOT NULL
                      AND e.namespace = :ns AND e.embedding IS NULL
                RETURNING 1""", sns=SCRATCH_NS, ns=NS)
            moved = len(n or [])
            scratch_cleanup(c)
            done += moved
            failed += len(rows) - moved
            if moved == 0:
                print(f"[edges] batch produced 0 vectors (ok={sum(oks)}/{len(rows)}) — aborting")
                break

        el = time.time() - t0
        print(f"[edges] {done}/{todo} embedded  failed={failed}  "
              f"{done/max(el,1e-9):.1f}/s  {el:.0f}s", flush=True)

    if not use_tei:
        scratch_cleanup(c)
    left = c.run("""SELECT count(*) FROM entity_edges
                    WHERE namespace = :ns AND fact IS NOT NULL AND fact <> ''
                      AND embedding IS NULL""", ns=NS)[0][0]
    print(f"[edges] DONE embedded={done} failed={failed} still-null={left}")


if __name__ == "__main__":
    main()
