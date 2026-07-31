#!/usr/bin/env python3
"""
CaBrain DYNAMIC code indexer — repomix is THE standard path for putting a repo's
codebase into the brain.

    scripts/code-index.py add onestudio-co/agents     # register a repo (one action)
    scripts/code-index.py list                        # what is registered + last sha
    scripts/code-index.py run                         # index everything that changed
    scripts/code-index.py run --repo owner/name --force

WHY THIS SHAPE
--------------
* REGISTRY = the existing `datasources` table (kind=github, config.indexer="repomix").
  It is already creatable over the API and through the `datasource_create` MCP tool,
  so "add a repo" is one call and needs no new concept. This script only *reads* the
  registry; it never calls the built-in blob-by-blob github connector (that path
  produces source_kind=datasource:github and is superseded here).
* FETCH  = shallow clone into $CODE_INDEX_CACHE; HEAD sha compared against the state
  file, unchanged repo => skipped. That is what makes the auto-refresh timer cheap.
* PACK   = repomix --compress --style markdown (structure, not every line) with a
  noise ignore-list; the --token-count-tree output is kept for the size profile.
* CHUNK  = split the pack on its "## File:" boundaries, ~1.5k-4k chars per memory,
  never crossing a file unless the files are tiny neighbours in the same directory,
  always prefixed with "repo — path" so a recalled chunk is self-locating.
* REFS   = stable: "code:<owner>/<repo>:<path>#<n>" / "doc:<owner>/<repo>:<path>#<n>".
  Retain treats sourceRef as an identity, so re-indexing UPDATEs in place instead of
  duplicating. Files that vanish are soft-retired (invalid_at), never deleted.
* GRAPH  = repo entity (entity_type=repo) + code_module/doc entities, CODE_IN/DOC_IN
  edges, memory_entities links so recall's 1-hop expansion can reach the code.

CONFIG — everything from env, no credential ever lives in this file:
    CABRAIN_API_URL   default https://cabrain.fadymondy.com
    CABRAIN_TOKEN     brain token (X-Cabrain-Token)
    CABRAIN_NAMESPACE default flowos
    CABRAIN_DSN       optional postgres DSN -> enables graph + prune of stale chunks
    FLOWOS_DSN        optional READ-ONLY FlowOS prod DSN -> REPO_OF venture edges
    GITHUB_TOKEN      optional; falls back to `gh auth token`
    CODE_INDEX_CACHE  default ~/.cache/cabrain-repos
    CODE_INDEX_STATE  default $CODE_INDEX_CACHE/state.json
    REPOMIX_CMD       default "npx --yes repomix@latest"
    CODE_MAX_CHUNKS   default 80    (per repo, per run — volume discipline)
    DOC_MAX_CHUNKS    default 40    (per repo, per run)
"""

import argparse
import base64
import json
import os
import re
import subprocess
import sys
import time
from collections import Counter, defaultdict
from datetime import datetime, timezone

API = os.environ.get("CABRAIN_API_URL", "https://cabrain.fadymondy.com").rstrip("/")
TOKEN = os.environ.get("CABRAIN_TOKEN", "")
NS = os.environ.get("CABRAIN_NAMESPACE", "flowos")
CACHE = os.environ.get("CODE_INDEX_CACHE", os.path.expanduser("~/.cache/cabrain-repos"))
STATE_PATH = os.environ.get("CODE_INDEX_STATE", os.path.join(CACHE, "state.json"))
DSN = os.environ.get("CABRAIN_DSN", "")
FLOWOS_DSN = os.environ.get("FLOWOS_DSN", "")
REPOMIX_CMD = os.environ.get("REPOMIX_CMD", "npx --yes repomix@latest").split()
CODE_MAX_CHUNKS = int(os.environ.get("CODE_MAX_CHUNKS", "80"))
DOC_MAX_CHUNKS = int(os.environ.get("DOC_MAX_CHUNKS", "40"))
CHUNK_MAX = int(os.environ.get("CODE_CHUNK_MAX", "4000"))
CHUNK_MIN = int(os.environ.get("CODE_CHUNK_MIN", "1500"))
MAX_CHUNKS_PER_FILE = int(os.environ.get("CODE_MAX_CHUNKS_PER_FILE", "6"))

CODE_KIND = "flowos_code"
DOC_KIND = "flowos_repo_doc"

# Noise that never carries hand-written knowledge. Passed to repomix --ignore and
# used by the markdown walker.
IGNORE_GLOBS = [
    "**/node_modules/**", "**/dist/**", "**/build/**", "**/.next/**", "**/out/**",
    "**/vendor/**", "**/testdata/**", "**/__pycache__/**", "**/.venv/**", "**/venv/**",
    "**/target/**", "**/coverage/**", "**/.terraform/**", "**/Pods/**",
    "**/*.lock", "**/package-lock.json", "**/pnpm-lock.yaml", "**/yarn.lock",
    "**/go.sum", "**/Cargo.lock", "**/composer.lock", "**/poetry.lock",
    "**/*.min.js", "**/*.min.css", "**/*.bundle.js", "**/*.map",
    "**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.svg", "**/*.ico",
    "**/*.webp", "**/*.pdf", "**/*.zip", "**/*.gz", "**/*.tar", "**/*.woff",
    "**/*.woff2", "**/*.ttf", "**/*.mp4", "**/*.mp3", "**/*.wasm", "**/*.so",
    "**/*.dylib", "**/*.exe", "**/*.bin", "**/*.snap", "**/*.gen.go",
    "**/*_pb2.py", "**/*.pb.go", "**/generated/**", "**/gen/**",
    "**/*.md", "**/*.mdx",          # docs are indexed separately, as DOCS
]
SKIP_DIRS = {"node_modules", "dist", "build", ".next", "out", "vendor", "testdata",
             "__pycache__", ".venv", "venv", "target", "coverage", ".git", ".terraform"}

