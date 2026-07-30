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
   'FlowOS Portfolio: '||initcap(p.name)||
   COALESCE(' (also known as '||a.alias||'. '||a.alias||' and '||initcap(p.name)||
            ' are the SAME portfolio — any question about '||a.alias||' refers to '||initcap(p.name)||'.)','')||
   '. '||COALESCE(p.description,'')||
   '. Ventures: '||COALESCE((SELECT string_agg(v.name,', ' ORDER BY v.name) FROM ventures v WHERE v.portfolio_id=p.id),'none')
 FROM portfolios p
 LEFT JOIN (VALUES ('turif','BIV')) AS a(pid, alias) ON a.pid = p.id"""),

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

EXTRA = [
("marketing_campaigns", "flowos_marketing", "marketing", """
 SELECT m.id, COALESCE(m.updated_at,m.created_at),
   'FlowOS Marketing Campaign for venture '||COALESCE(v.name,'?')||': '||m.name||
   '. Channel: '||COALESCE(m.channel::text,'-')||'. Status: '||COALESCE(m.status::text,'-')||
   '. Budget: '||COALESCE(m.budget::text,'-')||
   COALESCE('. Runs '||to_char(m.starts_at,'YYYY-MM-DD')||' to '||to_char(m.ends_at,'YYYY-MM-DD'),'')||
   '. Owner: '||COALESCE(u.display_name,'-')||'. '||COALESCE(m.notes,'')
 FROM marketing_campaigns m LEFT JOIN ventures v ON v.id=m.venture_id LEFT JOIN users u ON u.id=m.owner_id"""),

("marketing_content", "flowos_marketing", "marketing", """
 SELECT mc.id, COALESCE(mc.updated_at,mc.created_at),
   'FlowOS Marketing Content for venture '||COALESCE(v.name,'?')||': '||mc.title||
   '. Channel: '||COALESCE(mc.channel::text,'-')||'. Status: '||COALESCE(mc.status::text,'-')||
   COALESCE('. Publish '||to_char(mc.publish_at,'YYYY-MM-DD'),'')||COALESCE('. URL: '||mc.url,'')||'. '||COALESCE(mc.notes,'')
 FROM marketing_content mc LEFT JOIN ventures v ON v.id=mc.venture_id"""),

("responsibilities", "flowos_management", "responsibility", """
 SELECT r.id, COALESCE(r.updated_at,r.created_at),
   'FlowOS Management — responsibility in venture '||COALESCE(v.name,'?')||': '||COALESCE(u.display_name,r.member_id::text)||
   ' owns '||COALESCE(r.owns,'-')||'. '||COALESCE(r.notes,'')
 FROM venture_responsibilities r LEFT JOIN ventures v ON v.id=r.venture_id
 LEFT JOIN users u ON u.id::text=r.member_id::text"""),

("okr_snapshots", "flowos_management", "okr", """
 SELECT s.id, COALESCE(s.period_end, s.period_start, now()),
   'FlowOS OKR snapshot for venture '||COALESCE(v.name,'?')||' ('||COALESCE(s.period_kind::text,'-')||' '||COALESCE(s.period_label,'-')||
   ', '||COALESCE(to_char(s.period_start,'YYYY-MM-DD'),'?')||' to '||COALESCE(to_char(s.period_end,'YYYY-MM-DD'),'?')||
   '): status '||COALESCE(s.status::text,'-')||', RAG '||COALESCE(s.rag::text,'-')||', confidence '||COALESCE(s.confidence::text,'-')||
   ', project '||COALESCE(s.project_pct::text,'-')||'%, new clients '||COALESCE(s.new_clients::text,'-')||
   '. What: '||COALESCE(s.what_happened,'')||'. Blocker: '||COALESCE(s.blocker,'none')
 FROM venture_okr_snapshots s LEFT JOIN ventures v ON v.id=s.venture_id"""),

("okr_targets", "flowos_management", "okr", """
 SELECT t.id, COALESCE(t.updated_at,t.created_at),
   'FlowOS OKR target for venture '||COALESCE(v.name,'?')||' quarter '||COALESCE(t.quarter,'-')||
   ': target clients '||COALESCE(t.target_clients::text,'-')
 FROM venture_okr_targets t LEFT JOIN ventures v ON v.id=t.venture_id"""),

("venture_metrics", "flowos_management", "metric", """
 SELECT m.id, COALESCE(m.recorded_at,m.created_at),
   'FlowOS Metric for venture '||COALESCE(v.name,'?')||' on '||to_char(COALESCE(m.recorded_at,m.created_at),'YYYY-MM-DD')||
   ': '||m.metric_key||' = '||COALESCE(m.value::text,'-')
 FROM venture_metrics m LEFT JOIN ventures v ON v.id=m.venture_id"""),

("budget", "flowos_management", "budget", """
 SELECT b.id, COALESCE(b.occurred_at,b.created_at),
   'FlowOS Budget entry for venture '||COALESCE(v.name,'?')||' on '||to_char(COALESCE(b.occurred_at,b.created_at),'YYYY-MM-DD')||
   ': '||COALESCE(b.kind::text,'-')||' '||COALESCE(b.label,'')||' amount '||COALESCE(b.amount::text,'-')
 FROM venture_budget_entries b LEFT JOIN ventures v ON v.id=b.venture_id"""),

("dependencies", "flowos_management", "dependency", """
 SELECT d.id, d.created_at,
   'FlowOS Venture dependency: '||COALESCE(a.name,'?')||' '||COALESCE(d.relation::text,'depends on')||' '||COALESCE(b2.name,'?')||
   '. '||COALESCE(d.notes,'')
 FROM venture_dependencies d LEFT JOIN ventures a ON a.id=d.from_venture_id LEFT JOIN ventures b2 ON b2.id=d.to_venture_id"""),

("pipeline_deals", "flowos_pipeline", "pipeline", """
 SELECT d.venture_id, COALESCE(d.won_at, d.next_action_due_at, now()),
   'FlowOS BD Pipeline — venture '||COALESCE(v.name,'?')||' is at stage '||COALESCE(d.pipeline_stage::text,'-')||
   '. Source: '||COALESCE(d.source_category::text,'-')||' / '||COALESCE(d.source_detail,'-')||
   '. Owner: '||COALESCE(u.display_name,'-')||'. Next action: '||COALESCE(d.next_action,'-')||
   COALESCE(' due '||to_char(d.next_action_due_at,'YYYY-MM-DD'),'')||
   COALESCE('. Won '||to_char(d.won_at,'YYYY-MM-DD'),'')||COALESCE('. Lost reason: '||d.lost_reason,'')
 FROM bd_deals d LEFT JOIN ventures v ON v.id=d.venture_id LEFT JOIN users u ON u.id::text=d.owner_user_id::text"""),

("pipeline_timeline", "flowos_pipeline", "pipeline", """
 SELECT t.id, COALESCE(t.occurred_at,t.created_at),
   'FlowOS BD timeline for venture '||COALESCE(v.name,'?')||' on '||to_char(COALESCE(t.occurred_at,t.created_at),'YYYY-MM-DD')||
   ' ['||COALESCE(t.kind::text,'note')||'] by '||COALESCE(u.display_name,'-')||': '||COALESCE(t.body,'')
 FROM bd_timeline t LEFT JOIN ventures v ON v.id=t.venture_id LEFT JOIN users u ON u.id::text=t.author_user_id::text"""),

("lean_canvas", "flowos_canvas", "canvas", """
 SELECT v.id, COALESCE(v.updated_at,v.created_at),
   'FlowOS Lean Canvas for venture '||v.name||': '||v.lean_canvas::text
 FROM ventures v WHERE v.lean_canvas IS NOT NULL AND v.lean_canvas::text NOT IN ('null','{}')"""),

("calendar", "flowos_calendar", "calendar-event", """
 SELECT e.id, COALESCE(e.start_at,e.created_at),
   'FlowOS Calendar event on '||to_char(e.start_at,'YYYY-MM-DD HH24:MI')||
   COALESCE(' to '||to_char(e.end_at,'HH24:MI'),'')||COALESCE(' for venture '||v.name,'')||
   ': '||COALESCE(e.title,'(untitled)')||'. Owner: '||COALESCE(u.display_name,'-')||
   COALESCE('. Location: '||e.location,'')||'. '||COALESCE(e.description,'')||
   COALESCE('. Attendees: '||(SELECT string_agg(au.display_name,', ') FROM calendar_event_attendees a
                              JOIN users au ON au.id=a.user_id WHERE a.event_id=e.id),'')
 FROM calendar_events e LEFT JOIN ventures v ON v.id=e.venture_id LEFT JOIN users u ON u.id=e.owner_id"""),

("transcripts", "flowos_transcript", "transcript", """
 SELECT t.job_id, COALESCE(t.backend_created_at, t.first_seen_at),
   'FlowOS Meeting transcript ('||COALESCE(t.provider,'-')||') on '||
   COALESCE(to_char(t.backend_created_at,'YYYY-MM-DD'),'unknown date')||': '||COALESCE(t.title,'(untitled)')||
   '. Status: '||COALESCE(t.status::text,'-')||'. Summary: '||COALESCE(t.summary,'')
 FROM transcript_jobs t"""),

("transcript_reports", "flowos_transcript", "transcript", """
 SELECT r.job_id, COALESCE(r.backend_created_at, r.fetched_at),
   'FlowOS Meeting transcript REPORT ('||COALESCE(r.provider,'-')||') on '||
   COALESCE(to_char(r.backend_created_at,'YYYY-MM-DD'),'unknown date')||': '||COALESCE(r.title,'(untitled)')||
   '. Summary: '||COALESCE(r.summary,'')||' '||COALESCE(r.report_md,'')
 FROM transcript_reports r"""),

("big_shift_levels", "flowos_bigshift", "transition", """
 SELECT d.level_code, COALESCE(d.published_at,d.updated_at),
   'FlowOS Big Shift transition level '||d.level_code||' (status '||COALESCE(d.status::text,'-')||'): '||COALESCE(d.body_md,'')
 FROM transition_level_docs d"""),

("big_shift_picks", "flowos_bigshift", "transition", """
 SELECT p.id, COALESCE(p.updated_at,p.created_at),
   'FlowOS Big Shift pick by '||COALESCE(u.display_name,'?')||' on '||to_char(COALESCE(p.updated_at,p.created_at),'YYYY-MM-DD')||
   ': level '||COALESCE(p.level_code,'-')||'. Intent: '||COALESCE(p.intent,'-')
 FROM transition_picks p LEFT JOIN users u ON u.id=p.user_id"""),

("activity_scores", "flowos_analytics", "activity-score", """
 SELECT a.id, a.computed_at,
   'FlowOS Analytics — activity score for '||COALESCE(u.display_name,'?')||' on '||to_char(a.computed_at,'YYYY-MM-DD')||
   ': '||COALESCE(a.kind::text,'-')||' = '||COALESCE(a.score::text,'-')||'. Reason: '||COALESCE(a.reason,'')
 FROM activity_scores a LEFT JOIN users u ON u.id=a.user_id"""),

("tasks", "flowos_task", "task", """
 SELECT t.id, COALESCE(t.updated_at,t.created_at),
   'FlowOS Task '||COALESCE(t.human_id,'')||' raised '||COALESCE(to_char(t.raised_date,'YYYY-MM-DD'),'-')||
   ': '||t.title||'. Status: '||COALESCE(t.status::text,'-')||'. Owner: '||COALESCE(u.display_name,t.owner,'-')||'. '||COALESCE(t.note,'')
 FROM tasks t LEFT JOIN users u ON u.id=t.owner_user_id"""),

("nala_approvals", "flowos_nala", "approval", """
 SELECT n.id, COALESCE(n.decided_at,n.created_at),
   'FlowOS Nala approval for venture '||COALESCE(v.name,'?')||' on '||to_char(COALESCE(n.decided_at,n.created_at),'YYYY-MM-DD')||
   ' ['||COALESCE(n.kind::text,'-')||'] status '||COALESCE(n.status::text,'-')||': '||COALESCE(n.summary,'')||
   '. Recommendation: '||COALESCE(n.recommendation,'')
 FROM nala_approvals n LEFT JOIN ventures v ON v.id=n.venture_id"""),

("session_topics", "flowos_session", "session-topic", """
 SELECT s.id, COALESCE(s.updated_at,s.created_at),
   'FlowOS Session topic ('||COALESCE(s.category::text,'-')||') proposed by '||COALESCE(u.display_name,'?')||
   ' on '||to_char(s.created_at,'YYYY-MM-DD')||': '||s.title||'. '||COALESCE(s.description,'')
 FROM session_topics s LEFT JOIN users u ON u.id=s.author_user_id"""),

("channels", "flowos_channel", "channel", """
 SELECT ch.id, COALESCE(ch.updated_at,ch.created_at),
   'FlowOS Channel #'||COALESCE(ch.slug,ch.name)||' ('||COALESCE(ch.type::text,'-')||')'||
   COALESCE(' for venture '||v.name,'')||': '||COALESCE(ch.name,'')||'. '||COALESCE(ch.description,'')
 FROM channels ch LEFT JOIN ventures v ON v.id=ch.venture_id WHERE ch.archived_at IS NULL"""),
]

QUERIES += EXTRA

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

STATE = os.path.expanduser(os.environ.get("FLOWOS_SYNC_STATE", "~/.flowos-sync.state"))

def read_since():
    """Incremental watermark. --since=auto reads the state file (realtime cron mode)."""
    for a in sys.argv[1:]:
        if a.startswith("--since="):
            v = a.split("=", 1)[1]
            if v != "auto":
                return v
            try:
                return open(STATE).read().strip() or None
            except FileNotFoundError:
                return None
    return None

def main():
    since = read_since()
    started = None
    only = [a for a in sys.argv[1:] if not a.startswith("--")] or None
    c = src(); c.run("BEGIN READ ONLY")
    started = c.run("SELECT now()::text")[0][0]     # watermark taken from the SOURCE clock
    work = []
    for name, kind, mtype, sql in QUERIES:
        if only and name not in only: continue
        # Incremental: wrap the mapping query and filter on its event-time column,
        # so one watermark works for every entity regardless of which column it uses.
        if since:
            rows = c.run(f"SELECT * FROM ({sql}) q(rid, ts, content) WHERE ts > :since", since=since)
        else:
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
    if dec.get("ERR", 0) == 0:                      # only advance the watermark on a clean run
        try:
            with open(STATE, "w") as f: f.write(started or "")
        except Exception as e: print("  (could not write state:", e, ")")

if __name__ == "__main__":
    main()
