# Security Policy

## Supported versions

M5 pre-release: best-effort fixes on `main` only.

## Reporting a vulnerability

Open a **private security advisory** on GitHub (preferred) rather than a public issue.
Do not include PoC data containing personal information, credentials, or internal hosts.

## Controls (M5)

- `internal/core` has zero network / SDK / provider dependencies (SDK-001).
- Bearer-token gate on every `serve` route (`--token` / `TAXION_TOKEN`,
  constant-time compare, identical 401). Untokened binding beyond loopback
  is refused at startup. Tokens travel server-side only; `gen-token` mints
  32-byte crypto-random hex (ADR-0015).
- Append-only audit log of every mutation attempt and denial
  (`docs/07-audit-log.md`); no tokens, bodies, or queries. Taxion never
  deletes — the operator rotates.
- Plain HTTP is accepted on trusted networks only (loopback, SSH tunnel,
  VPN, docker bridge). Public-internet exposure without TLS termination in
  front is out of scope.
- No secrets in repo; CI runs hygiene (`grep` for `gho_`, `github_pat_`,
  private hosts). History scanned clean before public release (M5-3).
- `go vet` + `gofmt -l` + `go test ./...` required on every PR.
- Reviewer must bind to exact Coder output SHA (SHA-001).

## Known residual risks

- Single shared token (rotation is revocation; no per-user identity yet).
- No TLS in Taxion itself — terminate in front for anything beyond trusted nets.
- Audit log grows until the operator rotates it.