LANGS = {
    ".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript/React", ".js": "JavaScript",
    ".jsx": "JavaScript/React", ".py": "Python", ".rb": "Ruby", ".rs": "Rust",
    ".java": "Java", ".kt": "Kotlin", ".swift": "Swift", ".php": "PHP", ".cs": "C#",
    ".c": "C", ".h": "C header", ".cpp": "C++", ".sql": "SQL", ".sh": "Shell",
    ".yml": "YAML", ".yaml": "YAML", ".json": "JSON", ".tf": "Terraform",
    ".graphql": "GraphQL", ".proto": "Protobuf", ".css": "CSS", ".scss": "SCSS",
    ".html": "HTML", ".dart": "Dart", ".vue": "Vue", ".ex": "Elixir", ".m": "Objective-C",
}


def log(msg):
    print(msg, flush=True)


# ---------------------------------------------------------------- HTTP (curl) --
# Cloudflare 403s python-urllib's User-Agent in front of this API, so every call
# goes through curl.
def api(method, path, body=None, timeout=180):
    cmd = ["curl", "-sS", "-X", method, API + path,
           "-A", "cabrain-code-index/1.0",
           "-H", "Content-Type: application/json"]
    if TOKEN:
        cmd += ["-H", "X-Cabrain-Token: " + TOKEN]
    if body is not None:
        cmd += ["--data-binary", "@-"]
        raw = json.dumps(body)
    else:
        raw = None
    p = subprocess.run(cmd, input=raw, capture_output=True, text=True, timeout=timeout)
    if p.returncode != 0:
        raise RuntimeError("curl failed: " + p.stderr.strip()[:300])
    try:
        return json.loads(p.stdout)
    except Exception:
        raise RuntimeError("non-JSON response from %s: %s" % (path, p.stdout[:200]))


def retain(content, ref, kind, meta, valid_at):
    # indexer=repomix MARKS the rows this pipeline owns. Other producers write into
    # the same brain (and have used overlapping code: refs), so prune only ever
    # retires rows carrying this marker — never someone else's memories.
    meta = dict(meta, indexer="repomix")
    body = {"namespace": NS, "content": content, "sourceKind": kind, "sourceRef": ref,
            "metadata": meta}
    if valid_at:
        body["validAt"] = valid_at
    for attempt in range(3):
        try:
            r = api("POST", "/api/brain/retain", body)
            if "id" in r:
                return r["id"], r.get("decision", "?")
            return None, str(r)[:120]
        except Exception as e:
            if attempt == 2:
                log("    ! retain failed for %s: %s" % (ref, e))
                return None, "ERR"
            time.sleep(1.5 * (attempt + 1))


# ------------------------------------------------------------------- registry --
def registry():
    """Repos registered for repomix indexing, read from the datasources table."""
    r = api("GET", "/api/brain/datasources?namespace=" + NS)
    out = []
    for ds in r.get("datasources") or []:
        cfg = ds.get("config") or {}
        if ds.get("kind") != "github":
            continue
        if cfg.get("indexer") != "repomix" and not str(ds.get("name", "")).startswith("code:"):
            continue
        if not cfg.get("repo"):
            continue
        out.append(ds)
    return out


def cmd_add(args):
    repo = args.repo.strip().strip("/")
    if repo.startswith("https://github.com/"):
        repo = repo[len("https://github.com/"):].removesuffix(".git")
    if repo.count("/") != 1:
        sys.exit("repo must be owner/name")
    for ds in registry():
        if (ds["config"].get("repo") or "").lower() == repo.lower():
            log("already registered: %s (datasource %s)" % (repo, ds["id"]))
            return
    cfg = {"repo": repo, "indexer": "repomix"}
    if args.branch:
        cfg["branch"] = args.branch
    if args.include:
        cfg["include"] = args.include
    if args.ignore:
        cfg["ignore"] = args.ignore
    r = api("POST", "/api/brain/datasources",
            {"namespace": NS, "kind": "github", "name": "code:" + repo, "config": cfg})
    ds = r.get("datasource") or r
    log("registered %s -> datasource %s" % (repo, ds.get("id")))
    if args.now:
        index_repo(repo, cfg, load_state(), force=True)


def cmd_list(_args):
    st = load_state()
    rows = registry()
    if not rows:
        log("no repos registered in namespace %s" % NS)
        return
    log("%-34s %-10s %-22s %s" % ("REPO", "SHA", "LAST INDEXED", "CHUNKS(code/doc)"))
    for ds in sorted(rows, key=lambda d: d["config"]["repo"]):
        repo = ds["config"]["repo"]
        s = st.get(repo, {})
        log("%-34s %-10s %-22s %s/%s" % (
            repo, (s.get("sha") or "-")[:9], s.get("indexed_at", "never")[:19],
            s.get("code_chunks", "-"), s.get("doc_chunks", "-")))


