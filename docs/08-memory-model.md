# 08 — Memory reference

This document describes the current CLI and store. The proposed
log / lesson / decision / guardrail model, intended storage locations,
and promotion rationale are recorded in
[ADR-0017](adr/0017-memory-layers-ranges-and-promotion-path.md).
Implementation results are recorded in Issues #28–#34, #38, and #66.

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

### Local state and shared knowledge

The default ledger is `.taxion/ledger.json`; `memory.db` and `workspace.db`
are beside it. The whole `.taxion/` directory is local and ignored. A custom
`--store` selects a different local directory, not a shared knowledge source.
Keep its permissions and backups under the owner's control. Runtime session
files and private authentication or usage observations remain local as well.

Shared specifications belong in `docs/`, accepted architectural decisions in
`docs/adr/`, and explicitly reviewed reusable Memory records in
`docs/memory/`. These Markdown records are documentation, not automatically
loaded or approved runtime entries. Give each record a stable identifier,
revision, applicable scope, source references, and the decision or lesson it
preserves. Do not include credential values, local settings, private usage
details, or an unreviewed dump of the local database.

To share an existing entry, preserve the original local database and inspect
that entry's text, sources, version and approval separately. `memory export
--scope project` can provide a sanitized preview; it is not a database migration
or an executable-policy export. Select and review the intended knowledge,
then record it outside `.taxion/` with provenance to the original record.
Sanitization may change meaning and does not guarantee removal of private data.
Review the proposed file and commit it through the normal scope and secret
checks; do not move or force-add the local database.

To reuse a shared record in another local store, use `memory put` with the
appropriate kind, text and scope, and a `--source` reference identifying the
shared repository revision, path, record identifier and revision. The imported
text is proposed knowledge; file publication and old local approval do not
grant execution permission in the new store. For a guardrail, reconstruct its
structured predicates and obtain explicit local approval before enforcing it.
Retain a mapping to the old local ID/version when replacing a reference in a
new Task. Do not rewrite issued Dispatch snapshots or delete the old database
while saved Tasks or Dispatches still depend on its records.

There is no automatic shared-file importer, cross-store ID migration or approval
transfer. `memory export` omits structured guardrail predicates and thresholds;
the steps above deliberately preserve local history and require review rather
than claiming a lossless export/import round trip.

## Sources and lifecycle

Entries retain source references, a version, the replaced entry ID, and
creation/update times. A source reference identifies a record or external
evidence; it does not copy or independently verify that evidence. Repeat
`--source` to record more than one source:

```sh
taxion memory put --kind decision --title "Use SQLite" \
  --source task:T-1 --source dispatch:D-1
taxion memory warn --body "Verify the target before dispatch" --source dispatch:D-1
```

Each warning occurrence is recorded. A promoted candidate refers to those
occurrences and their supplied source IDs. Resolve a `warning:<id>` reference
with `memory source --warning-id ID`. Older databases kept only an aggregate
warning count; migration records it as `legacy:aggregate-count-only` rather
than fabricating individual evidence.

`memory revise --kind KIND --id ID --title TITLE --actor ACTOR --reason REASON`
creates a new proposed version and marks the previous version superseded.
Omitting `--source` preserves the previous source list. `memory revoke` takes
the same kind, ID, actor, and reason and disables an entry without deleting
its content. Revising or revoking an approved candidate lesson also revokes
the guardrail created from it. The new lesson must be approved again before
its guardrail is enforced. `memory history --kind KIND --id ID` prints
lifecycle events. Search omits superseded and revoked entries; `get` and
`list` retain them for audit.

The Task Graph owns the current list of Memory references. Set `memoryRefs`
to canonical IDs such as `lesson:12` or `decision:4`. CLI, HTTP, and ACP
dispatch resolve each reference and reject missing, revoked, superseded, or
non-approved guardrail entries. Each referenced entry must include source
references. The Task Graph stores IDs; the immutable Dispatch instruction
snapshot stores each selected entry's version, scope,
status, title, body, and source references. Approved guardrails also include
their structured predicate and source lesson ID. A worker therefore receives the exact Memory
version selected when the Dispatch was issued.

Record contradictions between entries explicitly; Taxion does not infer
semantic conflicts from free text. An unresolved conflict blocks dispatch
when either entry is referenced:

```sh
taxion memory conflict add --left decision:4 --right lesson:12 \
  --actor human --reason "The entries prescribe different storage sources"
taxion memory conflict list --ref decision:4
taxion memory conflict resolve --id 1 --actor human \
  --reason "Decision 4 supersedes the lesson for this workspace"
```

Conflict creation and resolution are retained in both entries' lifecycle
histories. Resolving a conflict requires a recorded human explanation.

## Warning promotion

`memory warn` counts identical `(body, scope)` pairs. At the configured
threshold it creates one lesson with status `proposed` as a guideline
candidate. The default is three. Set a positive threshold for the selected
workspace with `memory configure --promote-after N`. It persists in
`memory.db` and affects subsequent warnings, including existing counters.
Changing it never removes an existing candidate. The counter, candidate,
and their link commit in one transaction. The Memory database also records
each new occurrence with its optional source reference.

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
DB. Revoking a guardrail or its approved source lesson disables the check;
revising a source lesson also revokes its prior check until a human approves
the new version. Enforcement reads Memory separately from the SQLite
workspace transaction, so changes across the two databases are not atomic.
It does not constrain shell commands, worker output, or future Task edits.

## Search

`memory search --query TEXT [--kind KIND] [--scope SCOPE]` searches entry
titles and bodies. Queries of three or more characters use FTS5 trigram
with BM25 ranking. Shorter queries and FTS queries with no matches fall
back to LIKE. The database index is updated by SQLite insert/delete
triggers; callers do not update it separately. Superseded and revoked entries
remain stored for audit but do not appear in search results.

## Export

`memory export [--scope SCOPE] [--sanitize=false]` prints a preview to stdout only.
Sanitizing defaults on. It does not transmit data, and there are no
`--profile` or `--dry-run` flags.

The default sanitizer masks matching private-key blocks, token patterns,
email addresses, IPv4-shaped text, and home-directory paths in titles,
bodies, and source references. Rules are shared across both scopes. Unmatched
text and other entry fields pass through unchanged; sanitizing is not a guarantee that
all sensitive data is removed. Check the preview before sharing it.
`--sanitize=false` displays the original text with an UNSANITIZED header.
Export contains entry text, not the structured predicates or threshold;
use `memory checks` to inspect active policy.
