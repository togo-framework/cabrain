#!/usr/bin/env python3
"""Refresh the `cabrain` dev-knowledge brain from the repo — run after each set of
changes so future sessions can recall the latest project state.

Ingests (into namespace `cabrain`, deduped by the §4.1 write-decision):
  1. every repo markdown doc (SPEC/PLAN/DEPLOY/decisions/rules/CLAUDE.md/...), chunked
  2. the full git commit history (each commit = a build-log entry, with body)
  3. (optional) the Claude project-memory file if $CABRAIN_MEMORY_FILE is set

Usage:  python3 scripts/refresh-cabrain-brain.py
        CABRAIN_API_URL=http://localhost:8080 (default)
"""
import json, os, re, subprocess, urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
API = os.environ.get("CABRAIN_API_URL", "http://localhost:8080")
NS = "cabrain"
MAXC = 2500
EXCLUDE_DIRS = {"node_modules", "dist", "worktrees", "scratchpad", ".git"}

def retain(content, ref, meta):
    body = json.dumps({"namespace": NS, "content": content[:6000], "sourceKind": meta.get("_sk", "cabrain_repo"),
                       "sourceRef": ref, "metadata": {k: v for k, v in meta.items() if not k.startswith("_")}}).encode()
    req = urllib.request.Request(API + "/api/brain/retain", data=body, headers={"Content-Type": "application/json"})
    tok = os.environ.get("CABRAIN_TOKEN")
    if tok:
        req.add_header("X-Cabrain-Token", tok)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return json.loads(r.read()).get("decision", "?")
    except Exception:
        return "ERR"

def chunks(text):
    parts = re.split(r'(?m)^(?=#{1,3} )', text)
    packed, buf = [], ""
    for p in parts:
        if len(buf) + len(p) > MAXC and buf:
            packed.append(buf); buf = p
        else:
            buf += p
    if buf.strip():
        packed.append(buf)
    out = []
    for ch in packed:
        while len(ch) > MAXC * 2:
            out.append(ch[:MAXC * 2]); ch = ch[MAXC * 2:]
        if ch.strip():
            out.append(ch)
    return out

def main():
    n_doc = n_commit = n_hist = 0
    # 1. repo markdown
    for dp, dns, fns in os.walk(REPO):
        dns[:] = [d for d in dns if d not in EXCLUDE_DIRS]
        for fn in fns:
            if not fn.endswith(".md"):
                continue
            rel = os.path.relpath(os.path.join(dp, fn), REPO)
            try:
                text = open(os.path.join(dp, fn), encoding="utf-8", errors="replace").read().strip()
            except Exception:
                continue
            if len(text) < 20:
                continue
            cks = chunks(text) or [text]
            for i, ch in enumerate(cks):
                hdr = f"CaBrain repo · {rel}" + (f" (part {i+1}/{len(cks)})" if len(cks) > 1 else "")
                if retain(hdr + "\n\n" + ch, f"cabrain:{rel}#{i}", {"type": "doc", "path": rel}) != "ERR":
                    n_doc += 1
    print("docs:", n_doc, flush=True)

    # 2. git build-log
    log = subprocess.run(["git", "-C", REPO, "log", "-n", "500", "--pretty=format:%h%x1f%ai%x1f%s%x1f%b%x1e"],
                         capture_output=True, text=True).stdout
    for rec in log.split("\x1e"):
        parts = rec.strip().split("\x1f")
        if len(parts) < 3 or not parts[0].strip():
            continue
        h, date, subj = parts[0], parts[1], parts[2]
        body = parts[3] if len(parts) > 3 else ""
        content = f"CaBrain build-log commit {h} ({date[:10]}): {subj}\n{body.strip()[:1800]}"
        if retain(content, f"cabrain:log:{h}", {"type": "buildlog", "hash": h, "date": date[:10], "_sk": "cabrain_history"}) != "ERR":
            n_commit += 1
    print("commits:", n_commit, flush=True)

    # 3. optional curated project-memory
    mem = os.environ.get("CABRAIN_MEMORY_FILE")
    if mem and os.path.exists(mem):
        text = open(mem, encoding="utf-8", errors="replace").read()
        for i, ch in enumerate(chunks(text)):
            if retain(f"CaBrain project history / state (part {i+1})\n\n{ch}", f"cabrain:history:{i}",
                      {"type": "history", "_sk": "cabrain_history"}) != "ERR":
                n_hist += 1
        print("history:", n_hist, flush=True)

    print("DONE:", json.dumps({"docs": n_doc, "commits": n_commit, "history": n_hist}), flush=True)

if __name__ == "__main__":
    main()
