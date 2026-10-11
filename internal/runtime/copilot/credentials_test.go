package copilot

import (
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
)

func TestExplicitCredentialSelectionDoesNotFallBackOrExposeValues(t *testing.T) {
	for _, key := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(key, "ambient-private-fixture")
	}
	t.Setenv("TAXION_TEST_CREDENTIAL", "selected-private-fixture")
	for _, ref := range []string{"copilot:logged-in", "env:TAXION_TEST_CREDENTIAL"} {
		options := &sdk.ClientOptions{}
		if err := configureCredential(options, ref); err != nil {
			t.Fatal(err)
		}
		loggedIn := ref == "copilot:logged-in"
		if options.UseLoggedInUser == nil || *options.UseLoggedInUser != loggedIn ||
			(options.GitHubToken == "") != loggedIn {
			t.Fatal("credential policy did not select explicit mode")
		}
		for _, item := range options.Env {
			key, _, _ := strings.Cut(item, "=")
			if key == "COPILOT_GITHUB_TOKEN" || key == "GH_TOKEN" || key == "GITHUB_TOKEN" ||
				!loggedIn && key == "TAXION_TEST_CREDENTIAL" {
				t.Fatal("ambient or named credential remained in child environment")
			}
		}
	}
	for _, ref := range []string{"env:", "env:lowercase", "env:FOO-BAR", "env:TAXION_UNSET_TEST"} {
		options := &sdk.ClientOptions{}
		if err := configureCredential(options, ref); err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("invalid reference accepted or secret exposed")
		}
	}
	for _, value := range []string{"", " bad", "bad\nvalue"} {
		t.Setenv("TAXION_TEST_CREDENTIAL", value)
		if err := configureCredential(&sdk.ClientOptions{}, "env:TAXION_TEST_CREDENTIAL"); err == nil {
			t.Fatal("invalid credential value accepted")
		}
	}
	options := &sdk.ClientOptions{}
	if err := configureCredential(options, "legacy-reference"); err != nil ||
		options.Env != nil || options.UseLoggedInUser != nil {
		t.Fatal("legacy SDK selection changed")
	}
}
