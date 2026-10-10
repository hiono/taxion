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

Writes (`put` and `warn`) default to `project`. `--scope global` must be
explicit and prints a notification; it does not ask for confirmation.
Reads (`list`, `search`, and `export`) include both scopes unless filtered
with `--scope`. There are no separate project and global storage
directories in the current CLI.

## Warning promotion

`memory warn` counts identical `(body, scope)` pairs. On the third occurrence,
it creates one lesson with status `proposed` as a guideline candidate. The
threshold is fixed at `PromoteAfter = 3`; it is not a CLI setting. Promotion
to a guardrail and dispatch-time enforcement of Memory entries are not
implemented. Existing core checks such as DSP-001 remain separate.

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