def cmd_remove(args):
    for ds in registry():
        if ds["config"].get("repo", "").lower() == args.repo.lower():
            api("POST", "/api/brain/datasources/delete", {"id": ds["id"]})
            log("removed registry entry for %s (memories kept)" % args.repo)
            return
    log("not registered: %s" % args.repo)


# ---------------------------------------------------------------------- state --
def load_state():
    try:
        with open(STATE_PATH) as f:
            return json.load(f)
    except Exception:
        return {}


def save_state(st):
    os.makedirs(os.path.dirname(STATE_PATH), exist_ok=True)
    tmp = STATE_PATH + ".tmp"
    with open(tmp, "w") as f:
        json.dump(st, f, indent=1, sort_keys=True)
    os.replace(tmp, STATE_PATH)


# ---------------------------------------------------------------------- fetch --
def gh_token():
    tok = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if tok:
        return tok.strip()
    try:
        p = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True, timeout=20)
        if p.returncode == 0:
            return p.stdout.strip()
    except Exception:
        pass
    return ""


def git(args, cwd=None, token=None, check=True, timeout=600):
    """git with the credential passed per-command, so no token is ever written
    into .git/config."""
    cmd = ["git"]
    if token:
        basic = base64.b64encode(("x-access-token:" + token).encode()).decode()
        cmd += ["-c", "http.extraheader=AUTHORIZATION: basic " + basic]
    if cwd:
        cmd += ["-C", cwd]
    cmd += args
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    if check and p.returncode != 0:
        raise RuntimeError("git %s failed: %s" % (" ".join(args[:2]), p.stderr.strip()[:300]))
    return p.stdout.strip()


def ensure_clone(repo, branch, token):
    dest = os.path.join(CACHE, repo.replace("/", "__"))
    url = "https://github.com/%s.git" % repo
    if not os.path.isdir(os.path.join(dest, ".git")):
        os.makedirs(CACHE, exist_ok=True)
        args = ["clone", "--depth", "50", "--single-branch"]
        if branch:
            args += ["--branch", branch]
        args += [url, dest]
        git(args, token=token)
    else:
        ref = branch or "HEAD"
        git(["fetch", "--depth", "50", "origin", ref], cwd=dest, token=token)
        git(["reset", "--hard", "FETCH_HEAD"], cwd=dest, token=token)
        git(["clean", "-fdq"], cwd=dest, check=False)
    sha = git(["rev-parse", "HEAD"], cwd=dest)
    head_date = git(["log", "-1", "--format=%cI"], cwd=dest, check=False)
    return dest, sha, head_date


def file_dates(clone, limit=4000):
    """path -> last-commit ISO date, from ONE git log pass (shallow clone: only the
    fetched depth is visible; anything older falls back to the HEAD date)."""
    out = {}
    p = subprocess.run(["git", "-C", clone, "log", "--name-only", "--format=@%cI",
                        "-n", str(limit)], capture_output=True, text=True, timeout=300)
    cur = None
    for line in p.stdout.splitlines():
        if line.startswith("@"):
            cur = line[1:].strip()
        elif line.strip() and cur:
            out.setdefault(line.strip(), cur)
    return out


# ----------------------------------------------------------------------- pack --
FILE_HDR = re.compile(r"^#{1,6}\s+File:\s+(.+?)\s*$")


def run_repomix(clone, out_path, extra_ignore=None, include=None):
    ignore = list(IGNORE_GLOBS) + ([extra_ignore] if extra_ignore else [])
    cmd = list(REPOMIX_CMD) + [clone, "--style", "markdown", "--compress",
                               "-o", out_path, "--ignore", ",".join(ignore),
                               "--token-count-tree", "1000", "--top-files-len", "15"]
    if include:
        cmd += ["--include", include]
    p = subprocess.run(cmd, capture_output=True, text=True, timeout=1800,
                       cwd=os.path.dirname(out_path) or ".")
    if not os.path.exists(out_path):
        raise RuntimeError("repomix produced no pack: " + (p.stderr or p.stdout)[-400:])
    return strip_ansi(p.stdout)


ANSI = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]|\r")


def strip_ansi(s):
    s = ANSI.sub("", s or "")
    return "\n".join(l for l in s.splitlines() if not l.strip().startswith(("⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏")))


def token_profile(tree_out):
    """The size profile repomix reports: top files, the token tree, the totals."""
    out = []
    for marker, keep in (("Top 15 Files by Token Count", 17),
                         ("Token Count Tree", 20),
                         ("Pack Summary", 8)):
        i = (tree_out or "").find(marker)
        if i < 0:
            continue
        block = tree_out[i:].splitlines()[:keep]
        out += [l for l in block if l.strip() and not l.strip().startswith("──")]
        out.append("")
    return "\n".join(out).strip()


