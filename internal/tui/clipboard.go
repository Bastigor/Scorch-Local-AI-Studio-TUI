package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

func CopyToClipboardOSC52(dataDir, text string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	_, _ = fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\a", b64)

	fallbackPath := filepath.Join(dataDir, "cache", "clipboard.txt")
	_ = os.WriteFile(fallbackPath, []byte(text), 0o644)
	return fallbackPath
}
