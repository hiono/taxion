package copilot

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
)

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
	if status.Host == nil || !strings.EqualFold(*status.Host, "github.com") {
		return fmt.Errorf("taxion: effective runtime authentication host is not confirmed as github.com")
	}
	return nil
}