def parse_pack(path):
    """-> [(file_path, body)] from the repomix markdown pack."""
    sections, cur, buf = [], None, []
    with open(path, errors="replace") as f:
        for line in f:
            m = FILE_HDR.match(line)
            if m:
                if cur:
                    sections.append((cur, "".join(buf).strip()))
                cur, buf = m.group(1).strip(), []
            elif cur is not None:
                buf.append(line)
    if cur:
        sections.append((cur, "".join(buf).strip()))
    return [(p, b) for p, b in sections if b]


def split_body(body):
    """Split one file's body into ~CHUNK_MIN..CHUNK_MAX pieces on line boundaries."""
    if len(body) <= CHUNK_MAX:
        return [body]
    out, buf = [], []
    size = 0
    for line in body.splitlines(keepends=True):
        if size + len(line) > CHUNK_MAX and size >= CHUNK_MIN:
            out.append("".join(buf))
            buf, size = [], 0
        # a single monstrous line still has to be cut somewhere
        while len(line) > CHUNK_MAX:
            out.append(line[:CHUNK_MAX])
            line = line[CHUNK_MAX:]
        buf.append(line)
        size += len(line)
    if "".join(buf).strip():
        out.append("".join(buf))
    return out


def order_sections(sections, block=4):
    """Order files so a per-repo cap still yields BREADTH: directories are
    interleaved in blocks (biggest directory first), and within a directory the
    largest files lead. Without this, a cap would index only the alphabetically
    first folder. Blocks keep same-directory neighbours adjacent so tiny files can
    still be grouped into one chunk."""
    bydir = defaultdict(list)
    for p, b in sections:
        bydir[os.path.dirname(p) or "(root)"].append((p, b))
    for d in bydir:
        bydir[d].sort(key=lambda x: -len(x[1]))
    dirs = sorted(bydir, key=lambda d: -sum(len(b) for _, b in bydir[d]))
    out, idx = [], {d: 0 for d in dirs}
    while True:
        moved = False
        for d in dirs:
            part = bydir[d][idx[d]:idx[d] + block]
            if part:
                out += part
                idx[d] += block
                moved = True
        if not moved:
            return out


def build_chunks(repo, sections):
    """[(sourceRef, content, meta)] — one chunk per file where the file fits, big
    files split, tiny neighbours in the same directory merged so recall isn't
    flooded with 200-char memories."""
    chunks = []
    pending = []          # tiny files waiting to be grouped: (path, body)
    pending_dir = None
    pending_len = 0

    def flush():
        nonlocal pending, pending_dir, pending_len
        if not pending:
            return
        head = pending[0][0]
        parts = ["%s — %s\n\n%s" % (repo, p, b) for p, b in pending]
        content = "\n\n".join(parts)
        meta = {"type": "code", "repo": repo, "path": head,
                "lang": LANGS.get(os.path.splitext(head)[1], os.path.splitext(head)[1].lstrip(".") or "text")}
        if len(pending) > 1:
            meta["paths"] = [p for p, _ in pending]
        chunks.append(("code:%s:%s#1" % (repo, head), content, meta))
        pending, pending_dir, pending_len = [], None, 0

    for path, body in sections:
        d = os.path.dirname(path)
        if len(body) < CHUNK_MIN:
            if pending_dir is not None and (d != pending_dir or pending_len + len(body) > CHUNK_MAX):
                flush()
            pending_dir = d
            pending.append((path, body))
            pending_len += len(body)
            if pending_len >= CHUNK_MIN:
                flush()
            continue
        flush()
        pieces = split_body(body)[:MAX_CHUNKS_PER_FILE]
        lang = LANGS.get(os.path.splitext(path)[1], os.path.splitext(path)[1].lstrip(".") or "text")
        for i, piece in enumerate(pieces, 1):
            head = "%s — %s" % (repo, path)
            if len(pieces) > 1:
                head += "  (part %d/%d)" % (i, len(pieces))
            chunks.append(("code:%s:%s#%d" % (repo, path, i),
                           head + "\n\n" + piece,
                           {"type": "code", "repo": repo, "path": path, "lang": lang,
                            "part": i, "parts": len(pieces)}))
    flush()
    return chunks


# ------------------------------------------------------------------ docs (md) --
HEAD_SPLIT = re.compile(r"(?m)^(?=#{1,3} )")


def md_chunks(text, limit=2800):
    parts, packed, buf = HEAD_SPLIT.split(text), [], ""
    for p in parts:
        if len(buf) + len(p) > limit and buf:
            packed.append(buf)
            buf = p
        else:
            buf += p
    if buf.strip():
        packed.append(buf)
    out = []
    for c in packed:
        while len(c) > limit * 2:
            out.append(c[:limit * 2])
            c = c[limit * 2:]
        if c.strip():
            out.append(c)
    return out


def collect_docs(clone):
    docs = []
    for dp, dns, fns in os.walk(clone):
        dns[:] = [d for d in dns if d not in SKIP_DIRS and not d.startswith(".git")]
        for fn in fns:
            if not (fn.endswith(".md") or fn.endswith(".mdx")):
                continue
            full = os.path.join(dp, fn)
            try:
                if os.path.getsize(full) > 400_000:
                    continue
                with open(full, errors="replace") as f:
                    txt = f.read()
            except Exception:
                continue
            if len(txt.strip()) < 120:
                continue
            docs.append((os.path.relpath(full, clone), txt))
    # README / docs/ first — highest-value knowledge leads
    def rank(item):
        rel = item[0].lower()
        return (0 if "readme" in rel else 1 if rel.startswith("docs/") else 2, rel)
    return sorted(docs, key=rank)


