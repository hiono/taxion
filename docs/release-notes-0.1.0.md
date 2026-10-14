# Taxion 0.1.0 (draft)

> Draft only. Do not publish until M15 Issue #79 and release Issue #78 meet
> their acceptance conditions.

## Scope

Taxion 0.1.0 is a CLI-first tool for Human-approved coding-agent work. It
records Task and Dispatch contracts, runtime results, validation evidence, and
independent review in a local workspace.

Tasks can opt into `worktree-tree-snapshot` output to review uncommitted work.
The controller records a tree SHA, retains it under local
`refs/taxion/snapshots/`, and reproduces the same tree in an independent review
worktree. The default remains commit-based output.

## Known limits

- Taxion does not provide `ready` or `doctor` commands. Configuration checks
  and runtime authentication checks report only their documented scope.
- AI-credit limits are soft ceilings. A running model turn may exceed the
  configured value before Taxion can observe usage and stop later work.
- Cold-start guide examples and snapshot continuation are covered by local
  CLI/controller tests with real Git and no paid model calls. These tests do
  not establish signed-in SDK end-to-end acceptance or repair missing evidence
  in legacy Dispatches.
- `runtime stop` now uses only its saved session binding and transport options,
  without requiring original prompts or tools. It rejects execution operations
  and disables pending-work continuation. Suspension and cancellation receipts
  are retained separately. `workflow run|step` checks all remaining possible
  Reviewer configuration approvals before creating a session.
- This release does not promise GitLab merge-request support, Bitbucket
  integration, a Web UI, multi-user administration, or unattended operation.

## Install

Requires Go 1.26.0. After the `v0.1.0` tag is published, install the command
with mise's [Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) and
[`mise use`](https://mise.jdx.dev/cli/use.html):

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@v0.1.0
mise exec -- taxion --help
```

## Update

Replace `v0.1.0` with the desired published version:

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@<VERSION>
```

## Remove

Use [`mise unuse`](https://mise.jdx.dev/cli/unuse.html) to remove the Go tool
from the global mise configuration. mise prunes the
installation when no other configuration still requests it:

```sh
mise unuse -g go:github.com/hiono/taxion/cmd/taxion
```

If a specific installation remains and no other project uses it, remove that
version explicitly with [`mise uninstall`](https://mise.jdx.dev/cli/uninstall.html):

```sh
mise uninstall go:github.com/hiono/taxion/cmd/taxion@v0.1.0
```
