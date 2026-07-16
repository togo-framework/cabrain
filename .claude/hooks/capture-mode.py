#!/usr/bin/env python3
"""CaBrain capture mode — SPEC §6.

A Claude Code `Stop` hook: at the end of an assistant turn it reads the turn's
final assistant message from the transcript, keeps it only if it states a durable
decision / correction / fact (heuristic), redacts <private>…</private> spans and
anything that looks like a secret, and fire-and-forget POSTs it to memory_retain.

Best-effort by contract: any error (endpoint down, non-worthy turn, all-private)
exits 0 silently. The live session is authoritative; capture only accumulates mass.

OPT-IN. Does nothing unless CABRAIN_CAPTURE=1. Config via env:
  CABRAIN_CAPTURE=1                 enable
  CABRAIN_API_URL=http://localhost:8080
  CABRAIN_NAMESPACE=<name>          override the derived project namespace
  CABRAIN_AGENT_ID=claude-code      X-Agent-Id (F5 scoping)
"""
import json
import os
import re
import sys
import urllib.request

TIMEOUT = 2.0  # never add latency to the interactive loop


def main() -> int:
    if os.environ.get("CABRAIN_CAPTURE") != "1":
        return 0
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0

    text = last_assistant_text(payload.get("transcript_path", ""))
    if not text:
        return 0

    content = redact(text)
    if not content or not worthy(content):
        return 0

    ns = namespace(payload)
    session = payload.get("session_id", "") or "unknown"
    post(ns, content, session)
    return 0  # always succeed; capture is best-effort


def last_assistant_text(path: str) -> str:
    """Return the concatenated text of the final assistant message in the transcript."""
    if not path or not os.path.exists(path):
        return ""
    last = ""
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except Exception:
                    continue
                if rec.get("type") != "assistant" and rec.get("role") != "assistant":
                    continue
                msg = rec.get("message", rec)
                parts = msg.get("content", "")
                if isinstance(parts, str):
                    last = parts
                elif isinstance(parts, list):
                    chunks = [p.get("text", "") for p in parts
                              if isinstance(p, dict) and p.get("type") == "text"]
                    if any(chunks):
                        last = "\n".join(c for c in chunks if c)
    except Exception:
        return ""
    return last.strip()


PRIVATE_RE = re.compile(r"<private>.*?</private>", re.DOTALL | re.IGNORECASE)
# Implicitly-private: obvious credential/secret shapes — drop the whole turn if seen.
SECRET_RE = re.compile(
    r"(AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|"
    r"\b[A-Za-z0-9_\-]*(?:password|secret|token|api[_-]?key)[\"'\s:=]+\S{6,})",
    re.IGNORECASE,
)


def redact(text: str) -> str:
    text = PRIVATE_RE.sub("", text)
    if SECRET_RE.search(text):
        return ""  # treat a secret-bearing turn as fully private
    return text.strip()


# Turns that state a conclusion / choice / correction / durable fact are worthy.
WORTHY_RE = re.compile(
    r"\b(decid|chose|choose|because|instead of|rather than|turns out|"
    r"the (?:reason|issue|fix|root cause)|should (?:use|not)|"
    r"constraint|gotcha|note that|important|prefer|corrected|"
    r"endpoint|schema|contract|convention)\b",
    re.IGNORECASE,
)


def worthy(content: str) -> bool:
    if len(content) < 40:        # too short to carry a durable fact
        return False
    if len(content) > 6000:      # a whole essay/code dump — store a pointer, not the blob
        return WORTHY_RE.search(content[:6000]) is not None
    return WORTHY_RE.search(content) is not None


def namespace(payload: dict) -> str:
    if ns := os.environ.get("CABRAIN_NAMESPACE"):
        return ns
    cwd = payload.get("cwd") or os.getcwd()
    return os.path.basename(cwd.rstrip("/")).lower() or "default"


def post(ns: str, content: str, session: str) -> None:
    base = os.environ.get("CABRAIN_API_URL", "http://localhost:8080").rstrip("/")
    body = json.dumps({
        "namespace": ns,
        "content": content,
        "sourceKind": "claude_code",
        "sourceRef": session,
    }).encode()
    req = urllib.request.Request(base + "/api/brain/retain", data=body,
                                 headers={"Content-Type": "application/json"})
    if agent := os.environ.get("CABRAIN_AGENT_ID"):
        req.add_header("X-Agent-Id", agent)
    try:
        urllib.request.urlopen(req, timeout=TIMEOUT).read()
    except Exception:
        pass  # best-effort: endpoint may be down (pre-deploy) or embed unavailable


if __name__ == "__main__":
    sys.exit(main())