# ---------------------------------------------------------------------- graph --
def db():
    if not DSN:
        return None
    try:
        import pg8000.native as pg
    except Exception:
        log("  (graph skipped: pg8000 not importable)")
        return None
    from urllib.parse import urlparse
    u = urlparse(DSN)
    c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=u.path.lstrip("/"))
    c.run("SET search_path = public")
    return c


def upsert_entity(c, name, etype, summary=None):
    r = c.run("""INSERT INTO entities (namespace, name, entity_type, summary)
                 VALUES (:ns,:n,:t,:s)
                 ON CONFLICT (namespace, name) DO UPDATE
                   SET entity_type = COALESCE(entities.entity_type, EXCLUDED.entity_type),
                       summary     = COALESCE(EXCLUDED.summary, entities.summary)
                 RETURNING id""", ns=NS, n=name, t=etype, s=summary)
    return r[0][0]


def add_edge(c, src, dst, rel, fact, when):
    if c.run("""SELECT 1 FROM entity_edges WHERE namespace=:ns AND src_id=:s AND dst_id=:d
                  AND relation=:r AND valid_to IS NULL LIMIT 1""",
             ns=NS, s=src, d=dst, r=rel):
        return 0
    c.run("""INSERT INTO entity_edges (namespace,src_id,dst_id,relation,fact,valid_from)
             VALUES (:ns,:s,:d,:r,:f, COALESCE(:w::timestamptz, now()))
             ON CONFLICT (namespace,src_id,dst_id,relation,valid_from) DO NOTHING""",
          ns=NS, s=src, d=dst, r=rel, f=fact, w=when)
    return 1


def link_memory(c, mem_id, ent_id):
    if not mem_id:
        return
    c.run("""INSERT INTO memory_entities (memory_id, entity_id)
             VALUES (CAST(:m AS uuid), CAST(:e AS uuid)) ON CONFLICT DO NOTHING""",
          m=mem_id, e=ent_id)


def venture_for_repo(repo):
    """FlowOS prod (READ ONLY) — which venture claims this github_url."""
    if not FLOWOS_DSN:
        return None
    try:
        import pg8000.native as pg
        from urllib.parse import urlparse
        u = urlparse(FLOWOS_DSN)
        c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                          port=u.port or 5432, database=u.path.lstrip("/"))
        c.run("BEGIN READ ONLY")
        rows = c.run("""SELECT name FROM ventures
                        WHERE github_url ILIKE :p AND github_url <> '' LIMIT 1""",
                     p="%" + repo + "%")
        c.run("ROLLBACK")
        return rows[0][0] if rows else None
    except Exception as e:
        log("  (venture lookup skipped: %s)" % str(e)[:100])
        return None


