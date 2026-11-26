#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v go >/dev/null 2>&1; then
  printf 'The Go command is required to select the go.mod toolchain. Install Go or add it to PATH.\n' >&2
  exit 1
fi

required_go_version="$(awk '$1 == "go" { print $2; exit }' go.mod)"
if [[ -z "$required_go_version" ]]; then
  printf 'Could not determine the required Go version from go.mod.\n' >&2
  exit 1
fi

toolchain="go${required_go_version}+auto"
if ! toolchain_goroot="$(GOTOOLCHAIN="$toolchain" go env GOROOT 2>/dev/null)"; then
  printf 'Could not obtain the Go %s toolchain required for formatting. Check network access or install that toolchain.\n' "$required_go_version" >&2
  exit 1
fi

gofmt="${toolchain_goroot}/bin/gofmt"
if [[ ! -x "$gofmt" ]]; then
  printf 'The Go %s toolchain does not provide an executable gofmt at %s.\n' "$required_go_version" "$gofmt" >&2
  exit 1
fi

unformatted="$("$gofmt" -l cmd internal)"
if [[ -z "$unformatted" ]]; then
  printf 'gofmt (%s): all Go files are formatted.\n' "$required_go_version"
  exit 0
fi

printf 'The following Go files require gofmt (%s):\n%s\n' "$required_go_version" "$unformatted" >&2
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  "$gofmt" -d "$file" >&2
done <<< "$unformatted"
exit 1
