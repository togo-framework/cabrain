#!/usr/bin/env python3
"""Index repository SOURCE CODE into a CaBrain namespace.

SUPERSEDED by scripts/code-index.py (repomix-based, sha-gated, prunes stale
chunks, and is what the cabrain-code-index systemd timer runs). Kept because it
is the only indexer that needs nothing but the GitHub API — no clone, no npx —
so it still works from a host that cannot run repomix.

Why this exists (and why it is not just `datasource sync`): the built-in github
connector walks a tree and retains blobs, but a *code* index needs volume control
the brain has already been bitten by — a bulk ingest of 2,177 github-activity rows
once crowded real answers out of recall. So this script:

  * picks a small, RANKED set of files per repo (entrypoints, routes, handlers,
    services, schema, config, README) instead of the whole tree,
  * hard-skips vendored / generated / minified / test / lock files,
  * caps per-file size and per-repo memory count,
  * stamps each memory with the file's LAST-COMMIT date as valid_at (not now()),
  * uses a stable sourceRef `code:<owner>/<repo>:<path>#<chunk>` so a re-run
    UPDATEs in place (Retain treats source_ref as an identity),
  * links every code memory to its repo ENTITY via memory_entities, which is what
    makes code reachable by 1-hop graph expansion during recall.

Everything is read from the environment — never hardcode a token here:

  GITHUB_TOKEN     repo-read token (e.g. `gh auth token`)
  CABRAIN_URL      brain API base (default https://cabrain.fadymondy.com)
  CABRAIN_TOKEN    X-Cabrain-Token value
  CABRAIN_PG_*     PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD for the entity links
  BRAIN_NS         namespace (default flowos)

Usage:
  python3 scripts/index-repo-code.py [--repos scripts/flowos-code-repos.json]
                                     [--only owner/name] [--dry-run]
"""

import argparse
import base64
import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.parse
from concurrent.futures import ThreadPoolExecutor

API = "https://api.github.com"
CABRAIN_URL = os.environ.get("CABRAIN_URL", "https://cabrain.fadymondy.com").rstrip("/")
CABRAIN_TOKEN = os.environ.get("CABRAIN_TOKEN", "")
GH_TOKEN = os.environ.get("GITHUB_TOKEN", "")
NS = os.environ.get("BRAIN_NS", "flowos")

# ── volume policy ─────────────────────────────────────────────────────────────
MAX_FILES_PER_REPO = int(os.environ.get("MAX_FILES_PER_REPO", "14"))
MAX_MEM_PER_REPO = int(os.environ.get("MAX_MEM_PER_REPO", "26"))
MAX_CHUNKS_PER_FILE = int(os.environ.get("MAX_CHUNKS_PER_FILE", "2"))
CHUNK_CHARS = int(os.environ.get("CHUNK_CHARS", "1800"))
MIN_BYTES, MAX_BYTES = 250, 60000

EXTS = {
    ".go": "go", ".ts": "typescript", ".tsx": "typescript", ".js": "javascript",
    ".jsx": "javascript", ".py": "python", ".sql": "sql", ".md": "markdown",
    ".yaml": "yaml", ".yml": "yaml", ".tf": "terraform",
    # the FlowOS portfolio is heavily PHP/Laravel + Vue; excluding them would skip
    # the actual product code of Member Plus, Wallet Plus, aeroplane, weArt…
    ".php": "php", ".vue": "vue",
}

SKIP_DIRS = (
    "node_modules/", "vendor/", "dist/", "build/", ".next/", "out/", "testdata/",
    "__pycache__/", ".venv/", "coverage/", "third_party/", "public/build/",
    "storage/framework/", "__snapshots__/", "bootstrap/cache/", ".github/workflows/",
    "locales/", "translations/", "lang/", "fixtures/", "seeders/", "assets/",
)
SKIP_NAME_BITS = (
    ".min.", ".gen.", ".d.ts", "_test.go", ".test.ts", ".spec.ts", ".test.tsx",
    "-lock.json", "package-lock.json", "yarn.lock", "composer.lock",
    "pnpm-lock.yaml", "go.sum", "changelog.md", "license",
)

# path tokens that mark a file as worth remembering (score bonus)
BOOSTS = [
    (("readme.md",), 14), (("main.go", "index.ts", "index.js", "app.py", "server.ts",
                            "server.js", "main.py", "app.tsx", "bootstrap"), 8),
    (("route", "router", "handler", "controller", "endpoint", "/api/"), 7),
    (("service", "usecase", "core/", "engine", "domain/", "repository"), 6),
    (("auth", "token", "session", "permission", "livekit", "webhook", "payment",
      "billing", "subscription", "queue", "worker", "cron", "agent"), 6),
    (("schema", "model", "migration", ".sql", "entity", "types.ts"), 5),
    (("config", "settings", "env", "docker-compose", "dockerfile", ".tf"), 4),
    (("hook", "store", "context", "provider", "middleware", "util", "helper"), 2),
]
PENALTIES = [
    (("test", "spec", "mock", "stub", "example", "demo", "sample"), -8),
    (("component", "widget", "styles", "css", "icon", "svg"), -3),
    (("docs/", "doc/"), -1),
]