# ------------------------------------------------------------------ index one --
def index_repo(repo, cfg, state, force=False, dry=False):
    t0 = time.time()
    log("== %s" % repo)
    token = gh_token()
    try:
        clone, sha, head_date = ensure_clone(repo, cfg.get("branch"), token)
    except Exception as e:
        log("  ! clone failed: %s" % e)
        return {"repo": repo, "status": "clone_failed", "error": str(e)[:200]}
    prev = state.get(repo, {})
    if prev.get("sha") == sha and not force:
        log("  unchanged (%s) — skipped" % sha[:9])
        return {"repo": repo, "status": "skipped", "sha": sha}

    packdir = os.path.join(CACHE, "_packs")
    os.makedirs(packdir, exist_ok=True)
    pack = os.path.join(packdir, repo.replace("/", "__") + ".md")
    try:
        tree_out = run_repomix(clone, pack, extra_ignore=cfg.get("ignore"), include=cfg.get("include"))
    except Exception as e:
        log("  ! repomix failed: %s" % e)
        return {"repo": repo, "status": "pack_failed", "error": str(e)[:200]}

    sections = parse_pack(pack)
    dates = file_dates(clone)
    chunks = build_chunks(repo, order_sections(sections))
    capped_code = chunks[:CODE_MAX_CHUNKS]
    log("  pack %.1f KB · %d files · %d chunks (indexing %d)"
        % (os.path.getsize(pack) / 1024, len(sections), len(chunks), len(capped_code)))

    docs = collect_docs(clone)
    doc_units = []
    for rel, txt in docs:
        for i, ch in enumerate(md_chunks(txt), 1):
            doc_units.append(("doc:%s:%s#%d" % (repo, rel, i),
                              "%s — %s (doc)\n\n%s" % (repo, rel, ch),
                              {"type": "doc", "repo": repo, "path": rel, "lang": "markdown",
                               "part": i}))
    capped_docs = doc_units[:DOC_MAX_CHUNKS]
    log("  docs: %d markdown files -> %d chunks (indexing %d)"
        % (len(docs), len(doc_units), len(capped_docs)))

    if dry:
        return {"repo": repo, "status": "dry", "code": len(capped_code), "doc": len(capped_docs)}

    seen_refs, mem_ids = [], []
    n_ok = 0
    for ref, content, meta in capped_code:
        meta = dict(meta, sha=sha)
        va = dates.get(meta["path"]) or head_date
        mid, _ = retain(content, ref, CODE_KIND, meta, va)
        seen_refs.append(ref)
        if mid:
            mem_ids.append((mid, meta["path"], "code"))
            n_ok += 1
    n_doc = 0
    for ref, content, meta in capped_docs:
        meta = dict(meta, sha=sha)
        va = dates.get(meta["path"]) or head_date
        mid, _ = retain(content, ref, DOC_KIND, meta, va)
        seen_refs.append(ref)
        if mid:
            mem_ids.append((mid, meta["path"], "doc"))
            n_doc += 1

    # ---- the INDEX overview memory (entry point for "tell me about this repo") --
    overview_ref = "code:%s:INDEX" % repo
    overview = build_overview(repo, clone, sections, docs, tree_out, sha, head_date,
                              len(capped_code), len(chunks), n_doc, len(doc_units))
    oid, _ = retain(overview, overview_ref, DOC_KIND,
                    {"type": "doc", "subtype": "repo_index", "repo": repo,
                     "path": "INDEX", "sha": sha}, head_date)
    seen_refs.append(overview_ref)

    # ---- graph + prune (needs the DB) ------------------------------------------
    graph_stats = {}
    c = db()
    if c:
        try:
            graph_stats = do_graph(c, repo, mem_ids, oid, head_date, sections)
            graph_stats["retired"] = prune(c, repo, seen_refs)
        except Exception as e:
            log("  ! graph/prune error: %s" % str(e)[:200])
        finally:
            try:
                c.close()
            except Exception:
                pass

    state[repo] = {"sha": sha, "indexed_at": datetime.now(timezone.utc).isoformat(),
                   "code_chunks": n_ok, "doc_chunks": n_doc,
                   "files": len(sections), "docs": len(docs)}
    log("  retained: %d code + %d doc + 1 index  %s  (%.0fs)"
        % (n_ok, n_doc, graph_stats or "", time.time() - t0))
    return {"repo": repo, "status": "indexed", "sha": sha, "code": n_ok, "doc": n_doc,
            **graph_stats}


def build_overview(repo, clone, sections, docs, tree_out, sha, head_date,
                   n_code_indexed, n_code_total, n_doc_indexed, n_doc_total):
    dirs = Counter()
    exts = Counter()
    sizes = []
    for path, body in sections:
        top = path.split("/")[0] if "/" in path else "(root)"
        dirs[top] += 1
        exts[os.path.splitext(path)[1]] += 1
        sizes.append((len(body), path))
    sizes.sort(reverse=True)
    langs = Counter()
    for e, n in exts.items():
        langs[LANGS.get(e, e.lstrip(".") or "other")] += n
    readme = ""
    for rel, txt in docs:
        if os.path.basename(rel).lower().startswith("readme"):
            readme = re.sub(r"\n{3,}", "\n\n", txt.strip())[:700]
            break
    tok = token_profile(tree_out)
    total_chars = sum(s for s, _ in sizes)

    out = []
    out.append("REPO INDEX — %s (indexed by the CaBrain repomix pipeline)" % repo)
    out.append("")
    out.append("HEAD %s, last commit %s. The compressed repomix pack covers %d source "
               "files (~%d KB of structure-extracted code); %d of %d code chunks and "
               "%d of %d doc chunks are stored as memories."
               % (sha[:9], (head_date or "?")[:10], len(sections), total_chars // 1024,
                  n_code_indexed, n_code_total, n_doc_indexed, n_doc_total))
    out.append("")
    out.append("LANGUAGES: " + ", ".join("%s (%d files)" % (l, n) for l, n in langs.most_common(8)))
    out.append("")
    out.append("STRUCTURE (top-level, files packed): "
               + ", ".join("%s %d" % (d, n) for d, n in dirs.most_common(14)))
    out.append("")
    out.append("BIGGEST / MOST IMPORTANT FILES:")
    for s, p in sizes[:12]:
        out.append("  - %s (%d chars)" % (p, s))
    if docs:
        out.append("")
        out.append("DOCS PRESENT: " + ", ".join(rel for rel, _ in docs[:12])
                   + (" …" if len(docs) > 12 else ""))
    if readme:
        out.append("")
        out.append("README (opening):")
        out.append(readme)
    if tok:
        out.append("")
        out.append("TOKEN / SIZE PROFILE (repomix):")
        out.append(tok)
    out.append("")
    out.append("Chunks are recallable individually: source_ref code:%s:<path>#<n> "
               "(source_kind flowos_code) and doc:%s:<path>#<n> (flowos_repo_doc)."
               % (repo, repo))
    return "\n".join(out)[:12000]


