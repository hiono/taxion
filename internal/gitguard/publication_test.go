package gitguard

import (
	"context"
	"testing"
)

func TestPublicationScannerFailsClosed(t *testing.T) {
	for _, behavior := range []string{"clean", "leak", "error", "invalid"} {
		t.Run(behavior, func(t *testing.T) {
			err := CheckPublication(context.Background(), fakeScanner(t, behavior), "approved public report")
			if (behavior == "clean") != (err == nil) {
				t.Fatalf("%s: %v", behavior, err)
			}
		})
	}
	if err := CheckPublication(context.Background(), "missing-scanner", "report"); err == nil {
		t.Fatal("missing scanner accepted")
	}
}
