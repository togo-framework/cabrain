#!/usr/bin/env python3
"""
Connect the `repo` island to the rest of the flowos graph.

Everything already hangs off repos — doc --DOC_IN--> repo (420),
code_module --CODE_IN--> repo (222), person --COMMITS_TO--> repo (584) — but only
22 of 470 repos had an edge to a venture, so the graph could not answer
"show me the code/docs/people for venture X". Traversal died at the repo.

This links repos upward using four independent signals, strongest first, and
records which one produced each edge in metadata.via so a wrong link is
traceable rather than mysterious:

  1. ventures.github_url          — explicit, authoritative
  2. github_orgs.venture_id       — an org dedicated to one venture
  3. venture slug / name == repo name — SADA's slug is literally "aeroplane"
  4. github_orgs.portfolio_id     — org-level home for everything else

Idempotent: valid_from is part of entity_edges' unique key, so a plain upsert
would add a second live edge on every run. We check for an existing OPEN edge
(valid_to IS NULL) first.

Credentials come from the environment only:
  FLOWOS_DSN   postgresql://…/onestudio_hub   (READ-ONLY; every read is in a tx)
  CABRAIN_DSN  postgresql://…/cabrain
"""
import os
import re
import sys
from urllib.parse import urlparse

import pg8000.native as pg

NS = os.environ.get("FLOWOS_NAMESPACE", "flowos")


def conn(dsn):
    u = urlparse(dsn)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=u.path.lstrip("/"))


def norm(s):
    """Fold a repo/venture name to a comparable key: lowercase, alnum only."""
    return re.sub(r"[^a-z0-9]", "", (s or "").lower())


def main():
    s = conn(os.environ["FLOWOS_DSN"])
    b = conn(os.environ["CABRAIN_DSN"])
    b.run("SET search_path = public")
    s.run("BEGIN READ ONLY")

    ventures = s.run("SELECT id, name, github_url FROM ventures")
    orgs = s.run("SELECT slug, venture_id, portfolio_id FROM github_orgs")
    portfolios = {r[0]: r[1] for r in s.run("SELECT id, initcap(name) FROM portfolios")}
    vnames = {r[0]: r[1] for r in ventures}
    s.run("ROLLBACK")

    # brain entity ids, keyed the way each type is named
    ent_repo = {r[0].lower(): r[1] for r in b.run(
        "SELECT name, id FROM entities WHERE namespace=:ns AND entity_type='repo'", ns=NS)}
    ent_vent = {r[0]: r[1] for r in b.run(
        "SELECT name, id FROM entities WHERE namespace=:ns AND entity_type='venture'", ns=NS)}
    # Portfolio entities may carry a "(portfolio)" suffix: the graph builder appends
    # the type to disambiguate a name shared with another entity ("One Studio" is
    # both a portfolio and a venture). Key on the normalised name so the lookup
    # still hits — missing this stranded all 144 onestudio-* repos.
    ent_port = {norm(r[0].replace("(portfolio)", "")): r[1] for r in b.run(
        "SELECT name, id FROM entities WHERE namespace=:ns AND entity_type='portfolio'", ns=NS)}

    # ── resolve repo -> (dst entity, relation, fact, via) ────────────────────
    plan = {}   # repo_full_name(lower) -> tuple

    def claim(repo, dst_id, rel, fact, via):
        if repo in ent_repo and dst_id and repo not in plan:
            plan[repo] = (dst_id, rel, fact, via)

    # 1. explicit github_url on the venture
    for vid, vname, url in ventures:
        if not url:
            continue
        m = re.search(r"github\.com[:/]+([^/\s]+)/([^/\s#?]+)", url)
        if not m:
            continue
        repo = f"{m.group(1)}/{m.group(2)}".lower().removesuffix(".git")
        claim(repo, ent_vent.get(vname), "REPO_OF",
              f"{repo} is the source repository of the {vname} venture", "ventures.github_url")

    # 2. an org dedicated to a single venture
    org_vent = {o[0].lower(): o[1] for o in orgs if o[1]}
    org_port = {o[0].lower(): o[2] for o in orgs if o[2]}
    for repo in ent_repo:
        org = repo.split("/", 1)[0]
        vid = org_vent.get(org)
        if vid and vid in vnames:
            claim(repo, ent_vent.get(vnames[vid]), "REPO_OF",
                  f"{repo} belongs to the {vnames[vid]} venture (its GitHub org is dedicated to it)",
                  "github_orgs.venture_id")

    # 3. repo name == venture slug or venture name
    by_key = {}
    for vid, vname, _ in ventures:
        for k in (norm(vid), norm(vname)):
            if len(k) >= 4:
                by_key.setdefault(k, vname)
    for repo in ent_repo:
        vname = by_key.get(norm(repo.split("/", 1)[1]))
        if vname:
            claim(repo, ent_vent.get(vname), "REPO_OF",
                  f"{repo} is the repository for the {vname} venture (name match)", "name-match")

    # 4. fall back to the org's portfolio, so no repo is left stranded
    for repo in ent_repo:
        pid = org_port.get(repo.split("/", 1)[0])
        pname = portfolios.get(pid)
        if pname:
            claim(repo, ent_port.get(norm(pname)), "REPO_IN",
                  f"{repo} sits in the {pname} portfolio (via its GitHub org)",
                  "github_orgs.portfolio_id")

    # ── write, skipping any repo that already has an open edge ───────────────
    written, skipped = 0, 0
    by_via = {}
    for repo, (dst, rel, fact, via) in plan.items():
        src = ent_repo[repo]
        if b.run("""SELECT 1 FROM entity_edges WHERE namespace=:ns AND src_id=:s
                    AND dst_id=:d AND relation=:r AND valid_to IS NULL LIMIT 1""",
                 ns=NS, s=src, d=dst, r=rel):
            skipped += 1
            continue
        b.run("""INSERT INTO entity_edges (namespace,src_id,dst_id,relation,fact,valid_from,metadata)
                 VALUES (:ns,:s,:d,:r,:f, now(), jsonb_build_object('derivation','fk','via',:v::text))
                 ON CONFLICT (namespace,src_id,dst_id,relation,valid_from) DO NOTHING""",
              ns=NS, s=src, d=dst, r=rel, f=fact, v=via)
        written += 1
        by_via[via] = by_via.get(via, 0) + 1

    print(f"repos in graph: {len(ent_repo)}   resolved: {len(plan)}   "
          f"written: {written}   already linked: {skipped}")
    for via, n in sorted(by_via.items(), key=lambda x: -x[1]):
        print(f"   via {via:26} {n}")
    unresolved = len(ent_repo) - len(plan)
    if unresolved:
        print(f"   NOT linked: {unresolved} (no github_url, no org mapping, no name match)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
