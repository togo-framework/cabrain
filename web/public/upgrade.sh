#!/bin/sh
# cabrain-cli upgrade — update to the latest version, however you installed it.
#
#   curl -fsSL https://cabrain.fadymondy.com/upgrade.sh | sh
#
# Detects an npm global install vs. a standalone binary and updates in place.
set -eu

say()  { printf '\033[1;36m›\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }

BIN="$(command -v cabrain 2>/dev/null || true)"
before="$("$BIN" version 2>/dev/null || echo 'not installed')"
say "current: $before"

# Not installed at all → hand off to the installer.
if [ -z "$BIN" ]; then
  warn "cabrain not found on PATH — running the installer"
  exec sh -c "$(curl -fsSL https://cabrain.fadymondy.com/install.sh)"
fi

updated=0

# 1) npm global install? (path under node_modules, or npm knows the package)
case "$BIN" in
  *node_modules*|*/lib/node_modules/*) npm_install=1 ;;
  *) npm_install=0 ;;
esac
if [ "$npm_install" = 0 ] && command -v npm >/dev/null 2>&1; then
  if npm ls -g cabrain-cli >/dev/null 2>&1; then npm_install=1; fi
fi

if [ "$npm_install" = 1 ] && command -v npm >/dev/null 2>&1; then
  say "npm global install → npm i -g cabrain-cli@latest"
  npm i -g cabrain-cli@latest
  updated=1
fi

# 2) go install? (binary under a Go bin dir)
if [ "$updated" = 0 ]; then
  case "$BIN" in
    *"/go/bin/"*|"${GOBIN:-/nonexistent}"/*)
      if command -v go >/dev/null 2>&1; then
        say "go install → go install github.com/togo-framework/cabrain-cli@latest"
        GOBIN="$(dirname "$BIN")" go install github.com/togo-framework/cabrain-cli@latest
        updated=1
      fi ;;
  esac
fi

# 3) standalone binary from install.sh → re-download latest into the same dir
if [ "$updated" = 0 ]; then
  say "standalone binary at $BIN → downloading the latest release into $(dirname "$BIN")"
  CABRAIN_BIN_DIR="$(dirname "$BIN")" sh -c "$(curl -fsSL https://cabrain.fadymondy.com/install.sh)"
  updated=1
fi

after="$(cabrain version 2>/dev/null || echo '?')"
if [ "$before" = "$after" ]; then
  say "already up to date ($after)"
else
  say "upgraded: $before → $after"
fi
