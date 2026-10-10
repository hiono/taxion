# 08 — Memory reference

This document describes the current CLI and store. The proposed
log / lesson / decision / guardrail model, intended storage locations,
and promotion rationale are recorded in
[ADR-0017](adr/0017-memory-layers-ranges-and-promotion-path.md).
Implementation results are recorded in Issues #28–#34 and #38.

## Storage and scopes

`memory put`, `get`, and `list` operate on four kinds: `lesson`, `decision`,
`guardrail`, and `concern`. New entries default to status `proposed`.
Storing an entry with kind `guardrail` does not make it an enforced check.

The CLI stores both `project` and `global` entries in `memory.db` next to
the selected ledger, distinguished by the `scope` column. Use the global
`--store PATH` option before the command to select the ledger and hence
the Memory database directory.

Writes (`put`, `warn`, and `approve`) default to `project`. `--scope global` must be
explicit and prints a notification; it does not ask for confirmation.
Reads (`list`, `search`, and `export`) include both scopes unless filtered
with `--scope`. There are no separate project and global storage
directories in the current CLI.

## Warning promotion

`memory warn` counts identical `(body, scope)` pairs. At the configured
threshold it creates one lesson with status `proposed` as a guideline
candidate. The default is three. Set a positive threshold for the selected
workspace with `memory configure --promote-after N`. It persists in
`memory.db` and affects subsequent warnings, including existing counters.
Changing it never removes an existing candidate. The counter, candidate,
and their link commit in one transaction.

## Approval and dispatch checks

Approve a candidate explicitly; free text is not parsed as policy:

```sh
taxion memory approve --id 1 --check repo-equals --value target-repo
taxion memory approve --id 2 --check task-title-matches --value '^fix:'
taxion memory checks
```

`--id` is the candidate lesson ID returned by `warn`, not a guardrail ID.
Global candidates require `--scope global`. Approval creates an `approved`
guardrail and records its predicate in the same transaction as the lesson
status change. Repeating the identical approval returns the same guardrail;
a conflicting approval fails. Ordinary lessons and `memory put --kind
guardrail` do not activate policy.

`repo-equals` compares the requested dispatch repo string exactly with
`--value`. It does not resolve paths or verify that a repository exists.
`task-title-matches` uses a Go regular expression against the stored task
title, not a title supplied with the dispatch. Matching is unanchored unless
the expression includes `^` and `$`. Empty values, invalid regular
expressions, and unknown predicates are rejected at approval.

CLI, HTTP API, and ACP (including the deprecated prompt form) evaluate all
approved predicates in both scopes before creating a dispatch. All must
match (MEM-001). Web forwards the API verdict without implementing a second
policy evaluator. A policy mismatch returns HTTP 409; unavailable or
malformed policy returns 500. Neither writes a dispatch. Existing workspaces
without `memory.db` retain their behavior; an existing unreadable database
does not silently disable checks.

Project/global identify entries within the selected workspace DB, not
separate repo databases. Approved checks apply to every dispatch using that
DB. The initial implementation has no policy-edit/revoke command; review
the value and scope before approving. Enforcement checks a snapshot before
the JSON ledger save, not an atomic Memory-plus-ledger transaction. It does
not constrain shell commands, worker output, or future task edits.

## Search

`memory search --query TEXT [--kind KIND] [--scope SCOPE]` searches entry
titles and bodies. Queries of three or more characters use FTS5 trigram
with BM25 ranking. Shorter queries and FTS queries with no matches fall
back to LIKE. The database index is updated by SQLite insert/delete
triggers; callers do not update it separately.

## Export

`memory export [--scope SCOPE] [--sanitize=false]` prints a preview to stdout only.
Sanitizing defaults on. It does not transmit data, and there are no
`--profile` or `--dry-run` flags.

The default sanitizer masks matching private-key blocks, token patterns,
email addresses, IPv4-shaped text, and home-directory paths in titles and
bodies. Rules are shared across both scopes. Unmatched text and other
entry fields pass through unchanged; sanitizing is not a guarantee that
all sensitive data is removed. Check the preview before sharing it.
`--sanitize=false` displays the original text with an UNSANITIZED header.
Export contains entry text, not the structured predicates or threshold;
use `memory checks` to inspect active policy.