def gh(path_or_url, raw=False):
    url = path_or_url if path_or_url.startswith("http") else API + path_or_url
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github.raw" if raw else "application/vnd.github+json")
    req.add_header("User-Agent", "cabrain-code-indexer")
    if GH_TOKEN:
        req.add_header("Authorization", "Bearer " + GH_TOKEN)
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=45) as r:
                body = r.read()
                return body if raw else json.loads(body)
        except Exception as e:  # noqa: BLE001 — transient GitHub errors are common
            if attempt == 2:
                raise
            time.sleep(1.5 * (attempt + 1))
    return None


def skip_path(p):
    lp = p.lower()
    if any(d in lp for d in SKIP_DIRS):
        return True
    base = lp.rsplit("/", 1)[-1]
    return any(b in base for b in SKIP_NAME_BITS)


def score(path, size):
    lp = path.lower()
    s = 0.0
    for toks, w in BOOSTS:
        if any(t in lp for t in toks):
            s += w
    for toks, w in PENALTIES:
        if any(t in lp for t in toks):
            s += w
    s -= 0.8 * lp.count("/")            # shallow files describe the system
    if 800 <= size <= 25000:            # substantial but not a dump
        s += 3
    return s


def pick_files(repo, branch):
    """Return [(path, size, sha)] — the ranked, filtered shortlist for one repo."""
    tree = gh(f"/repos/{repo}/git/trees/{urllib.parse.quote(branch)}?recursive=1")
    truncated = bool(tree.get("truncated"))
    cands = []
    for e in tree.get("tree", []):
        if e.get("type") != "blob":
            continue
        p, size = e["path"], int(e.get("size") or 0)
        ext = "." + p.rsplit(".", 1)[-1].lower() if "." in p else ""
        if ext not in EXTS or size < MIN_BYTES or size > MAX_BYTES or skip_path(p):
            continue
        cands.append((score(p, size), p, size, e["sha"]))
    cands.sort(key=lambda t: (-t[0], t[1]))
    picked, per_dir = [], {}
    for sc, p, size, sha in cands:
        d = p.rsplit("/", 1)[0] if "/" in p else "."
        if per_dir.get(d, 0) >= 3:      # spread coverage across the codebase
            continue
        per_dir[d] = per_dir.get(d, 0) + 1
        picked.append((p, size, sha))
        if len(picked) >= MAX_FILES_PER_REPO:
            break
    return picked, len(cands), truncated


def file_date(repo, path):
    try:
        c = gh(f"/repos/{repo}/commits?per_page=1&path={urllib.parse.quote(path)}")
        return c[0]["commit"]["committer"]["date"] if c else None
    except Exception:  # noqa: BLE001
        return None


def chunk(text, n, limit):
    out, cur = [], []
    size = 0
    for line in text.splitlines(keepends=True):
        if size + len(line) > n and cur:
            out.append("".join(cur))
            cur, size = [], 0
            if len(out) >= limit:
                return out
        cur.append(line)
        size += len(line)
    if cur and len(out) < limit:
        out.append("".join(cur))
    return out


def retain(payload):
    """POST /api/brain/retain via curl — Cloudflare 403s the python-urllib UA."""
    p = subprocess.run(
        ["curl", "-s", "--max-time", "90", "-X", "POST", CABRAIN_URL + "/api/brain/retain",
         "-H", "X-Cabrain-Token: " + CABRAIN_TOKEN, "-H", "content-type: application/json",
         "--data-binary", "@-"],
        input=json.dumps(payload), capture_output=True, text=True)
    if p.returncode != 0 or not p.stdout.strip():
        return None, (p.stderr or "empty response")[:160]
    try:
        j = json.loads(p.stdout)
    except json.JSONDecodeError:
        return None, p.stdout[:160]
    return j.get("id"), j.get("decision")


