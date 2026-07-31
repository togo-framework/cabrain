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

# learning_events (396 rows) is NOT its own memory type: the events are curator
# bookkeeping ("added tags: x,y,z", "normalized classification to X") that only
# mean anything attached to the learning they mutated. Folding them in costs ZERO
# extra memories and hands recall the curator's keyword tags, which is the single
# most useful thing in that table. The event time then becomes the learning's
# LAST revision, so an incremental run re-emits a learning when the curator
# touches it (its own created_at never moves).
("learnings", "flowos_learning", "learning", """
 SELECT l.id, COALESCE(ev.last_at, l.created_at),
   'FlowOS Learning ('||COALESCE(l.scope::text,'general')||')'||COALESCE(' in venture '||v.name,'')||
   ' recorded '||to_char(l.created_at,'YYYY-MM-DD')||': '||l.title||'. Status: '||COALESCE(l.status::text,'-')||
   '. Classification: '||COALESCE(l.classification::text,'-')||'. '||COALESCE(l.body,'')||
   COALESCE(' How this learning evolved: '||ev.trail,'')
 FROM issue_learnings l LEFT JOIN ventures v ON v.id=l.venture_id
 LEFT JOIN LATERAL (
   SELECT max(e.created_at) AS last_at,
          string_agg(to_char(e.created_at,'YYYY-MM-DD')||' '||e.event_type::text||
                     COALESCE(' by '||COALESCE(eu.display_name,e.actor_agent_id,e.actor_type),'')||
                     COALESCE(' — '||e.reason,''), '; ' ORDER BY e.created_at) AS trail
   FROM learning_events e LEFT JOIN users eu ON eu.id=e.actor_user_id
   WHERE e.learning_id = l.id) ev ON true"""),

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
   COALESCE('. Offboarded '||to_char(u.offboarded_at,'YYYY-MM-DD'),'')||
   -- Which ventures this person actually works on, and what they own. Without this
   -- a person memory is an isolated card and "who works on X" / "what does Y do"
   -- can only be answered by the graph, never by recall.
   COALESCE('. Works on: '||(SELECT string_agg(v.name||COALESCE(' ('||vm.role::text||')',''), ', '
                             ORDER BY (vm.role::text='cofounder') DESC, v.name)
                             FROM venture_members vm JOIN ventures v ON v.id=vm.venture_id
                             WHERE vm.member_id::text=u.id::text AND vm.member_type::text='human'),'')||
   COALESCE('. Owns: '||(SELECT string_agg(vr.owns||' in '||v.name,', ' ORDER BY v.name)
                         FROM venture_responsibilities vr JOIN ventures v ON v.id=vr.venture_id
                         WHERE vr.member_id::text=u.id::text AND NULLIF(vr.owns,'') IS NOT NULL),'')
 FROM users u WHERE u.deleted_at IS NULL"""),

("ventures", "flowos_venture", "venture", """
 SELECT v.id, COALESCE(v.updated_at,v.created_at),
   'FlowOS Venture: '||v.name||' (slug: '||v.id||'). Portfolio: '||COALESCE(initcap(p.name),'(none)')||
   '. Stage: '||COALESCE(v.stage::text,'-')||'. Kind: '||COALESCE(v.kind::text,'-')||
   '. On-hold: '||COALESCE(v.on_hold::text,'false')||'. Created '||to_char(v.created_at,'YYYY-MM-DD')||
   '. Tagline: '||COALESCE(v.tagline,'')||'. North star: '||COALESCE(v.north_star,'')||'. '||COALESCE(v.description,'')||
   COALESCE(' Website: '||v.website_url,'')||
   -- member_type is the enum 'human'/'agent'; it is never 'user'. Matching 'user'
   -- silently dropped every team from every venture memory, which is why questions
   -- about the team / هيكل الشركة never resolved. Cofounders lead, then members.
   COALESCE('. Team: '||(SELECT string_agg(u.display_name||COALESCE(' ('||vm.role::text||')',''), ', '
                                           ORDER BY (vm.role::text='cofounder') DESC, u.display_name)
                         FROM venture_members vm JOIN users u ON u.id::text=vm.member_id::text
                         WHERE vm.venture_id=v.id AND vm.member_type::text='human'
                           AND u.deleted_at IS NULL),'')||
   COALESCE('. Agents on this venture: '||(SELECT string_agg(a.name,', ' ORDER BY a.name)
                         FROM venture_members vm JOIN agents a ON a.id::text=vm.member_id::text
                         WHERE vm.venture_id=v.id AND vm.member_type::text='agent'),'')
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

