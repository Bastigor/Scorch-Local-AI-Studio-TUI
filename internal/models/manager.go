package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateModelFile(t *testing.T) {
	cases := []struct {
		file    string
		wantErr bool
		kind    ModelKind
	}{
		{"qwen2.5-coder-1.5b.gguf", false, KindLLM},
		{"sd_turbo.safetensors", false, KindCheckpoint},
		{"my_lora_v1.safetensors", true, ""},
		{"sdxl_vae.safetensors", true, ""},
		{"controlnet_canny.safetensors", true, ""},
		{"clip_l.safetensors", true, ""},
		{"mmproj-model-f16.gguf", true, ""},
		{"notas.txt", true, ""},
	}

	for _, tc := range cases {
		kind, err := ValidateModelFile(tc.file)
		if tc.wantErr && err == nil {
			t.Errorf("expected error for %s, got nil", tc.file)
		}
		if !tc.wantErr {
			if err != nil {
				t.Errorf("unexpected error for %s: %v", tc.file, err)
			}
			if kind != tc.kind {
				t.Errorf("got kind %s, want %s", kind, tc.kind)
			}
		}
	}
}

func TestSafeResolveUnderAndImport(t *testing.T) {
	base := t.TempDir()
	if _, err := SafeResolveUnder(base, "../../etc/passwd"); err == nil {
		t.Fatal("expected path traversal error")
	}

	srcDir := t.TempDir()
	srcModel := filepath.Join(srcDir, "test_model.gguf")
	if err := os.WriteFile(srcModel, []byte("gguf-dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(base)
	installed, err := mgr.ImportLocalModel(srcModel)
	if err != nil {
		t.Fatalf("unexpected import error: %v", err)
	}
	if installed.Kind != KindLLM || installed.Name != "test_model.gguf" {
		t.Fatalf("unexpected installed model: %+v", installed)
	}
}
