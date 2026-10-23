package publictext

import (
	"os"
	"strings"
	"testing"
)

func TestTaxionRequiresPlainStatementTitles(t *testing.T) {
	data, err := os.ReadFile("../../.github/communication-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := DecodeProfiles(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	profile := profiles.Projects[0]
	if profile.Repository != "hiono/taxion" || profile.TitleStyle != "plain-statement" {
		t.Fatal("Taxion title style must be explicitly configured")
	}
	for _, title := range []string{"fix: 設定を保持する", "docs(v0.7.0): 利用手順を説明する", "M12: 設定を保持する", "0.7.0: 設定を保持する"} {
		if err := CheckPresentation(profile, title, ""); err == nil {
			t.Errorf("Taxion accepted a classified title: %s", title)
		}
	}
	for _, title := range []string{"設定内容を保持する", "初回設定の最終確認で保存されない", "0.7.2 が承認情報を拒否する", "ApprovalBundle の照合を修正する"} {
		if err := CheckPresentation(profile, title, ""); err != nil {
			t.Errorf("Taxion rejected a reader-facing title: %s: %v", title, err)
		}
	}
}
