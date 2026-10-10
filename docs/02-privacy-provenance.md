# 02 — Privacy & Provenance Policy

## Rule

`main` must always satisfy PRIV-001. No exceptions.

## Forbidden in public artifacts

- Personal names, emails, user IDs
- Home directory paths (`/home/<user>`, `C:\Users\…`)
- Internal hostnames, intranet URLs
- Organization-specific repository / project names
- Private predecessor / PoC project names (never mention in public artifacts)
- Tokens, keys, credentials (`gho_`, `github_pat_`, `glpat-`, `.pem`)

## Sanitization procedure (for any imported knowledge)

1. Extract requirement / guardrail / procedure / failure pattern.
2. Strip actors, hosts, paths, repo names → replace with generic roles
   (`coder`, `reviewer`, `implementation-repo`, `validation-target`).
3. Drop timestamps, internal URLs, screenshots with PII.
4. Second-person review before merge.

## CI

- `hygiene` scans Go source and selected configuration files for full token
  patterns and private-key headers. Markdown and the workflow itself are
  excluded. It does not check personal names, internal hosts, or arbitrary
  sensitive data; those require review. A match fails the job.
