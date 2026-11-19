package imagegen

import "testing"

func TestParseProgress(t *testing.T) {
	pct, ok := ParseProgress("|========================================| 15/20 - 1.2s/it")
	if !ok {
		t.Fatal("expected progress match")
	}
	if pct != 0.75 {
		t.Fatalf("got %.2f, want 0.75", pct)
	}

	if _, ok := ParseProgress("loading model weights..."); ok {
		t.Fatal("did not expect progress match")
	}
}
