#!/usr/bin/env bash
# togo PostToolUse hook: gofmt Go files after Claude edits them.
input=$(cat)
file=$(printf '%s' "$input" | sed -n 's/.*"file_path"[: ]*"\([^"]*\)".*/\1/p' | head -1)
case "$file" in
  *.go) command -v gofmt >/dev/null 2>&1 && gofmt -w "$file" >/dev/null 2>&1 ;;
esac
exit 0
