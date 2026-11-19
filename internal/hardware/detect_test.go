package hardware

import "testing"

func TestEvaluateHardwareSelection(t *testing.T) {
	cases := []struct {
		name        string
		env         ProbeEnv
		wantBackend BackendType
		wantSupport bool
	}{
		{
			name: "Windows NVIDIA selects CUDA",
			env: ProbeEnv{
				GOOS:          "windows",
				GOARCH:        "amd64",
				HasNvidiaSMI:  true,
				NvidiaGPUName: "RTX 4070",
				NvidiaVRAMMB:  12288,
			},
			wantBackend: BackendCUDA,
			wantSupport: true,
		},
		{
			name: "Linux dual GPU prefers discrete NVIDIA over Intel",
			env: ProbeEnv{
				GOOS:          "linux",
				GOARCH:        "amd64",
				HasNvidiaSMI:  true,
				NvidiaGPUName: "RTX 3060",
				HasIntelGPU:   true,
				HasLibGomp:    true,
			},
			wantBackend: BackendCUDA,
			wantSupport: true,
		},
		{
			name: "macOS Apple Silicon selects Metal",
			env: ProbeEnv{
				GOOS:   "darwin",
				GOARCH: "arm64",
			},
			wantBackend: BackendMetal,
			wantSupport: true,
		},
		{
			name: "macOS Intel is rejected cleanly",
			env: ProbeEnv{
				GOOS:   "darwin",
				GOARCH: "amd64",
			},
			wantBackend: BackendCPU,
			wantSupport: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.env)
			if got.SelectedBackend != tc.wantBackend {
				t.Fatalf("got backend %s, want %s", got.SelectedBackend, tc.wantBackend)
			}
			if got.Supported != tc.wantSupport {
				t.Fatalf("got supported %v, want %v", got.Supported, tc.wantSupport)
			}
		})
	}
}
