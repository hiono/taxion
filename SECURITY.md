# Security Policy

## Supported versions

Day2 pre-release: best-effort fixes on `main` only.

## Reporting a vulnerability

Open a **private security advisory** on GitHub (preferred) rather than a public issue.
Do not include PoC data containing personal information, credentials, or internal hosts.

## Baseline controls (Day2)

- `internal/core` has zero network / SDK / provider dependencies (SDK-001).
- No secrets in repo; CI runs `gitleaks`-friendly hygiene (`grep` for `gho_`, `github_pat_`, private hosts).
- `go vet` + `gofmt -l` + `go test ./...` required on every PR.
- Reviewer must bind to exact Coder output SHA (SHA-001).
