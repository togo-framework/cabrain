#!/usr/bin/env python3
"""
Populate the CaBrain ENTITY GRAPH (entities + memory_entities) for the flowos brain
from the FlowOS production foreign keys.

Why this matters: Store.expandEntities does 1-hop "spreading activation" — after
recall it pulls in memories that share an entity with the top hits, tagged
via_entity. Both graph tables are EMPTY today, so that whole feature has always
been a no-op. Cognee was supposed to fill it by LLM extraction; the relational DB
already knows the true relationships, so we build them exactly instead of guessing.

Entities = the nouns of FlowOS: ventures, people, portfolios, agents.
Edges     = memory -> every entity the source row actually references (author,
            venture, assignee, portfolio...), derived from real FKs.

Idempotent: entities upsert on (namespace,name); edges upsert on the PK.
"""
import os
from urllib.parse import urlparse
import pg8000.native as pg
from collections import defaultdict

NS = os.environ.get("FLOWOS_NAMESPACE", "flowos")
# Both DSNs come from env; the FlowOS one is vaulted in think-os as `flowos_prod_db`.
#   FLOWOS_DSN  = postgresql://flowos:***@<tunnel-or-host>:5432/onestudio_hub   (READ ONLY)
#   CABRAIN_DSN = postgresql://cabrain:***@<host>:5432/cabrain
def _conn(dsn):
    u = urlparse(dsn)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=u.path.lstrip("/"))
def src():   return _conn(os.environ["FLOWOS_DSN"])
def brain(): return _conn(os.environ["CABRAIN_DSN"])

s, b = src(), brain()
s.run("BEGIN READ ONLY")
b.run("SET search_path = public")

# ---- 1. entity catalogue from prod -------------------------------------------------
ventures   = {r[0]: r[1] for r in s.run("SELECT id, name FROM ventures")}
people     = {r[0]: (r[1] or r[2]) for r in s.run("SELECT id, display_name, email FROM users WHERE deleted_at IS NULL")}
portfolios = {r[0]: r[1] for r in s.run("SELECT id, initcap(name) FROM portfolios")}
agents     = {r[0]: r[1] for r in s.run("SELECT id, name FROM agents")}

# name -> canonical label, disambiguating collisions across types
label = {}
for kind, table in (("venture",ventures), ("person",people), ("portfolio",portfolios), ("agent",agents)):
    for k, v in table.items():
        nm = (v or str(k)).strip()
        key = (kind, k)
        label[key] = nm
counts = defaultdict(list)
for key, nm in label.items(): counts[nm].append(key)
for nm, keys in counts.items():
    if len(keys) > 1:                       # same display name used by 2 types -> qualify
        for kind, k in keys: label[(kind,k)] = f"{nm} ({kind})"

# ---- 2. upsert entities ------------------------------------------------------------
ent_id = {}
for key, nm in label.items():
    r = b.run("""INSERT INTO entities (namespace, name) VALUES (:ns, :n)
                 ON CONFLICT (namespace, name) DO UPDATE SET name = EXCLUDED.name
                 RETURNING id""", ns=NS, n=nm)
    ent_id[key] = r[0][0]
print(f"  entities upserted: {len(ent_id)}  (ventures {len(ventures)}, people {len(people)}, portfolios {len(portfolios)}, agents {len(agents)})")

# ---- 3. source_ref -> [entity keys] from real FKs -----------------------------------
links = defaultdict(set)   # source_ref -> {(kind,id)}
def add(ref, kind, ident):
    if ident is not None and (kind, ident) in ent_id: links[ref].add((kind, ident))

for vid, pid in s.run("SELECT id, portfolio_id FROM ventures"):
    add(f"db:venture:{vid}", "venture", vid); add(f"db:venture:{vid}", "portfolio", pid)
for uid, in s.run("SELECT id FROM users WHERE deleted_at IS NULL"):
    add(f"db:person:{uid}", "person", uid)
for pid, in s.run("SELECT id FROM portfolios"):
    add(f"db:portfolio:{pid}", "portfolio", pid)
for aid, in s.run("SELECT id FROM agents"):
    add(f"db:agent:{aid}", "agent", aid)
for i, v, a in s.run("SELECT id, venture_id, author_id FROM posts"):
    add(f"db:post:{i}", "venture", v); add(f"db:post:{i}", "person", a)
for i, v, a in s.run("SELECT id, venture_id, assignee_id FROM issues"):
    add(f"db:issue:{i}", "venture", v); add(f"db:issue:{i}", "person", a)
