package hardware

import "testing"

func TestDetectAndProbeEnv(t *testing.T) {
	info := Detect()
	if info.OS == "" || info.Arch == "" {
		t.Fatalf("expected OS and Arch to be populated, got %+v", info)
	}
	if info.CPUCores <= 0 {
		t.Fatalf("expected positive CPUCores, got %d", info.CPUCores)
	}
	if !info.Supported {
		t.Fatalf("expected Supported=true, got false (%s)", info.UnsupportedReason)
	}

	probed := ProbeEnv(Info{
		OS:      "darwin",
		Arch:    "arm64",
		GPUName: "Apple M-series",
	})
	if probed.SelectedBackend != BackendMetal {
		t.Fatalf("expected BackendMetal on darwin/arm64, got %s", probed.SelectedBackend)
	}
}
