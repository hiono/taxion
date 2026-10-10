# Contributing

1. Pick one small roadmap item (`docs/04-roadmap.md`).
2. Keep `internal/core` stdlib-only (SDK-001). Put integrations under `internal/runtime/<name>/`.
3. Every behavior change needs a guardrail ID + test.
4. No personal / internal / secret data in commits (PRIV-001). Check `git diff` before push.
5. Verify locally:
   ```bash
   test -z "$(gofmt -l cmd internal)" && go vet ./... && go test ./... && go build ./...
   ```

## Technical descriptions

Use Japanese prose for Issue bodies and Milestone descriptions. Keep
established technical terms, feature names, and identifiers in their
existing form: Memory, sanitizer, rate limiting, lockout, and audit
hash-chain do not need invented translations. Use the actual command
names and flags, and describe what the operation does rather than
substituting a metaphor or an approximate term.

Distinguish proposals, recorded decisions, and current implementation.
Preserve historical requirements and results; add a clarification when
they could be mistaken for current behavior. Check related Issues,
documentation, code comments, and displayed text before marking wording
maintenance complete.
