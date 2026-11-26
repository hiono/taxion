#!/usr/bin/env bash
set -euo pipefail
repo_root="$(git -C "$(dirname "${BASH_SOURCE[0]}")/.." rev-parse --show-toplevel)"
configured_hooks_path="$(git -C "$repo_root" config --local --get core.hooksPath || true)"
if [[ -n "$configured_hooks_path" && "$configured_hooks_path" != ".githooks" ]]; then
  printf 'A different local core.hooksPath is already configured: %s\n' "$configured_hooks_path" >&2
  printf 'Resolve the existing hook configuration before installing Taxion hooks.\n' >&2
  exit 1
fi
if [[ ! -x "$repo_root/.githooks/pre-push" ]]; then
  printf 'The pre-push hook is missing or not executable.\n' >&2
  exit 1
fi
if [[ ! -x "$repo_root/scripts/check-gofmt.sh" ]]; then
  printf 'The shared gofmt checker is missing or not executable.\n' >&2
  exit 1
fi
git -C "$repo_root" config --local core.hooksPath .githooks
printf 'Installed Taxion Git hooks for this clone. Pushes now run the gofmt check.\n'
