package imagegen

import "time"

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

func ListGallery(outputsDir string) ([]GalleryEntry, error) {
	return nil, nil
}