# harvester_submissions (122) folded in as attribution — "who brought this in, via
# which channel" is the only knowledge it carries and it is worthless detached
# from the item. Zero extra memories.
("harvested", "flowos_harvested", "harvested", """
 SELECT h.id, h.created_at,
   'FlowOS Harvested research on '||to_char(h.created_at,'YYYY-MM-DD')||': '||COALESCE(h.title,h.url)||
   '. Why it matters: '||COALESCE(h.why_it_matters,'')||'. Summary: '||COALESCE(h.summary,'')||'. URL: '||COALESCE(h.url,'')||
   COALESCE('. Submitted by '||sub.who,'')
 FROM harvester_items h
 LEFT JOIN LATERAL (
   SELECT string_agg(DISTINCT COALESCE(su.display_name,'someone')||' via '||COALESCE(s.channel::text,'-'), ', ') AS who
   FROM harvester_submissions s LEFT JOIN users su ON su.id=s.submitter_id
   WHERE s.item_id = h.id) sub ON true
 WHERE h.status::text <> 'rejected' OR h.status IS NULL"""),

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

# ---------------------------------------------------------------------------
# GAP_FILL — the knowledge-bearing tables that were NOT covered above.
#
# Every one of these is a ROLLUP. The raw tables hold 2,339 issue comments,
# 3,888 agent actions and 1,948 pull requests; emitting one memory per row is
# exactly what drowned this brain before (2,177 github-activity memories had to
# be excluded from recall). Each query below collapses a table to the grain a
# QUESTION is actually asked at — per issue thread, per agent-week, per chat,
# per repo — so the answer arrives in ONE hit instead of forty fragments.
#
# The grain is also the source_ref, so re-runs UPDATE in place and the whole set
# stays at a fixed size no matter how many rows arrive underneath it.
# ---------------------------------------------------------------------------
GAP_FILL = [

# issue_comments 2,339 -> one digest per ISSUE (~670). "What was decided on
# issue X" needs the thread, not 40 loose comments. issue_status_history (2,099)
# and issue_links (295) fold in here as the lifecycle/links line — they are
# unanswerable on their own and cost no extra memories.
("issue_threads", "flowos_issue_thread", "issue-thread", """
 SELECT i.id, t.last_at,
   'FlowOS Issue discussion thread — Issue #'||i.number||COALESCE(' in venture '||v.name,'')||': '||i.title||
   '. Issue status: '||COALESCE(i.status::text,'-')||', type '||COALESCE(i.type::text,'-')||
   ', assignee '||COALESCE(asg.display_name,'unassigned')||
   '. '||t.n||' substantive comment(s) from '||to_char(t.first_at,'YYYY-MM-DD')||' to '||to_char(t.last_at,'YYYY-MM-DD')||'.'||
   COALESCE(' Status lifecycle: '||sh.trail||'.','')||
   COALESCE(' Linked issues: '||lk.links||'.','')||
   ' Discussion: '||t.body
 FROM issues i
 LEFT JOIN ventures v ON v.id=i.venture_id
 LEFT JOIN users asg ON asg.id=i.assignee_id
 JOIN LATERAL (
   SELECT count(*) AS n, min(x.at) AS first_at, max(x.at) AS last_at,
          string_agg(x.line, ' | ' ORDER BY x.at) AS body
   FROM (SELECT c.created_at AS at,
                '['||to_char(c.created_at,'YYYY-MM-DD')||' '||
                COALESCE(CASE WHEN c.author_kind::text='agent' THEN COALESCE(ag.name, c.as_agent_id)
                              ELSE cu.display_name END, 'someone')||'] '||
                left(regexp_replace(c.body_md, '\\s+', ' ', 'g'), 500) AS line
         FROM issue_comments c
         LEFT JOIN users cu ON cu.id=c.author_user_id
         LEFT JOIN agents ag ON ag.id=c.as_agent_id
         WHERE c.issue_id=i.id AND length(COALESCE(c.body_md,'')) >= 40
         ORDER BY c.created_at DESC LIMIT 8) x) t ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(COALESCE(h.from_status::text,'new')||'->'||h.to_status::text||
                     ' ('||to_char(h.changed_at,'YYYY-MM-DD')||')', ', ' ORDER BY h.changed_at) AS trail
   FROM issue_status_history h WHERE h.issue_id=i.id) sh ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(l.type::text||' #'||i2.number, ', ') AS links
   FROM issue_links l JOIN issues i2 ON i2.id=l.to_issue_id WHERE l.from_issue_id=i.id) lk ON true
 WHERE t.n > 0"""),

# agent_actions 3,888 (+ agent_runs 50) -> one digest per agent x venture x ISO
# week (~11). Answers "what did Nala do on flow-os that week"; the per-action
# rows ("deploying main abc -> def") are worthless individually.
("agent_activity", "flowos_agent_activity", "agent-activity", """
 WITH k AS (SELECT agent_id, venture_id, date_trunc('week', created_at) AS wk, kind,
                   count(*) AS n, max(created_at) AS last_at
              FROM agent_actions GROUP BY 1,2,3,4),
      g AS (SELECT agent_id, venture_id, wk, sum(n) AS total, max(last_at) AS last_at,
                   string_agg(kind||' x'||n, ', ' ORDER BY n DESC) AS breakdown
              FROM k GROUP BY 1,2,3)
 SELECT g.agent_id||'|'||COALESCE(g.venture_id,'none')||'|'||to_char(g.wk,'IYYY-"W"IW'), g.last_at,
   'FlowOS Agent activity digest — agent '||COALESCE(ag.name,g.agent_id)||
   ' on '||COALESCE('venture '||v.name,'the studio (no venture)')||
   ' for the week of '||to_char(g.wk,'YYYY-MM-DD')||': '||g.total||' recorded actions. '||
   'Breakdown by kind: '||g.breakdown||'.'||
   COALESCE(' '||r.runs,'')||
   COALESCE(' What it actually did (latest first): '||n.notable,'')
 FROM g
 LEFT JOIN agents ag ON ag.id=g.agent_id
 LEFT JOIN ventures v ON v.id=g.venture_id
 LEFT JOIN LATERAL (
   SELECT CASE WHEN count(*)>0 THEN count(*)||' agent run(s), '||
               count(*) FILTER (WHERE ar.status::text='failed')||' failed.' END AS runs
   FROM agent_runs ar WHERE ar.agent_id=g.agent_id
     AND ar.venture_id IS NOT DISTINCT FROM g.venture_id
     AND date_trunc('week', ar.created_at)=g.wk) r ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(y.summary, ' | ' ORDER BY y.at DESC) AS notable FROM (
     SELECT z.summary, z.at FROM (
       SELECT DISTINCT ON (left(a2.summary,45)) a2.summary, a2.created_at AS at
       FROM agent_actions a2
       WHERE a2.agent_id=g.agent_id AND a2.venture_id IS NOT DISTINCT FROM g.venture_id
         AND date_trunc('week', a2.created_at)=g.wk AND NULLIF(a2.summary,'') IS NOT NULL
       ORDER BY left(a2.summary,45), a2.created_at DESC) z
     ORDER BY z.at DESC LIMIT 25) y) n ON true"""),

# chats 67 / chat_messages 99 / chat_members 203 -> one transcript per TEAM chat.
# The text is copied verbatim, never translated or summarised away, or the Arabic
# recall path dies. Three hard filters, because this table is mostly not knowledge:
#   * scope <> 'direct'  — 1:1 DMs are private social chatter between two named
#     people ("بمووووتتتتتتتت", "🤪"); putting them in a shared brain is a privacy
#     problem and they answer nothing.
#   * a credential regex — venture chats contain pasted staging logins
#     ("email: ... pass: 12345678"). Secret-bearing text is NEVER ingested.
#   * a substance floor — most venture chats are literally "test | test | hi".
# What survives is small and honest; see the report for the exact count.
("chat_threads", "flowos_chat", "chat-thread", """
 SELECT ch.id, m.last_at,
   'FlowOS Chat conversation ('||COALESCE(ch.scope::text,'-')||COALESCE(' — venture '||v.name,'')||') titled "'||
   COALESCE(ch.title,'(untitled)')||'": '||m.n||' message(s) from '||to_char(m.first_at,'YYYY-MM-DD')||
   ' to '||to_char(m.last_at,'YYYY-MM-DD')||'. Participants: '||COALESCE(p.who,'unknown')||
   '. Transcript (original language preserved): '||m.body
 FROM chats ch
 LEFT JOIN ventures v ON v.id=ch.venture_id
 JOIN LATERAL (
   SELECT count(*) AS n, min(x.at) AS first_at, max(x.at) AS last_at,
          sum(x.blen) AS substance, string_agg(x.line, ' | ' ORDER BY x.at) AS body
   FROM (SELECT msg.created_at AS at, length(msg.body) AS blen,
                COALESCE(CASE WHEN msg.sender_kind::text='agent' THEN COALESCE(mag.name, msg.sender_agent_id)
                              ELSE mu.display_name END,'someone')||': '||
                left(regexp_replace(msg.body, '\\s+', ' ', 'g'), 400) AS line
         FROM chat_messages msg
         LEFT JOIN users mu ON mu.id=msg.sender_id
         LEFT JOIN agents mag ON mag.id=msg.sender_agent_id
         WHERE msg.chat_id=ch.id AND msg.deleted_at IS NULL AND NULLIF(msg.body,'') IS NOT NULL
           AND msg.body !~* '(pass\\s*[:=]|password|passwd|api[_ -]?key|secret|token\\s*[:=]|bearer |\\muser\\s*[:=]|\\madmin\\s*[:=]|كلمة المرور|الباسورد)'
         ORDER BY msg.created_at DESC LIMIT 30) x) m ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(DISTINCT COALESCE(pu.display_name, pag.name, 'unknown'), ', ') AS who
   FROM chat_members cm
   LEFT JOIN users pu ON pu.id=cm.user_id
   LEFT JOIN agents pag ON pag.id=cm.member_agent_id
   WHERE cm.chat_id=ch.id) p ON true
 WHERE m.n > 0 AND COALESCE(ch.scope::text,'') <> 'direct' AND m.substance >= 120"""),

# comments 212 (polymorphic; 209 of them hang off posts) -> one discussion digest
# per commented post (~134), attached to the post it belongs to.
("post_threads", "flowos_post_thread", "post-thread", """
 SELECT p.id, c.last_at,
   'FlowOS Post discussion — comments on the post "'||COALESCE(NULLIF(p.title,''),'(untitled)')||'" ('||
   COALESCE(p.type::text,'status')||' posted '||to_char(p.created_at,'YYYY-MM-DD')||
   COALESCE(' by '||au.display_name,'')||COALESCE(' in venture '||v.name,'')||'): '||
   c.n||' comment(s) up to '||to_char(c.last_at,'YYYY-MM-DD')||'. '||c.body
 FROM posts p
 LEFT JOIN users au ON au.id=p.author_id
 LEFT JOIN ventures v ON v.id=p.venture_id
 JOIN LATERAL (
   SELECT count(*) AS n, max(x.at) AS last_at, string_agg(x.line, ' | ' ORDER BY x.at) AS body
   FROM (SELECT cm.created_at AS at,
                '['||to_char(cm.created_at,'YYYY-MM-DD')||' '||
                COALESCE(cu.display_name, cm.as_agent_id, 'someone')||'] '||
                left(regexp_replace(cm.body_md, '\\s+', ' ', 'g'), 400) AS line
         FROM comments cm LEFT JOIN users cu ON cu.id=cm.author_id
         WHERE cm.target_type::text='post' AND cm.target_id=p.id AND NULLIF(cm.body_md,'') IS NOT NULL
         ORDER BY cm.created_at DESC LIMIT 10) x) c ON true
 WHERE c.n > 0"""),

# github_pull_requests 1,948 -> one activity rollup per repo (~79). The table has
# no PR title, so a per-PR memory would read "#412 by fady, merged" — pure noise.
# The answerable knowledge is the aggregate: throughput, merge rate, who ships,
# how fast review starts.
("github_pr_activity", "flowos_github_pr", "pr-activity", """
 SELECT pr.repo_full_name,
   max(GREATEST(pr.created_at, COALESCE(pr.merged_at,pr.created_at), COALESCE(pr.closed_at,pr.created_at))),
   'FlowOS GitHub pull-request activity — repository '||pr.repo_full_name||' (org '||COALESCE(pr.org_slug,'-')||'): '||
   count(*)||' pull requests tracked between '||to_char(min(pr.created_at),'YYYY-MM-DD')||' and '||
   to_char(max(pr.created_at),'YYYY-MM-DD')||'. Merged: '||count(*) FILTER (WHERE pr.merged_at IS NOT NULL)||
   ', still open: '||count(*) FILTER (WHERE pr.state='open')||
   ', closed without merging: '||count(*) FILTER (WHERE pr.merged_at IS NULL AND pr.state<>'open')||
   '. Median hours to first review: '||
   COALESCE(round(percentile_cont(0.5) WITHIN GROUP (
     ORDER BY EXTRACT(epoch FROM pr.first_review_at-pr.created_at)/3600.0)::numeric,1)::text,'never reviewed')||
   '. Median hours open before merge: '||
   COALESCE(round(percentile_cont(0.5) WITHIN GROUP (
     ORDER BY EXTRACT(epoch FROM pr.merged_at-pr.created_at)/3600.0)::numeric,1)::text,'-')||
   '. Authors: '||COALESCE(a.authors,'-')||
   '. Most recent pull requests: '||COALESCE(rec.recent,'-')
 FROM github_pull_requests pr
 LEFT JOIN LATERAL (
   SELECT string_agg(t.author_login||' ('||t.n||')', ', ' ORDER BY t.n DESC) AS authors FROM (
     SELECT author_login, count(*) AS n FROM github_pull_requests p2
     WHERE p2.repo_full_name=pr.repo_full_name AND p2.author_login IS NOT NULL
     GROUP BY 1 ORDER BY 2 DESC LIMIT 8) t) a ON true
 LEFT JOIN LATERAL (
   SELECT string_agg('#'||t.number||' by '||COALESCE(t.author_login,'?')||' '||
                     CASE WHEN t.merged_at IS NOT NULL THEN 'merged '||to_char(t.merged_at,'YYYY-MM-DD')
                          ELSE t.state||' since '||to_char(t.created_at,'YYYY-MM-DD') END,
                     ', ' ORDER BY t.created_at DESC) AS recent FROM (
     SELECT * FROM github_pull_requests p3 WHERE p3.repo_full_name=pr.repo_full_name
     ORDER BY p3.created_at DESC LIMIT 10) t) rec ON true
 GROUP BY pr.repo_full_name, pr.org_slug, a.authors, rec.recent"""),

# pulse_campaigns/questions/responses/answers/anonymous_messages -> one digest per
# survey (4). This is the only place the team says, in its own words and in
# Arabic, what it thinks of Flow OS. Free-text answers are quoted verbatim;
# matrix/scale answers are aggregated.
("pulse_surveys", "flowos_pulse", "pulse", """
 SELECT c.id, COALESCE(c.anonymous_revealed_at, r.last_at, c.closes_at, c.updated_at, c.created_at),
   'FlowOS Team Pulse survey "'||COALESCE(c.title_en,'(untitled)')||'"'||
   COALESCE(' / '||c.title_ar,'')||' — status '||COALESCE(c.status::text,'-')||
   COALESCE(', opened '||to_char(c.opens_at,'YYYY-MM-DD'),'')||
   COALESCE(', closes '||to_char(c.closes_at,'YYYY-MM-DD'),'')||
   '. Responses: '||COALESCE(r.n,0)||'. '||COALESCE(c.intro_en,'')||' '||COALESCE(c.intro_ar,'')||
   COALESCE(' Questions asked: '||q.qs||'.','')||
   COALESCE(' Free-text answers from the team (verbatim): '||f.txt,'')||
   COALESCE(' Scale/choice results: '||sc.agg,'')||
   COALESCE(' Anonymous messages to the company (verbatim): '||anon.msgs,'')
 FROM pulse_campaigns c
 LEFT JOIN LATERAL (SELECT count(*) AS n, max(submitted_at) AS last_at
                      FROM pulse_responses pr WHERE pr.campaign_id=c.id) r ON true
 LEFT JOIN LATERAL (SELECT string_agg(pq.prompt_en||COALESCE(' / '||pq.prompt_ar,''), '; ' ORDER BY pq.sort_order) AS qs
                      FROM pulse_questions pq WHERE pq.campaign_id=c.id) q ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(t.line, ' | ') AS txt FROM (
     SELECT '['||pq.key||'] '||left(regexp_replace(pa.value->>'text','\\s+',' ','g'),220) AS line
     FROM pulse_answers pa JOIN pulse_questions pq ON pq.id=pa.question_id
     JOIN pulse_responses pr2 ON pr2.id=pa.response_id
     WHERE pq.campaign_id=c.id AND pr2.campaign_id=c.id AND length(COALESCE(pa.value->>'text',''))>=12
     ORDER BY pr2.submitted_at DESC LIMIT 24) t) f ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(t.k||': '||t.v||' x'||t.n, ', ') AS agg FROM (
     SELECT pq.key AS k, COALESCE(pa.value->>'choice', pa.value->>'value') AS v, count(*) AS n
     FROM pulse_answers pa JOIN pulse_questions pq ON pq.id=pa.question_id
     WHERE pq.campaign_id=c.id AND COALESCE(pa.value->>'choice', pa.value->>'value') IS NOT NULL
     GROUP BY 1,2 ORDER BY 1,3 DESC) t) sc ON true
 LEFT JOIN LATERAL (
   SELECT string_agg(left(regexp_replace(am.body,'\\s+',' ','g'),500), ' | ') AS msgs
   FROM pulse_anonymous_messages am WHERE am.campaign_id=c.id AND length(COALESCE(am.body,''))>=20) anon ON true
 WHERE COALESCE(r.n,0) > 0"""),

# The anonymous box gets its OWN memory. Inside the campaign digest it sat behind
# 24 free-text answers and was silently cut off by the content cap — and it is the
# only place the team criticises the product without attribution ("النظام يعاني من
# تضخّم..."). Losing it to a truncation would have been the worst possible loss.
("pulse_anonymous", "flowos_pulse", "pulse-anonymous", """
 SELECT c.id, COALESCE(c.anonymous_revealed_at, c.closes_at, c.updated_at),
   'FlowOS Team Pulse — ANONYMOUS messages from the team about Flow OS, survey "'||
   COALESCE(c.title_en,'(untitled)')||'"'||COALESCE(' / '||c.title_ar,'')||
   COALESCE(', revealed '||to_char(c.anonymous_revealed_at,'YYYY-MM-DD'),'')||
   ' ('||a.n||' message(s), unattributed by design, original language preserved): '||a.msgs
 FROM pulse_campaigns c
 JOIN LATERAL (
   SELECT count(*) AS n, string_agg(left(regexp_replace(am.body,'\\s+',' ','g'),700), ' | ') AS msgs
   FROM pulse_anonymous_messages am
   WHERE am.campaign_id=c.id AND length(COALESCE(am.body,''))>=20) a ON true
 WHERE a.n > 0"""),

# venture_deployment_events 63 -> one deployment history per venture.
("deployments", "flowos_deployment", "deployment-history", """
 SELECT e.venture_id, max(e.created_at),
   'FlowOS Deployment history — venture '||COALESCE(v.name,e.venture_id)||': '||count(*)||' deployment events between '||
   to_char(min(e.created_at),'YYYY-MM-DD')||' and '||to_char(max(e.created_at),'YYYY-MM-DD')||
   '. Succeeded: '||count(*) FILTER (WHERE e.status::text IN ('success','succeeded','ready'))||
   ', failed: '||count(*) FILTER (WHERE e.status::text IN ('failed','error'))||
   '. Providers: '||COALESCE(string_agg(DISTINCT e.provider::text,', '),'-')||
   '. Environments: '||COALESCE(string_agg(DISTINCT e.env,', '),'-')||
   '. Recent deploys: '||COALESCE(rec.recent,'-')
 FROM venture_deployment_events e
 LEFT JOIN ventures v ON v.id=e.venture_id
 LEFT JOIN LATERAL (
   SELECT string_agg(to_char(t.created_at,'YYYY-MM-DD')||' '||COALESCE(t.status::text,'-')||' '||
                     COALESCE(left(t.sha,7),'')||COALESCE(' "'||left(regexp_replace(t.commit_message,'\\s+',' ','g'),90)||'"','')||
                     COALESCE(' by '||t.author,''), ' | ' ORDER BY t.created_at DESC) AS recent
   FROM (SELECT * FROM venture_deployment_events e2 WHERE e2.venture_id=e.venture_id
         ORDER BY e2.created_at DESC LIMIT 12) t) rec ON true
 GROUP BY e.venture_id, v.name, rec.recent"""),

# feature_registry 43 + feature_flags 44 -> one catalogue per lifecycle stage
# (~3). "Which features are still beta / behind a kill switch" is a real
# question; 43 one-line memories would just be 43 near-duplicates.
("feature_catalog", "flowos_feature", "feature-catalog", """
 SELECT COALESCE(f.lifecycle,'unspecified'), max(COALESCE(f.updated_at,f.created_at)),
   'FlowOS feature catalogue — '||COALESCE(f.lifecycle,'unspecified')||' features ('||count(*)||' of them): '||
   string_agg(f.name||COALESCE(' / '||f.name_ar,'')||COALESCE(' at route '||f.route,'')||
              COALESCE(' [flag '||f.flag_key||' = '||CASE WHEN fl.enabled THEN 'ON' ELSE 'OFF' END||
                       CASE WHEN fl.kill_switch THEN ', KILL SWITCH ENGAGED' ELSE '' END||']','')||
              COALESCE(': '||left(regexp_replace(f.description,'\\s+',' ','g'),220),''),
              ' | ' ORDER BY f.name)
 FROM feature_registry f LEFT JOIN feature_flags fl ON fl.key=f.flag_key
 GROUP BY COALESCE(f.lifecycle,'unspecified')"""),

# harvester_sources 69 -> one roll-up per source type (3). These are the feeds the
# studio watches; the harvested ITEMS are already ingested individually.
("harvest_sources", "flowos_harvested", "harvest-sources", """
 SELECT s.source_type::text, max(COALESCE(s.updated_at,s.created_at)),
   'FlowOS Harvester sources — '||count(*)||' '||s.source_type::text||' source(s) the studio watches for research: '||
   string_agg(COALESCE(NULLIF(s.title,''),s.url)||COALESCE(' ('||NULLIF(array_to_string(s.tags,', '),'')||')','')||
              COALESCE(' — '||left(regexp_replace(s.note,'\\s+',' ','g'),150),'')||
              CASE WHEN s.confirmed THEN ' [confirmed]' ELSE '' END, ' | ' ORDER BY s.title)
 FROM harvester_sources s GROUP BY s.source_type::text"""),

# curator_proposals 115 -> ONE memory. 113 of them are "these two learnings are
# near-duplicates"; the backlog is the fact worth knowing, not each proposal.
("curator_backlog", "flowos_learning", "curator-backlog", """
 SELECT 'all', max(p.created_at),
   'FlowOS Learnings curator backlog as of '||to_char(max(p.created_at),'YYYY-MM-DD')||': '||count(*)||
   ' curator proposals — '||
   string_agg(DISTINCT p.proposal_type::text||'/'||p.status::text, ', ')||
   '. Counts: '||(SELECT string_agg(t.proposal_type::text||' '||t.status::text||' x'||t.n, ', ')
                  FROM (SELECT proposal_type, status, count(*) n FROM curator_proposals GROUP BY 1,2) t)||
   '. Sample reasons the curator gave: '||
   COALESCE((SELECT string_agg(left(regexp_replace(r.reason,'\\s+',' ','g'),260), ' | ')
             FROM (SELECT reason FROM curator_proposals WHERE NULLIF(reason,'') IS NOT NULL
                   ORDER BY created_at DESC LIMIT 8) r),'-')
 FROM curator_proposals p"""),

# boards 2 -> 2 memories. Tiny table, very high density: these describe the
# WhatsApp-migrated decision/task registries and how items are routed between
# them. The tasks themselves are already ingested.
("boards", "flowos_board", "board", """
 SELECT b.id, COALESCE(b.updated_at,b.created_at),
   'FlowOS Board "'||b.name||'"'||COALESCE(' / '||b.name_ar,'')||' (key '||b.key||', task prefix '||
   COALESCE(b.task_prefix,'-')||', visibility '||COALESCE(b.visibility::text,'-')||
   COALESCE(', sourced from '||b.source,'')||COALESCE(', registry year '||b.registry_year::text,'')||
   '): '||COALESCE(b.description,'')||' '||COALESCE(b.description_ar,'')
 FROM boards b"""),

# playbook_completions 16 -> ONE roll-up: who has finished which onboarding steps.
("playbook_progress", "flowos_management", "playbook-progress", """
 SELECT 'all', max(pc.updated_at),
   'FlowOS Playbook (onboarding) progress as of '||to_char(max(pc.updated_at),'YYYY-MM-DD')||': '||
   count(*)||' people have progress recorded. '||
   string_agg(COALESCE(u.display_name,'someone')||' completed '||
              COALESCE(array_length(pc.completed_step_ids,1),0)||' step(s) ('||
              COALESCE(array_to_string(pc.completed_step_ids,', '),'none')||')',
              ' | ' ORDER BY COALESCE(array_length(pc.completed_step_ids,1),0) DESC)
 FROM playbook_completions pc LEFT JOIN users u ON u.id=pc.user_id"""),
]

