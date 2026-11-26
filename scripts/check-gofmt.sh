#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v gofmt >/dev/null 2>&1; then
  printf 'gofmt is required for the local formatting check. Install Go and add gofmt to PATH.\n' >&2
  exit 1
fi

gofmt_bin="$(command -v gofmt)"
unformatted="$("$gofmt_bin" -l cmd internal)"
if [[ -z "$unformatted" ]]; then
  printf 'gofmt (%s): all Go files are formatted.\n' "$gofmt_bin"
  exit 0
fi

printf 'The following Go files require gofmt:\n%s\n' "$unformatted" >&2
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  "$gofmt_bin" -d "$file" >&2
done <<< "$unformatted"
exit 1
