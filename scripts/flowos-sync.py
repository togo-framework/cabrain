#!/usr/bin/env python3
"""
Rebuild the `flowos` CaBrain brain from the FlowOS production DB (onestudio_hub).

Design notes
------------
* SEMANTIC entities only. High-volume event tables (feature_events 465k,
  transition_events 95k, claude_activity_events 59k, github_commits 27k) are
  deliberately NOT embedded — they are for counting, not semantic recall, and
  embedding them is what drowned this brain before. They belong in SQL/DuckDB.
* Every memory carries its EVENT TIME, both as a human-readable date inside the
  content (so the model can reason about it) and in valid_at (so the store can
  order it). This is what makes "latest feeds / last goals" answerable.
* source_ref is stable (`db:<entity>:<id>`), so re-running UPDATEs/NOOPs instead
  of creating duplicates.
* Read-only against prod: every query runs inside BEGIN READ ONLY.
"""
import json, os, subprocess, sys, time
from urllib.parse import urlparse
from concurrent.futures import ThreadPoolExecutor
import pg8000.native as pg

CABRAIN = os.environ.get("CABRAIN_API_URL", "https://cabrain.fadymondy.com")
TOK     = os.environ["CABRAIN_TOKEN"]                    # required
NS      = os.environ.get("FLOWOS_NAMESPACE", "flowos")
# FlowOS prod is on a private net — open a tunnel first, e.g.
#   ssh -L 15433:10.10.10.30:5432 root@<proxmox-host>
# then export FLOWOS_DSN=postgresql://flowos:***@localhost:15433/onestudio_hub
# Credentials live in the think-os vault as `flowos_prod_db` (secret_reveal).
FLOWOS_DSN = os.environ["FLOWOS_DSN"]                    # required

def src():
    """Read-only connection to FlowOS prod (via your SSH tunnel)."""
    u = urlparse(FLOWOS_DSN)
    return pg.Connection(user=u.username, password=u.password, host=u.hostname,
                         port=u.port or 5432, database=u.path.lstrip("/"))

def d(x):
    return x.strftime("%Y-%m-%d") if x else "unknown date"

def clean(s, n=1500):
    if not s: return ""
    s = " ".join(str(s).split())
    return s[:n]

