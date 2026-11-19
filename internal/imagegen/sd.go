package imagegen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

type GenParams struct {
	ModelPath      string  `json:"model_path"`
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt"`
	Steps          int     `json:"steps"`
	CFGScale       float64 `json:"cfg_scale"`
	Seed           int64   `json:"seed"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Sampler        string  `json:"sampler"`
	BatchCount     int     `json:"batch_count"`
}

type GalleryEntry struct {
	ImagePath string    `json:"image_path"`
	MetaPath  string    `json:"meta_path"`
	CreatedAt time.Time `json:"created_at"`
	Params    GenParams `json:"params"`
}

var progressRegex = regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)

func ParseProgress(line string) (float64, bool) {
	m := progressRegex.FindStringSubmatch(line)
	if len(m) == 3 {
		cur, err1 := strconv.Atoi(m[1])
		tot, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && tot > 0 && cur <= tot {
			return float64(cur) / float64(tot), true
		}
	}
	return 0, false
}

func BuildSDArgs(p GenParams, outputPath string) []string {
	return []string{
		"-m", p.ModelPath,
		"-p", p.Prompt,
		"-n", p.NegativePrompt,
		"--steps", fmt.Sprintf("%d", p.Steps),
		"--cfg-scale", fmt.Sprintf("%.2f", p.CFGScale),
		"-s", fmt.Sprintf("%d", p.Seed),
		"-W", fmt.Sprintf("%d", p.Width),
		"-H", fmt.Sprintf("%d", p.Height),
		"--sampling-method", p.Sampler,
		"-b", fmt.Sprintf("%d", p.BatchCount),
		"-o", outputPath,
	}
}

func SaveMetadata(imagePath string, p GenParams) (string, error) {
	metaPath := imagePath + ".json"
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return metaPath, os.WriteFile(metaPath, data, 0o644)
}

func ListGallery(outputsDir string) ([]GalleryEntry, error) {
	entries, err := os.ReadDir(outputsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []GalleryEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".png" {
			continue
		}
		imgPath := filepath.Join(outputsDir, e.Name())
		metaPath := imgPath + ".json"
		info, _ := e.Info()
		var created time.Time
		if info != nil {
			created = info.ModTime()
		}
		var params GenParams
		if raw, err := os.ReadFile(metaPath); err == nil {
			_ = json.Unmarshal(raw, &params)
		}
		result = append(result, GalleryEntry{
			ImagePath: imgPath,
			MetaPath:  metaPath,
			CreatedAt: created,
			Params:    params,
		})
	}
	return result, nil
}
