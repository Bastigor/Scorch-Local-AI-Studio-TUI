package imagegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSDArgsAndMetadataGallery(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "img_01.png")

	params := GenParams{
		ModelPath:      "/models/sd_turbo.safetensors",
		Prompt:         "cyberpunk street in rain",
		NegativePrompt: "blurry",
		Steps:          8,
		CFGScale:       2.5,
		Seed:           42,
		Width:          512,
		Height:         512,
		Sampler:        "euler",
		BatchCount:     1,
	}

	args := BuildSDArgs(params, outPath)
	if len(args) == 0 || args[len(args)-1] != outPath {
		t.Fatalf("unexpected args: %+v", args)
	}

	if err := os.WriteFile(outPath, []byte("png-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	metaPath, err := SaveMetadata(outPath, params)
	if err != nil {
		t.Fatalf("SaveMetadata error: %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("expected metadata file at %s: %v", metaPath, err)
	}

	gallery, err := ListGallery(dir)
	if err != nil {
		t.Fatalf("ListGallery error: %v", err)
	}
	if len(gallery) != 1 || gallery[0].Params.Seed != 42 {
		t.Fatalf("unexpected gallery entries: %+v", gallery)
	}
}