for i, u in s.run("SELECT id, user_id FROM user_goals"):
    add(f"db:goal:{i}", "person", u)
for i, v in s.run("SELECT id, venture_id FROM venture_goals"):
    add(f"db:goal:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM issue_learnings"):
    add(f"db:learning:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM roadmaps"):
    add(f"db:roadmap:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM releases"):
    add(f"db:release:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM venture_decisions"):
    add(f"db:decision:{i}", "venture", v)
for i, v, a in s.run("SELECT id, linked_venture_id, linked_agent_id FROM harvester_items"):
    add(f"db:harvested:{i}", "venture", v); add(f"db:harvested:{i}", "agent", a)

# --- new entity types added in the tab-coverage pass -------------------------------
for i, v in s.run("SELECT id, venture_id FROM marketing_campaigns"):
    add(f"db:marketing:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM marketing_content"):
    add(f"db:marketing:{i}", "venture", v)
for i, v, m in s.run("SELECT id, venture_id, member_id FROM venture_responsibilities"):
    add(f"db:responsibility:{i}", "venture", v); add(f"db:responsibility:{i}", "person", m)
for i, v in s.run("SELECT id, venture_id FROM venture_okr_snapshots"):
    add(f"db:okr:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM venture_okr_targets"):
    add(f"db:okr:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM venture_metrics"):
    add(f"db:metric:{i}", "venture", v)
for i, v in s.run("SELECT id, venture_id FROM venture_budget_entries"):
    add(f"db:budget:{i}", "venture", v)
for i, a2, b2 in s.run("SELECT id, from_venture_id, to_venture_id FROM venture_dependencies"):
    add(f"db:dependency:{i}", "venture", a2); add(f"db:dependency:{i}", "venture", b2)
for v, o in s.run("SELECT venture_id, owner_user_id FROM bd_deals"):
    add(f"db:pipeline:{v}", "venture", v); add(f"db:pipeline:{v}", "person", o)
for i, v, au in s.run("SELECT id, venture_id, author_user_id FROM bd_timeline"):
    add(f"db:pipeline:{i}", "venture", v); add(f"db:pipeline:{i}", "person", au)
for v, in s.run("SELECT id FROM ventures WHERE lean_canvas IS NOT NULL"):
    add(f"db:canvas:{v}", "venture", v)
for i, v, o in s.run("SELECT id, venture_id, owner_id FROM calendar_events"):
    add(f"db:calendar-event:{i}", "venture", v); add(f"db:calendar-event:{i}", "person", o)
for ev, u in s.run("SELECT event_id, user_id FROM calendar_event_attendees"):
    add(f"db:calendar-event:{ev}", "person", u)
for i, u in s.run("SELECT id, user_id FROM transition_picks"):
    add(f"db:transition:{i}", "person", u)
for i, u in s.run("SELECT id, user_id FROM activity_scores"):
    add(f"db:activity-score:{i}", "person", u)
for i, v in s.run("SELECT id, venture_id FROM channels"):
    add(f"db:channel:{i}", "venture", v)
for i, u in s.run("SELECT id, owner_user_id FROM tasks"):
    add(f"db:task:{i}", "person", u)
for i, v in s.run("SELECT id, venture_id FROM nala_approvals"):
    add(f"db:approval:{i}", "venture", v)
for i, u in s.run("SELECT id, author_user_id FROM session_topics"):
    add(f"db:session-topic:{i}", "person", u)
s.run("ROLLBACK")
print(f"  source_refs with at least one entity: {len(links)}")

# ---- 4. resolve source_ref -> live memory ids, write edges --------------------------
mem = defaultdict(list)
for mid, ref in b.run("""SELECT id::text, source_ref FROM memories
                         WHERE namespace=:ns AND invalid_at IS NULL AND source_ref LIKE 'db:%'""", ns=NS):
    mem[ref].append(mid)
print(f"  live memories with a db: source_ref: {sum(len(v) for v in mem.values())}")

edges = 0; matched = 0
for ref, keys in links.items():
    ids = mem.get(ref)
    if not ids: continue
    matched += 1
    for mid in ids:
        for k in keys:
            b.run("""INSERT INTO memory_entities (memory_id, entity_id) VALUES (:m::uuid, :e)
                     ON CONFLICT DO NOTHING""", m=mid, e=ent_id[k])
            edges += 1
print(f"  memories linked: {matched}   edges written: {edges}")
print("  totals ->", b.run("SELECT (SELECT count(*) FROM entities), (SELECT count(*) FROM memory_entities)"))
