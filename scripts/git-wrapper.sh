#!/usr/bin/env bash
# git-wrapper.sh — intercepts `git commit --amend` to skip version bump
#
# Usage: add to your shell profile:
#   alias git='/path/to/scripts/git-wrapper.sh'
#
# Or use the Makefile target:
#   make install-git-alias

if [[ "$1" == "commit" ]] && echo "$@" | grep -q '\-\-amend'; then
  # Signal pre-commit hook to skip bump
  touch "$(git rev-parse --show-toplevel)/.git/.bump-skip"
fi

exec git "$@"
