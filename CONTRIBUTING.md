# Contributing

1. Pick one small roadmap item (`docs/04-roadmap.md`).
2. Keep `internal/core` stdlib-only (SDK-001). Put integrations under `internal/runtime/<name>/`.
3. Every behavior change needs a guardrail ID + test.
4. No personal / internal / secret data in commits (PRIV-001). Check `git diff` before push.
5. Verify locally:
   ```bash
   gofmt -l ./... && go vet ./... && go test ./... && go build ./...
   ```
