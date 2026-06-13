#!/bin/zsh

set -euo pipefail

APP_ROOT="$HOME/Applications/codex-app-proxy"
PID_FILE="$HOME/.codex-app-proxy.pid"
LOG_FILE="$HOME/Library/Logs/codex-app-proxy.log"
NODE_BIN="/opt/homebrew/bin/node"

mkdir -p "$(dirname "$LOG_FILE")"

if [[ -f "$PID_FILE" ]]; then
  PID="$(cat "$PID_FILE")"
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    exit 0
  fi
  rm -f "$PID_FILE"
fi

cd "$APP_ROOT"
nohup "$NODE_BIN" src/server.js >>"$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"
