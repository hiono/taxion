package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/forge"
)

// CheckRepositoryAccess makes read-only API queries, never an SDK model call.
func (a *Adapter) CheckRepositoryAccess(ctx context.Context) error {
	var selected config.Config
	if len(a.config.Configuration) == 0 || json.Unmarshal(a.config.Configuration, &selected) != nil {
		return fmt.Errorf("taxion: repository access requires the selected execution configuration")
	}
	return forge.CheckRepositoryAccess(ctx, selected, a.config.Role == "CODER")
}

// CheckAuthentication verifies the effective GitHub.com account selected by
// the SDK client. It creates no session and does not establish contract eligibility.
func (a *Adapter) CheckAuthentication(ctx context.Context, expectedAccount string) error {
	if strings.TrimSpace(expectedAccount) == "" || strings.TrimSpace(expectedAccount) != expectedAccount {
		return fmt.Errorf("taxion: authentication requires a nonempty account without surrounding whitespace")
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.ensureStarted(ctx); err != nil {
		if err == ErrClosedAdapter {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("taxion: authentication client could not start; provider details withheld")
	}
	status, err := a.client.GetAuthStatus(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("taxion: authentication status unavailable; provider details withheld")
	}
	return checkAuthenticationStatus(status, expectedAccount)
}

func checkAuthenticationStatus(status *sdk.GetAuthStatusResponse, expectedAccount string) error {
	if status == nil || !status.IsAuthenticated {
		return fmt.Errorf("taxion: runtime authentication is unconfirmed")
	}
	if status.Login == nil || !strings.EqualFold(*status.Login, expectedAccount) {
		return fmt.Errorf("taxion: effective runtime account does not match the expected account")
	}
	if status.Host == nil || !githubAuthenticationHost(*status.Host) {
		return fmt.Errorf("taxion: effective runtime authentication host is not confirmed as github.com")
	}

	return nil
}

func githubAuthenticationHost(host string) bool {
	return strings.EqualFold(host, "github.com") ||
		strings.EqualFold(host, "https://github.com") ||
		strings.EqualFold(host, "https://github.com/")
}
