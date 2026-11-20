package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"local-ai-studio-tui/internal/backend"
	"local-ai-studio-tui/internal/hardware"
	"local-ai-studio-tui/internal/render"
	"local-ai-studio-tui/internal/store"
)

const fakeServerScript = `#!/bin/sh
while [ $# -gt 0 ]; do
  if [ "$1" = "--port" ]; then PORT="$2"; fi
  shift
done
exec python3 - "$PORT" <<'PY'
import sys, http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"{}")
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0)); self.rfile.read(n)
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream"); self.end_headers()
        for piece in ["Ol", "á", ", ", "mundo", "!"]:
            self.wfile.write(("data: " + json.dumps({"choices":[{"delta":{"content":piece}}]}) + "\n\n").encode())
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
`

// Reproduz o fluxo completo do chat: Enter -> servidor sobe -> resposta em vários
// trechos. Antes, o strings.Builder copiado por valor causava panic no 2º trecho.
func TestChatEndToEndWithFakeServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script sh indisponível no Windows")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 indisponível")
	}
	dir := t.TempDir()
	if err := store.EnsureLayout(dir); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "bin", "llama-server"), []byte(fakeServerScript), 0o755)
	modelPath := filepath.Join(dir, "models", "llm", "fake.gguf")
	_ = os.WriteFile(modelPath, []byte("x"), 0o644)

	hw := hardware.Info{OS: "linux", SelectedBackend: hardware.BackendCPU, FallbackBackends: []hardware.BackendType{hardware.BackendCPU}}
	bm := backend.NewManager(dir, hw)
	defer bm.Shutdown()

	m := NewAppModel(AppDeps{
		DataDir:    dir,
		Config:     store.DefaultConfig(),
		Hardware:   hw,
		TermCaps:   render.Capabilities{Cols: 100, Rows: 30, ColorDepth: "truecolor"},
		BackendMgr: bm,
		ConvStore:  store.NewConversationStore(filepath.Join(dir, "conversations")),
	})
	m.showSplash, m.showWizard = false, false
	m.selectedLLM = modelPath

	var model tea.Model = m
	model, cmd := model.Update(runes("Olá"))
	_ = cmd
	model, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	for i := 0; i < 200 && cmd != nil; i++ {
		msg := cmd()
		model, cmd = model.Update(msg)
		if am := model.(AppModel); !am.streaming && cmd == nil {
			break
		}
	}

	am := model.(AppModel)
	if am.errMsg != "" {
		t.Fatalf("erro inesperado: %s", am.errMsg)
	}
	msgs := am.activeConv.Messages
	if len(msgs) != 2 || msgs[1].Role != "assistant" || msgs[1].Content != "Olá, mundo!" {
		t.Fatalf("mensagens inesperadas: %+v", msgs)
	}
	if !strings.Contains(am.View(), "Conversa") {
		t.Fatal("view sem painel de conversa")
	}
}