def do_graph(c, repo, mem_ids, oid, head_date, sections):
    repo_ent = upsert_entity(c, repo, "repo")
    edges = 0
    links = 0
    # code_module entities: top-level dirs actually packed (bounded)
    dirs = Counter(p.split("/")[0] if "/" in p else "(root)" for p, _ in sections)
    mod_ent = {}
    for d, n in dirs.most_common(30):
        e = upsert_entity(c, "%s:%s/" % (repo, d), "code_module",
                          "%s files packed from %s/%s" % (n, repo, d))
        mod_ent[d] = e
        edges += add_edge(c, e, repo_ent, "CODE_IN",
                          "%s/%s is a module of the %s codebase" % (repo, d, repo), head_date)
    doc_ent = {}
    for mid, path, kind in mem_ids:
        link_memory(c, mid, repo_ent)
        links += 1
        if kind == "doc":
            if path not in doc_ent and len(doc_ent) < 60:
                e = upsert_entity(c, "%s:%s" % (repo, path), "doc",
                                  "documentation file %s in %s" % (path, repo))
                doc_ent[path] = e
                edges += add_edge(c, e, repo_ent, "DOC_IN",
                                  "%s documents the %s repository" % (path, repo), head_date)
            if path in doc_ent:
                link_memory(c, mid, doc_ent[path])
        else:
            top = path.split("/")[0] if "/" in path else "(root)"
            if top in mod_ent:
                link_memory(c, mid, mod_ent[top])
    if oid:
        link_memory(c, oid, repo_ent)
    # REPO_OF: repo -> venture, when FlowOS says the venture owns this github_url
    vname = venture_for_repo(repo)
    if vname:
        r = c.run("SELECT id FROM entities WHERE namespace=:ns AND name=:n LIMIT 1",
                  ns=NS, n=vname)
        if r:
            edges += add_edge(c, repo_ent, r[0][0], "REPO_OF",
                              "%s is a code repository of the venture %s" % (repo, vname),
                              head_date)
    return {"edges": edges, "mem_links": links}


def prune(c, repo, seen_refs):
    """Soft-retire chunks of THIS repo whose file/part no longer exists. Never
    deletes, and never touches a row this pipeline did not write (metadata.indexer
    = repomix) — other agents write into the same brain with overlapping refs."""
    live = c.run("""SELECT source_ref FROM memories
                    WHERE namespace=:ns AND invalid_at IS NULL
                      AND metadata->>'indexer' = 'repomix'
                      AND (source_ref LIKE :a OR source_ref LIKE :b)""",
                 ns=NS, a="code:%s:%%" % repo, b="doc:%s:%%" % repo)
    seen = set(seen_refs)
    stale = [r[0] for r in live if r[0] not in seen]
    for ref in stale:
        c.run("""UPDATE memories SET invalid_at = now()
                 WHERE namespace=:ns AND source_ref=:r AND invalid_at IS NULL
                   AND metadata->>'indexer' = 'repomix'""",
              ns=NS, r=ref)
    return len(stale)


def cmd_graph(args):
    """Rebuild the graph purely FROM THE BRAIN DB — no clone needed. The indexing
    run does this inline when CABRAIN_DSN is set; this is the catch-up path for
    repos indexed from a host that can only reach the HTTPS API (e.g. the systemd
    timer), and it is idempotent."""
    c = db()
    if not c:
        sys.exit("graph needs CABRAIN_DSN (and pg8000)")
    rows = c.run("""SELECT id::text, source_kind, metadata->>'repo', metadata->>'path',
                           valid_at
                    FROM memories
                    WHERE namespace=:ns AND invalid_at IS NULL
                      AND source_kind IN (:ck,:dk) AND metadata->>'repo' IS NOT NULL""",
                 ns=NS, ck=CODE_KIND, dk=DOC_KIND)
    byrepo = defaultdict(list)
    for mid, kind, repo, path, va in rows:
        if args.repo and repo.lower() != args.repo.lower():
            continue
        byrepo[repo].append((mid, path or "", "doc" if kind == DOC_KIND else "code", va))
    tot_e = tot_l = 0
    for repo, items in sorted(byrepo.items()):
        head_date = max((v for _, _, _, v in items if v), default=None)
        sections = [(p, "") for _, p, k, _ in items if k == "code" and p]
        st = do_graph(c, repo, [(m, p, k) for m, p, k, _ in items], None,
                      head_date.isoformat() if head_date else None, sections)
        tot_e += st["edges"]
        tot_l += st["mem_links"]
        log("  %-40s memories=%-4d edges+%-3d links=%d" % (repo, len(items), st["edges"], st["mem_links"]))
    log("graph: %d repos, %d new edges, %d memory links" % (len(byrepo), tot_e, tot_l))
    c.close()


# --------------------------------------------------------------------- runner --
def cmd_run(args):
    state = load_state()
    rows = registry()
    if args.repo:
        rows = [d for d in rows if d["config"]["repo"].lower() == args.repo.lower()]
        if not rows:
            # allow ad-hoc indexing of an unregistered repo
            rows = [{"config": {"repo": args.repo}, "name": "ad-hoc"}]
    if args.limit:
        rows = rows[:args.limit]
    log("code-index: %d repo(s), namespace=%s, api=%s" % (len(rows), NS, API))
    results = []
    for ds in rows:
        cfg = ds["config"]
        try:
            results.append(index_repo(cfg["repo"], cfg, state, force=args.force, dry=args.dry_run))
        except Exception as e:
            log("  ! %s failed: %s" % (cfg["repo"], str(e)[:200]))
            results.append({"repo": cfg["repo"], "status": "error", "error": str(e)[:200]})
        save_state(state)
    done = [r for r in results if r.get("status") == "indexed"]
    log("done: %d indexed, %d skipped, %d failed  (code %d, doc %d)" % (
        len(done),
        sum(1 for r in results if r.get("status") == "skipped"),
        sum(1 for r in results if r.get("status") in ("error", "clone_failed", "pack_failed")),
        sum(r.get("code", 0) for r in done), sum(r.get("doc", 0) for r in done)))
    return results


