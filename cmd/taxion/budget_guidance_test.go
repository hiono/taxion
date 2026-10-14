package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestNewProposalBudgetGuidanceUsesConfigWithoutRequiringIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for variable := range map[string]bool{
		"TAXION_EXECUTION_BUDGET_GUIDANCE": true, "TAXION_PROJECT_ID": true,
		"TAXION_RUNTIME_ACCOUNT": true,
	} {
		t.Setenv(variable, "")
		if err := os.Unsetenv(variable); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := newTaskBudgetGuidance()
	if err != nil || policy.Mode != "flow" || policy.Source != "default" {
		t.Fatal("unconfigured local proposal did not default to flow", policy, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".taxion"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".taxion", "config.yaml"),
		[]byte("version: 1\nexecution:\n  budgetGuidance: account\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err = newTaskBudgetGuidance()
	if err != nil || policy.Mode != "account" || !policy.AccountReadApproved {
		t.Fatal("config account selection missing from proposal", policy, err)
	}
	t.Setenv("TAXION_EXECUTION_BUDGET_GUIDANCE", "off")
	policy, err = newTaskBudgetGuidance()
	if err != nil || policy.Mode != "off" || policy.AccountReadApproved {
		t.Fatal("environment override granted the wrong read scope", policy, err)
	}
	layers, err := config.ReadFiles(filepath.Join(dir, "empty"), filepath.Join(dir, "missing"))
	if err == nil || layers != nil {
		t.Fatal("proposal loading weakened required runtime configuration")
	}
}

func TestRequestGuidanceOverrideReachesPlannerWithoutExtraTurn(t *testing.T) {
	for _, mode := range []string{"off", "flow", "account"} {
		t.Run(mode, func(t *testing.T) {
			_, args := requestFixture(t)
			calls := 0
			planner := func(ctx context.Context, db *store.Store, selected config.Config, task core.GraphTask,
				text string, tokens int64, credits float64, cli, base string) (controller.QuestionResult, error) {
				calls++
				if task.BudgetGuidance == nil || task.BudgetGuidance.Mode != mode || task.BudgetGuidance.Source != "request" {
					t.Fatal("explicit request guidance did not reach the approved planning Task")
				}
				result, err := fixturePlan(t, requestFixtureAnswer([]string{}))(ctx, db, selected, task, text, tokens, credits, cli, base)
				if err == nil {
					if mode == "off" && result.BudgetSupplement != "" ||
						mode != "off" && !strings.Contains(result.BudgetSupplement, "Taxion budget observation") ||
						mode == "account" && !strings.Contains(result.BudgetSupplement, `"state":"unsupported"`) {
						t.Fatal("Planner received wrong or hidden guidance", result.BudgetSupplement)
					}
				}
				return result, err
			}
			var out bytes.Buffer
			if err := requestCommand(append(args, "--budget-guidance", mode, "変更を相談したい"),
				strings.NewReader("/plan\n/quit\n"), &out, planner,
				func(context.Context, []string, io.Writer) error {
					t.Fatal("quit started implementation")
					return nil
				}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("guidance added a separate model turn", calls)
			}
		})
	}
}
