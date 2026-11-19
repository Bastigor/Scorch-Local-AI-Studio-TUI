package llm

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ImagePath string `json:"image_path,omitempty"`
}

type ChatParams struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	MaxTokens   int     `json:"max_tokens"`
}

type StreamChunk struct {
	Delta string
	Done  bool
	Err   error
}
