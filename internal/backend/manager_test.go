package backend

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"local-ai-studio-tui/internal/hardware"
)

func writeFakeServer(t *testing.T, dir, script string) {
	t.Helper()
	binDir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	if err := os.WriteFile(filepath.Join(binDir, "llama-server"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStartLlamaServerReportsEarlyExitWithLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script sh indisponível no Windows")
	}
	dir := t.TempDir()
	writeFakeServer(t, dir, "#!/bin/sh\necho 'erro: modelo invalido' >&2\nexit 3\n")
	model := filepath.Join(dir, "m.gguf")
	_ = os.WriteFile(model, []byte("x"), 0o644)

	mgr := NewManager(dir, hardware.Info{SelectedBackend: hardware.BackendCPU, FallbackBackends: []hardware.BackendType{hardware.BackendCPU}})
	start := time.Now()
	_, err := mgr.StartLlamaServer(context.Background(), model, "", nil)
	if err == nil {
		t.Fatal("esperava erro")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("deveria falhar rápido quando o processo encerra, levou %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "modelo invalido") {
		t.Fatalf("erro deveria incluir os logs do servidor: %v", err)
	}
}

func TestStartLlamaServerBecomesHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script sh indisponível no Windows")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 indisponível")
	}
	dir := t.TempDir()
	// Lê --port dos argumentos e responde 503 por 1s e depois 200 em /health.
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  if [ "$1" = "--port" ]; then PORT="$2"; fi
  shift
done
exec python3 - "$PORT" <<'PY'
import sys, time, http.server
t0 = time.time()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        code = 200 if time.time() - t0 > 1 else 503
        self.send_response(code); self.end_headers(); self.wfile.write(b"{}")
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
`
	writeFakeServer(t, dir, script)
	model := filepath.Join(dir, "m.gguf")
	_ = os.WriteFile(model, []byte("x"), 0o644)

	mgr := NewManager(dir, hardware.Info{SelectedBackend: hardware.BackendCPU, FallbackBackends: []hardware.BackendType{hardware.BackendCPU}})
	defer mgr.Shutdown()

	var statuses []string
	ep, err := mgr.StartLlamaServer(context.Background(), model, "", func(s string) { statuses = append(statuses, s) })
	if err != nil {
		t.Fatalf("esperava sucesso: %v", err)
	}
	if !strings.HasPrefix(ep, "http://127.0.0.1:") {
		t.Fatalf("endpoint inesperado: %s", ep)
	}
	if len(statuses) == 0 {
		t.Fatal("esperava mensagens de status")
	}
	// Segunda chamada reaproveita o processo.
	ep2, err := mgr.StartLlamaServer(context.Background(), model, "", nil)
	if err != nil || ep2 != ep {
		t.Fatalf("esperava reaproveitar o servidor (ep=%s ep2=%s err=%v)", ep, ep2, err)
	}
}

func TestStartLlamaServerMissingBinary(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, hardware.Info{SelectedBackend: hardware.BackendCPU})
	_, err := mgr.StartLlamaServer(context.Background(), filepath.Join(dir, "m.gguf"), "", nil)
	if err == nil || !strings.Contains(err.Error(), "llama-server") {
		t.Fatalf("esperava erro sobre binário ausente, obteve %v", err)
	}
}