# (name, source_kind, mem_type, SQL) -> rows of (ref_id, content, event_time)
QUERIES = [
("posts", "flowos_post", "post", """
 SELECT p.id, p.created_at,
   'FlowOS Post ['||COALESCE(p.type::text,'status')||'] on '||to_char(p.created_at,'YYYY-MM-DD')||
   ' by '||COALESCE(u.display_name,'unknown')||
   COALESCE(' in venture '||v.name,'')||': '||COALESCE(NULLIF(p.title,''),'(untitled)')||'. '||
   COALESCE(p.body_md,'')
 FROM posts p LEFT JOIN users u ON u.id=p.author_id LEFT JOIN ventures v ON v.id=p.venture_id"""),

("issues", "flowos_issue", "issue", """
 SELECT i.id, i.created_at,
   'FlowOS Issue #'||i.number||COALESCE(' in venture '||v.name,'')||' opened '||to_char(i.created_at,'YYYY-MM-DD')||
   COALESCE(', closed '||to_char(i.closed_at,'YYYY-MM-DD'),'')||': '||i.title||
   '. Type: '||COALESCE(i.type::text,'-')||'. Status: '||COALESCE(i.status::text,'-')||'. Priority: '||COALESCE(i.priority::text,'-')||
   '. Area: '||COALESCE(i.area::text,'-')||'. Assignee: '||COALESCE(a.display_name,'unassigned')||'. '||COALESCE(i.description,'')
 FROM issues i LEFT JOIN ventures v ON v.id=i.venture_id LEFT JOIN users a ON a.id=i.assignee_id"""),

("user_goals", "flowos_goal", "goal", """
 SELECT g.id, COALESCE(g.set_at,g.created_at),
   'FlowOS Goal (personal) of '||COALESCE(u.display_name,'unknown')||' set '||to_char(COALESCE(g.set_at,g.created_at),'YYYY-MM-DD')||
   COALESCE(', deadline '||to_char(g.deadline_at,'YYYY-MM-DD'),'')||
   COALESCE(', resolved '||to_char(g.resolved_at,'YYYY-MM-DD'),'')||
   ': '||g.title||'. Status: '||COALESCE(g.status::text,'-')||'. '||COALESCE(g.resolution_note,'')
 FROM user_goals g LEFT JOIN users u ON u.id=g.user_id"""),

("venture_goals", "flowos_goal", "goal", """
 SELECT g.id, COALESCE(g.set_at,g.created_at),
   'FlowOS Goal (venture '||COALESCE(v.name,'?')||') set '||to_char(COALESCE(g.set_at,g.created_at),'YYYY-MM-DD')||
   COALESCE(', deadline '||to_char(g.deadline_at,'YYYY-MM-DD'),'')||
   COALESCE(', resolved '||to_char(g.resolved_at,'YYYY-MM-DD'),'')||
   ': '||g.title||'. Status: '||COALESCE(g.status::text,'-')||'. '||COALESCE(g.resolution_note,'')
 FROM venture_goals g LEFT JOIN ventures v ON v.id=g.venture_id"""),

("learnings", "flowos_learning", "learning", """
 SELECT l.id, l.created_at,
   'FlowOS Learning ('||COALESCE(l.scope::text,'general')||')'||COALESCE(' in venture '||v.name,'')||
   ' recorded '||to_char(l.created_at,'YYYY-MM-DD')||': '||l.title||'. Status: '||COALESCE(l.status::text,'-')||
   '. Classification: '||COALESCE(l.classification::text,'-')||'. '||COALESCE(l.body,'')
 FROM issue_learnings l LEFT JOIN ventures v ON v.id=l.venture_id"""),

("roadmaps", "flowos_roadmap", "roadmap", """
 SELECT r.id, r.created_at,
   'FlowOS Roadmap ('||COALESCE(r.cadence::text,'-')||') for venture '||COALESCE(v.name,'?')||
   ' period '||COALESCE(to_char(r.period_start,'YYYY-MM-DD'),'?')||' to '||COALESCE(to_char(r.period_end,'YYYY-MM-DD'),'?')||
   ', status '||COALESCE(r.status::text,'-')||'. Focus: '||COALESCE(r.focus::text,'')||'. Blocked: '||COALESCE(r.blocked::text,'none')||
   '. Notes: '||COALESCE(r.notes::text,'')
 FROM roadmaps r LEFT JOIN ventures v ON v.id=r.venture_id"""),

("releases", "flowos_release", "release", """
 SELECT rel.id, rel.created_at,
   'FlowOS Release '||COALESCE(rel.version,'?')||' of venture '||COALESCE(v.name,'?')||
   ' on '||to_char(rel.created_at,'YYYY-MM-DD')||'. Notes: '||COALESCE(rel.notes,'')
 FROM releases rel LEFT JOIN ventures v ON v.id=rel.venture_id"""),

("people", "flowos_person", "person", """
 SELECT u.id, u.created_at,
   'FlowOS Person: '||COALESCE(u.display_name,u.email)||' (slug: '||COALESCE(u.slug,'-')||'). Email: '||u.email||
   '. Position: '||COALESCE(NULLIF(u.position,''),'-')||'. Admin: '||u.is_admin||
   '. Account type: '||COALESCE(u.account_type::text,'-')||'. Joined '||to_char(u.created_at,'YYYY-MM-DD')||
   COALESCE('. GitHub: '||array_to_string(u.github_usernames,', '),'')||
   COALESCE('. Offboarded '||to_char(u.offboarded_at,'YYYY-MM-DD'),'')
 FROM users u WHERE u.deleted_at IS NULL"""),

("ventures", "flowos_venture", "venture", """
 SELECT v.id, COALESCE(v.updated_at,v.created_at),
   'FlowOS Venture: '||v.name||' (slug: '||v.id||'). Portfolio: '||COALESCE(initcap(p.name),'(none)')||
   '. Stage: '||COALESCE(v.stage::text,'-')||'. Kind: '||COALESCE(v.kind::text,'-')||
   '. On-hold: '||COALESCE(v.on_hold::text,'false')||'. Created '||to_char(v.created_at,'YYYY-MM-DD')||
   '. Tagline: '||COALESCE(v.tagline,'')||'. North star: '||COALESCE(v.north_star,'')||'. '||COALESCE(v.description,'')||
   COALESCE(' Website: '||v.website_url,'')||
   COALESCE('. Team: '||(SELECT string_agg(u.display_name,', ') FROM venture_members vm JOIN users u ON u.id::text=vm.member_id::text WHERE vm.venture_id=v.id AND vm.member_type::text='user'),'')
 FROM ventures v LEFT JOIN portfolios p ON p.id=v.portfolio_id"""),

("portfolios", "flowos_portfolio", "portfolio", """
 SELECT p.id, p.created_at,
   'FlowOS Portfolio: '||initcap(p.name)||'. '||COALESCE(p.description,'')||
   '. Ventures: '||COALESCE((SELECT string_agg(v.name,', ' ORDER BY v.name) FROM ventures v WHERE v.portfolio_id=p.id),'none')
 FROM portfolios p"""),

("decisions", "flowos_decision", "decision", """
 SELECT d.id, COALESCE(d.decided_at,d.created_at),
   'FlowOS Decision for venture '||COALESCE(v.name,'?')||' on '||to_char(COALESCE(d.decided_at,d.created_at),'YYYY-MM-DD')||
   ': '||d.title||'. Context: '||COALESCE(d.context,'')||'. Decision: '||COALESCE(d.decision,'')
 FROM venture_decisions d LEFT JOIN ventures v ON v.id=d.venture_id"""),

("harvested", "flowos_harvested", "harvested", """
 SELECT h.id, h.created_at,
   'FlowOS Harvested research on '||to_char(h.created_at,'YYYY-MM-DD')||': '||COALESCE(h.title,h.url)||
   '. Why it matters: '||COALESCE(h.why_it_matters,'')||'. Summary: '||COALESCE(h.summary,'')||'. URL: '||COALESCE(h.url,'')
 FROM harvester_items h WHERE h.status::text <> 'rejected' OR h.status IS NULL"""),

("agents", "flowos_agent", "agent", """
 SELECT a.id, a.updated_at,
   'FlowOS Domain-Expert Agent: '||a.name||COALESCE(' / '||a.name_ar,'')||' (slug: '||a.id||'). Domain: '||COALESCE(a.domain,'-')||
   '. Position: '||COALESCE(a.position,'-')||'. Status: '||COALESCE(a.status::text,'-')||'. Model: '||COALESCE(a.model,'-')||
   '. Health: '||COALESCE(a.health::text,'-')||'. '||COALESCE(a.headline,'')||' '||COALESCE(a.description,'')
 FROM agents a"""),
]