# ----------------------------------------------------------------- candidates --
# 459 repos exist across the FlowOS orgs but only ~33 are indexed. Indexing all of
# them would drown recall (this brain has been drowned twice already), so this
# command only SURFACES the gap: repos that received commits recently but are not
# in the registry. It writes ONE rollup memory, never one per repo, and never
# registers anything by itself — `code-index.py add <repo>` stays a human choice.
def cmd_candidates(args):
    dsn = os.environ.get("FLOWOS_DSN", "")
    if not dsn:
        sys.exit("FLOWOS_DSN must be set (read-only FlowOS prod) for `candidates`")
    import pg8000.native as pg
    from urllib.parse import urlparse
    u = urlparse(dsn)
    c = pg.Connection(user=u.username, password=u.password, host=u.hostname,
                      port=u.port or 5432, database=(u.path or "/").lstrip("/"))
    try:
        c.run("BEGIN READ ONLY")          # prod is strictly read-only to us
        rows = c.run("""
            SELECT repo_full_name, count(*), max(authored_at),
                   count(DISTINCT author_login)
            FROM github_commits
            WHERE authored_at >= now() - make_interval(days => :d)
            GROUP BY 1 ORDER BY 2 DESC""", d=args.days)
        c.run("ROLLBACK")
    finally:
        c.close()

    known = {(ds["config"].get("repo") or "").lower() for ds in registry()}
    miss = [r for r in rows if r[0] and r[0].lower() not in known]
    log("%d repos with commits in the last %d days; %d registered, %d NOT indexed"
        % (len(rows), args.days, len(rows) - len(miss), len(miss)))
    log("%-40s %8s %8s  %s" % ("REPO", "COMMITS", "AUTHORS", "LAST COMMIT"))
    for name, n, last, authors in miss[:args.top]:
        log("%-40s %8d %8d  %s" % (name, n, authors, last))

    if args.dry_run:
        return
    top = miss[:args.top]
    if not top:
        body = ("Code-index coverage: every FlowOS repo with commits in the last "
                "%d days is already registered for repomix indexing." % args.days)
    else:
        body = (
            "Code-index coverage gap as of %s: %d GitHub repositories received "
            "commits in the last %d days and are NOT in the CaBrain code-index "
            "registry, so their source code is absent from the brain. Most active "
            "unindexed: %s. Register one with `code-index.py add <owner>/<repo>`; "
            "they are deliberately NOT auto-indexed because bulk-indexing every "
            "repo crowds operational memories out of recall."
            % (dt_now().strftime("%Y-%m-%d"), len(miss), args.days,
               "; ".join("%s (%d commits by %d authors, last %s)"
                         % (r[0], r[1], r[3], r[2].strftime("%Y-%m-%d"))
                         for r in top)))
    mid, dec = retain(body, "code:index-coverage-gap", "flowos_code_coverage",
                      {"type": "code-coverage", "unindexed": len(miss),
                       "window_days": args.days},
                      dt_now().strftime("%Y-%m-%dT%H:%M:%SZ"))
    log("coverage memory %s (%s)" % (mid, dec))


def dt_now():
    return datetime.now(timezone.utc)


def main():
    ap = argparse.ArgumentParser(description="CaBrain repomix code indexer")
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("add", help="register a repo (datasources row) ")
    a.add_argument("repo")
    a.add_argument("--branch")
    a.add_argument("--include", help="repomix --include globs")
    a.add_argument("--ignore", help="extra repomix --ignore globs")
    a.add_argument("--now", action="store_true", help="index immediately after adding")
    a.set_defaults(func=cmd_add)
    l = sub.add_parser("list", help="show the registry + last indexed sha")
    l.set_defaults(func=cmd_list)
    rm = sub.add_parser("remove", help="unregister (memories are kept)")
    rm.add_argument("repo")
    rm.set_defaults(func=cmd_remove)
    r = sub.add_parser("run", help="index changed repos")
    r.add_argument("--repo")
    r.add_argument("--force", action="store_true")
    r.add_argument("--limit", type=int)
    r.add_argument("--dry-run", action="store_true")
    r.set_defaults(func=cmd_run)
    g = sub.add_parser("graph", help="rebuild entities/edges/links from the DB (catch-up)")
    g.add_argument("--repo")
    g.set_defaults(func=cmd_graph)
    cd = sub.add_parser("candidates",
                        help="active repos that are NOT code-indexed (surface, don't index)")
    cd.add_argument("--days", type=int, default=14)
    cd.add_argument("--top", type=int, default=15, help="how many to name in the memory")
    cd.add_argument("--dry-run", action="store_true", help="print only, write nothing")
    cd.set_defaults(func=cmd_candidates)
    args = ap.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
