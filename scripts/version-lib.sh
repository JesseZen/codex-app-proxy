#!/usr/bin/env bash
# version-lib.sh — shared version management functions
# Sourced by scripts/bump and git hooks.
#
# .version file format (3 lines):
#   line 1: base version (x.y.z)
#   line 2: prerelease type (alpha|beta|rc|release)
#   line 3: prerelease number (integer)

# ── Discover project root ───────────────────────────────────────────
_version_find_root() {
  if [[ -n "${VERSION_ROOT:-}" ]]; then
    ROOT_DIR="$VERSION_ROOT"
  elif ROOT_DIR="$(git rev-parse --show-toplevel 2>/dev/null)" && [[ -f "$ROOT_DIR/.version" ]]; then
    : # found via git
  else
    # Walk up from script location or cwd
    local dir="${BASH_SOURCE[0]}"
    dir="$(cd "$(dirname "$dir")" && pwd)"
    while [[ "$dir" != "/" ]]; do
      if [[ -f "$dir/.version" ]]; then
        ROOT_DIR="$dir"
        return
      fi
      dir="$(dirname "$dir")"
    done
    ROOT_DIR="$(pwd)"
  fi
  VERSION_FILE="$ROOT_DIR/.version"
}

# ── Read .version state ─────────────────────────────────────────────
version_read() {
  _version_find_root
  if [[ ! -f "$VERSION_FILE" ]]; then
    echo "Error: .version file not found" >&2
    return 1
  fi
  BASE_VERSION=$(sed -n '1p' "$VERSION_FILE" | tr -d '[:space:]')
  PRERELEASE=$(sed -n '2p' "$VERSION_FILE" | tr -d '[:space:]')
  PRE_NUM=$(sed -n '3p' "$VERSION_FILE" | tr -d '[:space:]')
}

# ── Write .version state ────────────────────────────────────────────
version_write() {
  local base="$1" pre="$2" num="$3"
  printf '%s\n%s\n%s\n' "$base" "$pre" "$num" > "$VERSION_FILE"
}

# ── Compute full version string ─────────────────────────────────────
version_full() {
  local base="$1" pre="$2" num="$3"
  if [[ "$pre" == "release" || -z "$pre" ]]; then
    echo "$base"
  else
    echo "$base-$pre.$num"
  fi
}

# ── Auto-increment: compute next version from current .version ──────
# Sets NEXT_BASE, NEXT_PRE, NEXT_NUM, NEXT_VER
version_next() {
  version_read

  if [[ "$PRERELEASE" == "release" || -z "$PRERELEASE" ]]; then
    # Release mode: bump patch
    local x y z
    IFS='.' read -r x y z <<< "$BASE_VERSION"
    z=$((z + 1))
    NEXT_BASE="$x.$y.$z"
    NEXT_PRE="release"
    NEXT_NUM=0
  else
    # Prerelease mode: increment prerelease number
    NEXT_BASE="$BASE_VERSION"
    NEXT_PRE="$PRERELEASE"
    NEXT_NUM=$((PRE_NUM + 1))
  fi
  NEXT_VER=$(version_full "$NEXT_BASE" "$NEXT_PRE" "$NEXT_NUM")
}

# ── Cross-platform sed in-place ─────────────────────────────────────
# macOS BSD sed 要求 -i 带后缀参数（空后缀用 ''），Linux GNU sed 的 -i 不接受参数。
# 用数组封装差异，调用方写 sed "${SED_INPLACE[@]}" 's/.../.../' file。
if [[ "$(uname)" == "Darwin" ]]; then
  SED_INPLACE=(-i '')
else
  SED_INPLACE=(-i)
fi

# ── Update Go cmd/version.go ────────────────────────────────────────
version_update_go() {
  local ver="$1"
  local gofile="$ROOT_DIR/cmd/version.go"
  if [[ -f "$gofile" ]]; then
    sed "${SED_INPLACE[@]}" 's/^var version = ".*"/var version = "'"$ver"'"/' "$gofile"
  fi
}

# ── Update a single package.json's version field ────────────────────
_version_update_pkg() {
  local pkg="$1" ver="$2"
  if grep -q '"version"' "$pkg"; then
    sed "${SED_INPLACE[@]}" 's/"version": ".*"/"version": "'"$ver"'"/' "$pkg"
  else
    BUMP_PKG="$pkg" BUMP_VER="$ver" python3 -c '
import os, json
pkg = os.environ["BUMP_PKG"]
ver = os.environ["BUMP_VER"]
with open(pkg, "r") as f:
    data = json.load(f)
data["version"] = ver
with open(pkg, "w") as f:
    json.dump(data, f, indent=2, ensure_ascii=False)
    f.write("\n")
'
  fi
}

# ── Update all TS package.json files ────────────────────────────────
version_update_ts() {
  local ver="$1"
  local root_pkg="$ROOT_DIR/package.json"
  if [[ -f "$root_pkg" ]] && grep -q '"version"' "$root_pkg"; then
    sed "${SED_INPLACE[@]}" 's/"version": ".*"/"version": "'"$ver"'"/' "$root_pkg"
  fi
  for pkg in "$ROOT_DIR"/packages/*/package.json "$ROOT_DIR"/tui/package.json; do
    if [[ -f "$pkg" ]]; then
      _version_update_pkg "$pkg" "$ver"
    fi
  done
}

# ── Update all source files with new version ────────────────────────
version_update_all() {
  local ver="$1"
  version_update_go "$ver"
  version_update_ts "$ver"
}

# ── Stage version-related files in git ──────────────────────────────
version_stage() {
  git add "$VERSION_FILE" 2>/dev/null || true
  git add "$ROOT_DIR/cmd/version.go" 2>/dev/null || true
  git add "$ROOT_DIR/package.json" 2>/dev/null || true
  git add "$ROOT_DIR"/packages/*/package.json 2>/dev/null || true
  git add "$ROOT_DIR/tui/package.json" 2>/dev/null || true
}
