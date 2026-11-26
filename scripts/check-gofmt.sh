#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v go >/dev/null 2>&1 || ! command -v gofmt >/dev/null 2>&1; then
  printf 'Go and gofmt are required. Install the Go toolchain declared in go.mod before pushing.\n' >&2
  exit 1
fi

required_go_version="$(awk '$1 == "go" { print $2; exit }' go.mod)"
actual_go_version="$(GOTOOLCHAIN=local go env GOVERSION)"
required_go_series="\${required_go_version%.*}"
actual_go_series="\${actual_go_version#go}"
actual_go_series="\${actual_go_series%.*}"
if [[ -z "$required_go_version" || "$actual_go_series" != "$required_go_series" ]]; then
  printf 'Go version mismatch: go.mod requires Go %s, but the active toolchain is %s.\\n' "$required_go_series" "$actual_go_version" >&2
  exit 1
fi

unformatted="$(gofmt -l cmd internal)"
if [[ -z "$unformatted" ]]; then
  printf 'gofmt: all Go files are formatted.\\n'
  exit 0
fi

printf 'The following Go files require gofmt:\\n%s\\n' "$unformatted" >&2
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  gofmt -d "$file" >&2
done <<< "$unformatted"
exit 1