QUERIES += EXTRA + GAP_FILL

# Per-query content cap. The default 1500 is right for a single row; a digest that
# has already collapsed 40 comments into one memory needs the room, and 4,800 is
# still inside the existing corpus (p95 = 4,061 chars, max = 5,755).
CAP = {"learnings": 2800, "issue_threads": 4800, "agent_activity": 4200, "chat_threads": 4200,
       "post_threads": 4200, "pulse_surveys": 5200, "pulse_anonymous": 5200, "feature_catalog": 5200,
       "harvest_sources": 4200, "github_pr_activity": 2000, "deployments": 2500,
       "curator_backlog": 2500, "boards": 2500, "playbook_progress": 2500}

# Roll-ups are deliberately quieter than the primary entities. They are context
# you want WHEN you ask about that issue/agent/repo, not competition for "who is
# the management team". Everything not listed keeps the historical 0.6.
IMPORTANCE = {"issue_threads": 0.45, "agent_activity": 0.4, "post_threads": 0.45,
              "github_pr_activity": 0.35, "chat_threads": 0.5, "pulse_surveys": 0.55, "pulse_anonymous": 0.6,
              "feature_catalog": 0.45, "harvest_sources": 0.4, "deployments": 0.4,
              "curator_backlog": 0.35, "boards": 0.5, "playbook_progress": 0.4}

def retain(item):
    ref, content, kind, mtype, ts, imp = item
    body = {"namespace": NS, "content": content, "sourceKind": kind,
            "sourceRef": ref, "metadata": {"type": mtype}, "importanceHint": imp}
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
        cap, imp = CAP.get(name, 1500), IMPORTANCE.get(name, 0.6)
        for r in rows:
            rid, ts, content = r[0], r[1], r[2]
            if not content: continue
            work.append((f"db:{mtype}:{rid}", clean(content, cap), kind, mtype, ts, imp))
        print(f"  {name:20} {len(rows):>5} rows", flush=True)
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