def retain(item):
    ref, content, kind, mtype, ts = item
    body = {"namespace": NS, "content": content, "sourceKind": kind,
            "sourceRef": ref, "metadata": {"type": mtype}, "importanceHint": 0.6}
    if ts: body["validAt"] = ts.isoformat()
    p = subprocess.run(["curl","-s","--max-time","120","-X","POST",f"{CABRAIN}/api/brain/retain",
        "-H","Content-Type: application/json","-H",f"X-Cabrain-Token: {TOK}",
        "-H","X-Agent-Id: flowos-rebuild","-d",json.dumps(body)],capture_output=True)
    try:
        return json.loads(p.stdout).get("decision","?")
    except Exception:
        return "ERR"

def main():
    only = sys.argv[1:] or None
    c = src(); c.run("BEGIN READ ONLY")
    work = []
    for name, kind, mtype, sql in QUERIES:
        if only and name not in only: continue
        rows = c.run(sql)
        for r in rows:
            rid, ts, content = r[0], r[1], r[2]
            if not content: continue
            work.append((f"db:{mtype}:{rid}", clean(content), kind, mtype, ts))
        print(f"  {name:12} {len(rows):>5} rows", flush=True)
    c.run("ROLLBACK")
    print(f"\ntotal memories to write: {len(work)}", flush=True)
    t0=time.time(); done=0
    from collections import Counter
    dec=Counter()
    with ThreadPoolExecutor(max_workers=8) as ex:
        for r in ex.map(retain, work):
            dec[r]+=1; done+=1
            if done % 200 == 0: print(f"   {done}/{len(work)}  ({time.time()-t0:.0f}s)  {dict(dec)}", flush=True)
    print(f"\nDONE {done} in {time.time()-t0:.0f}s -> {dict(dec)}")

if __name__ == "__main__":
    main()