def index_repo(entry, dry=False, already=None):
    repo, branch = entry["repo"], entry.get("branch") or "main"
    owner_repo = repo
    try:
        picked, n_cand, truncated = pick_files(repo, branch)
        if already:
            picked = [f for f in picked if f[0] not in already]
    except Exception as e:  # noqa: BLE001
        return {"repo": repo, "error": f"tree: {e}"}
    stats = {"repo": repo, "branch": branch, "candidates": n_cand, "files": 0,
             "memories": 0, "truncated": truncated, "ids": [], "paths": [],
             "errors": []}
    mem_budget = MAX_MEM_PER_REPO

    def one(item):
        p, size, sha = item
        try:
            raw = gh(f"/repos/{repo}/git/blobs/{sha}", raw=True)
            text = raw.decode("utf-8")
        except Exception:  # noqa: BLE001 — binary or unreadable blob
            return None
        if not text.strip():
            return None
        return (p, size, text, file_date(repo, p))

    with ThreadPoolExecutor(max_workers=5) as pool:
        fetched = [f for f in pool.map(one, picked) if f]

    payloads = []
    for p, size, text, when in fetched:
        if mem_budget <= 0:
            break
        ext = "." + p.rsplit(".", 1)[-1].lower()
        lang = EXTS.get(ext, "text")
        parts = chunk(text, CHUNK_CHARS, MAX_CHUNKS_PER_FILE)
        stats["files"] += 1
        stats["paths"].append(p)
        for i, part in enumerate(parts):
            if mem_budget <= 0:
                break
            header = (f"Source code — {owner_repo} · {p} ({lang})"
                      f"{f' [part {i + 1}/{len(parts)}]' if len(parts) > 1 else ''}\n\n")
            payload = {
                "namespace": NS,
                "content": header + part,
                "sourceKind": "flowos_code",
                "sourceRef": f"code:{owner_repo}:{p}#{i}",
                "metadata": {"type": "code", "repo": owner_repo, "path": p,
                             "lang": lang, "branch": branch, "title": f"{owner_repo}/{p}"},
                "importanceHint": 0.55,
            }
            if when:
                payload["validAt"] = when
            payloads.append((p, i, payload))
            mem_budget -= 1

    if dry:
        stats["memories"] = len(payloads)
        return stats

    def send(item):
        p, i, payload = item
        mid, dec = retain(payload)
        return (p, i, mid, dec)

    with ThreadPoolExecutor(max_workers=4) as pool:
        for p, i, mid, dec in pool.map(send, payloads):
            if mid:
                stats["ids"].append(mid)
                stats["memories"] += 1
            else:
                stats["errors"].append(f"{p}#{i}: {dec}")
    return stats


# ── entity linking ────────────────────────────────────────────────────────────

def pg():
    import pg8000.native
    c = pg8000.native.Connection(
        user=os.environ["CABRAIN_PG_USER"], password=os.environ["CABRAIN_PG_PASSWORD"],
        host=os.environ.get("CABRAIN_PG_HOST", "localhost"),
        port=int(os.environ.get("CABRAIN_PG_PORT", "5432")),
        database=os.environ.get("CABRAIN_PG_DATABASE", "cabrain"))
    c.run("SET search_path = public")
    return c


def link_repo_entity(conn, repo, memory_ids):
    """Attach code memories to the repo entity (memory_entities = the mention index
    that recall's 1-hop expansion traverses). Idempotent."""
    rows = conn.run("SELECT id::text FROM entities WHERE namespace=:ns AND lower(name)=:n "
                    "AND entity_type='repo' LIMIT 1", ns=NS, n=repo.lower())
    if rows:
        eid = rows[0][0]
    else:
        eid = conn.run("INSERT INTO entities (namespace,name,entity_type,summary,metadata) "
                       "VALUES (:ns,:n,'repo',:s,:m::jsonb) "
                       "ON CONFLICT (namespace,name) DO UPDATE SET entity_type='repo' "
                       "RETURNING id::text", ns=NS, n=repo.lower(),
                       s=f"GitHub repository {repo}",
                       m=json.dumps({"source_id": repo.lower()}))[0][0]
    n = 0
    for mid in memory_ids:
        conn.run("INSERT INTO memory_entities (memory_id, entity_id) VALUES (:m::uuid,:e::uuid) "
                 "ON CONFLICT DO NOTHING", m=mid, e=eid)
        n += 1
    return eid, n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repos", default=os.path.join(os.path.dirname(__file__), "flowos-code-repos.json"))
    ap.add_argument("--only", default="")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--no-link", action="store_true")
    # --skip-existing makes a deepening second pass cheap: files already indexed are
    # left alone (re-retaining them would supersede a perfectly good row for nothing).
    ap.add_argument("--skip-existing", action="store_true")
    args = ap.parse_args()

    if not args.dry_run and not CABRAIN_TOKEN:
        sys.exit("CABRAIN_TOKEN is required (export it; never hardcode it)")
    entries = json.load(open(args.repos))["repos"]
    if args.only:
        entries = [e for e in entries if e["repo"].lower() == args.only.lower()]
    conn = None if (args.dry_run or args.no_link) else pg()

    total_mem, report = 0, []
    for e in entries:
        t0 = time.time()
        already = None
        if args.skip_existing:
            c2 = conn or pg()
            pref = f"code:{e['repo']}:"
            already = {r[0][len(pref):].rsplit("#", 1)[0] for r in c2.run(
                "SELECT source_ref FROM memories WHERE namespace=:ns AND invalid_at IS NULL "
                "AND source_ref LIKE :p", ns=NS, p=pref + "%")}
        st = index_repo(e, dry=args.dry_run, already=already)
        if conn and st.get("ids"):
            eid, n = link_repo_entity(conn, e["repo"], st["ids"])
            st["linked"] = n
        total_mem += st.get("memories", 0)
        st["secs"] = round(time.time() - t0, 1)
        report.append(st)
        print(json.dumps({k: v for k, v in st.items() if k != "ids"}), flush=True)
    print(json.dumps({"total_memories": total_mem, "repos": len(report)}), flush=True)
    out = os.environ.get("CODE_INDEX_REPORT")
    if out:
        json.dump(report, open(out, "w"), indent=1)


if __name__ == "__main__":
    main()
