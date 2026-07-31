#!/usr/bin/env python3
"""Register one github datasource per indexed repo ("code:<owner>/<name>").

The datasource is the durable CONNECTION: it records which repo/branch a brain is
allowed to read and with which volume filters, so a later `datasources/sync` can
refresh the index without anyone re-deriving the policy. The filter fields
(ext list, exclude, maxBytes, maxDocs, fileDates) are read by the extended github
connector in plugins/brain/internal/brain/connectors.go — an older deployed build
ignores the list form of `ext` and falls back to markdown-only, so check the
running version before mass-syncing.

Idempotent: a datasource whose name already exists in the namespace is skipped.

Env (never hardcode):
  GITHUB_TOKEN, CABRAIN_URL, CABRAIN_TOKEN, BRAIN_NS
Usage:
  python3 scripts/create-code-datasources.py [--repos scripts/flowos-code-repos.json]
                                             [--sync owner/name]...
"""

import argparse
import json
import os
import subprocess
import sys

CABRAIN_URL = os.environ.get("CABRAIN_URL", "https://cabrain.fadymondy.com").rstrip("/")
CABRAIN_TOKEN = os.environ.get("CABRAIN_TOKEN", "")
GH_TOKEN = os.environ.get("GITHUB_TOKEN", "")
NS = os.environ.get("BRAIN_NS", "flowos")

CODE_EXTS = [".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".sql", ".md",
             ".yaml", ".yml", ".tf", ".php", ".vue"]
EXCLUDE = ["node_modules/", "vendor/", "dist/", "build/", ".next/", "testdata/",
           "__pycache__/", "coverage/", "public/build/", "storage/framework/",
           "locales/", "translations/", "assets/", "fixtures/", "seeders/"]


def api(method, path, body=None):
    cmd = ["curl", "-s", "--max-time", "300", "-X", method, CABRAIN_URL + path,
           "-H", "X-Cabrain-Token: " + CABRAIN_TOKEN]
    if body is not None:
        cmd += ["-H", "content-type: application/json", "--data-binary", "@-"]
    p = subprocess.run(cmd, input=json.dumps(body) if body is not None else None,
                       capture_output=True, text=True)
    try:
        return json.loads(p.stdout)
    except json.JSONDecodeError:
        return {"error": (p.stdout or p.stderr)[:200]}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repos", default=os.path.join(os.path.dirname(__file__), "flowos-code-repos.json"))
    ap.add_argument("--sync", action="append", default=[])
    args = ap.parse_args()
    if not CABRAIN_TOKEN:
        sys.exit("CABRAIN_TOKEN is required")

    existing = {d["name"]: d["id"] for d in api("GET", f"/api/brain/datasources?namespace={NS}").get("datasources") or []}
    entries = json.load(open(args.repos))["repos"]
    made = {}
    for e in entries:
        repo, branch = e["repo"], e.get("branch") or "main"
        name = "code:" + repo
        if name in existing:
            made[repo] = existing[name]
            print(json.dumps({"repo": repo, "datasource": existing[name], "status": "exists"}))
            continue
        cfg = {"repo": repo, "branch": branch, "ext": CODE_EXTS, "exclude": EXCLUDE,
               "maxBytes": 60000, "maxDocs": 60, "fileDates": True}
        if GH_TOKEN:
            cfg["token"] = GH_TOKEN
        r = api("POST", "/api/brain/datasources",
                {"namespace": NS, "kind": "github", "name": name, "config": cfg})
        did = (r.get("datasource") or r).get("id")
        made[repo] = did
        print(json.dumps({"repo": repo, "datasource": did, "status": "created",
                          "error": r.get("error")}))
    for repo in args.sync:
        did = made.get(repo)
        if not did:
            print(json.dumps({"repo": repo, "sync": "no datasource"}))
            continue
        res = api("POST", "/api/brain/datasources/sync", {"id": did})
        print(json.dumps({"repo": repo, "sync": res}))


if __name__ == "__main__":
    main()
