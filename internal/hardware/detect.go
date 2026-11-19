package hardware

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type BackendType string

const (
	BackendCUDA   BackendType = "cuda"
	BackendROCm   BackendType = "rocm"
	BackendVulkan BackendType = "vulkan"
	BackendMetal  BackendType = "metal"
	BackendCPU    BackendType = "cpu"
)

type PrereqIssue struct {
	Name        string
	InstallHint string
}

type Info struct {
	OS                string
	Arch              string
	CPUName           string
	CPUCores          int
	TotalRAMBytes     uint64
	GPUName           string
	GPUVendor         string
	VRAMMB            int
	SelectedBackend   BackendType
	FallbackBackends  []BackendType
	Supported         bool
	UnsupportedReason string
	MissingPrereqs    []PrereqIssue
}

type ProbeEnv struct {
	GOOS          string
	GOARCH        string
	HasNvidiaSMI  bool
	NvidiaGPUName string
	NvidiaVRAMMB  int
	HasRocmSMI    bool
	AmdGPUName    string
	AmdVRAMMB     int
	HasIntelGPU   bool
	HasVulkanLib  bool
	HasLibGomp    bool
	VulkanDevice  string
}

func Detect() Info {
	env := collectProbeEnv()
	info := Evaluate(env)

	if c, err := cpu.Info(); err == nil && len(c) > 0 {
		info.CPUName = strings.TrimSpace(c[0].ModelName)
	}
	if info.CPUName == "" {
		info.CPUName = runtime.GOARCH + " CPU"
	}
	info.CPUCores = runtime.NumCPU()

	if v, err := mem.VirtualMemory(); err == nil {
		info.TotalRAMBytes = v.Total
	}
	return info
}

func collectProbeEnv() ProbeEnv {
	env := ProbeEnv{
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		VulkanDevice: os.Getenv("SD_VULKAN_DEVICE"),
	}

	if out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && lines[0] != "" {
			parts := strings.Split(lines[0], ",")
			env.HasNvidiaSMI = true
			env.NvidiaGPUName = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				env.NvidiaVRAMMB, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			}
		}
	}

	if out, err := exec.Command("rocm-smi", "--showproductname").Output(); err == nil && len(out) > 0 {
		env.HasRocmSMI = true
		env.AmdGPUName = "AMD Radeon (ROCm)"
	}

	if runtime.GOOS == "linux" {
		env.HasVulkanLib = fileExists("/usr/lib/x86_64-linux-gnu/libvulkan.so.1") ||
			fileExists("/usr/lib/libvulkan.so.1") ||
			fileExists("/usr/lib/aarch64-linux-gnu/libvulkan.so.1")
		env.HasLibGomp = fileExists("/usr/lib/x86_64-linux-gnu/libgomp.so.1") ||
			fileExists("/usr/lib/libgomp.so.1") ||
			fileExists("/usr/lib/aarch64-linux-gnu/libgomp.so.1")
		if entries, err := os.ReadDir("/sys/class/drm"); err == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "card") && !strings.Contains(e.Name(), "-") {
					data, err := os.ReadFile("/sys/class/drm/" + e.Name() + "/device/vendor")
					if err == nil {
						v := strings.TrimSpace(string(data))
						if v == "0x1002" && env.AmdGPUName == "" {
							env.AmdGPUName = "AMD Radeon GPU"
						}
						if v == "0x8086" {
							env.HasIntelGPU = true
						}
					}
				}
			}
		}
	} else if runtime.GOOS == "windows" {
		env.HasVulkanLib = true
		env.HasLibGomp = true
		if out, err := exec.Command("wmic", "path", "win32_VideoController", "get", "name").Output(); err == nil {
			s := strings.ToLower(string(out))
			if strings.Contains(s, "amd") || strings.Contains(s, "radeon") {
				env.AmdGPUName = "AMD Radeon Graphics"
			}
			if strings.Contains(s, "intel") {
				env.HasIntelGPU = true
			}
		}
	}

	return env
}

func Evaluate(env ProbeEnv) Info {
	info := Info{
		OS:        env.GOOS,
		Arch:      env.GOARCH,
		Supported: true,
	}

	switch env.GOOS {
	case "darwin":
		if env.GOARCH != "arm64" {
			info.Supported = false
			info.UnsupportedReason = "macOS Intel (x86_64) não é suportado. Apenas Macs com Apple Silicon (arm64) possuem suporte Metal."
			info.GPUVendor = "intel"
			info.GPUName = "Intel Mac (Não suportado)"
			info.SelectedBackend = BackendCPU
			return info
		}
		info.GPUVendor = "apple"
		info.GPUName = "Apple Silicon GPU (Metal)"
		info.SelectedBackend = BackendMetal
		info.FallbackBackends = []BackendType{BackendCPU}

	case "windows":
		if env.HasNvidiaSMI {
			info.GPUVendor = "nvidia"
			info.GPUName = env.NvidiaGPUName
			info.VRAMMB = env.NvidiaVRAMMB
			info.SelectedBackend = BackendCUDA
			info.FallbackBackends = []BackendType{BackendVulkan, BackendCPU}
		} else if env.AmdGPUName != "" {
			info.GPUVendor = "amd"
			info.GPUName = env.AmdGPUName
			info.SelectedBackend = BackendVulkan
			info.FallbackBackends = []BackendType{BackendCPU}
		} else if env.HasIntelGPU {
			info.GPUVendor = "intel"
			info.GPUName = "Intel Arc / Integrated Graphics"
			info.SelectedBackend = BackendVulkan
			info.FallbackBackends = []BackendType{BackendCPU}
		} else {
			info.GPUVendor = "none"
			info.GPUName = "CPU Fallback"
			info.SelectedBackend = BackendCPU
		}

	case "linux":
		if env.HasNvidiaSMI {
			info.GPUVendor = "nvidia"
			info.GPUName = env.NvidiaGPUName
			info.VRAMMB = env.NvidiaVRAMMB
			info.SelectedBackend = BackendCUDA
			info.FallbackBackends = []BackendType{BackendVulkan, BackendCPU}
		} else if env.HasRocmSMI {
			info.GPUVendor = "amd"
			info.GPUName = env.AmdGPUName
			info.VRAMMB = env.AmdVRAMMB
			info.SelectedBackend = BackendROCm
			info.FallbackBackends = []BackendType{BackendVulkan, BackendCPU}
		} else if env.AmdGPUName != "" {
			info.GPUVendor = "amd"
			info.GPUName = env.AmdGPUName
			info.SelectedBackend = BackendVulkan
			info.FallbackBackends = []BackendType{BackendCPU}
		} else if env.HasIntelGPU {
			info.GPUVendor = "intel"
			info.GPUName = "Intel Graphics"
			info.SelectedBackend = BackendVulkan
			info.FallbackBackends = []BackendType{BackendCPU}
		} else {
			info.GPUVendor = "none"
			info.GPUName = "CPU Only"
			info.SelectedBackend = BackendCPU
		}

		if !env.HasLibGomp {
			info.MissingPrereqs = append(info.MissingPrereqs, PrereqIssue{
				Name:        "libgomp.so.1",
				InstallHint: linuxPackageHint("libgomp"),
			})
		}
		if info.SelectedBackend == BackendVulkan && !env.HasVulkanLib {
			info.MissingPrereqs = append(info.MissingPrereqs, PrereqIssue{
				Name:        "libvulkan.so.1",
				InstallHint: linuxPackageHint("vulkan"),
			})
		}

	default:
		info.SelectedBackend = BackendCPU
	}

	return info
}

func linuxPackageHint(kind string) string {
	osRelease, _ := os.ReadFile("/etc/os-release")
	content := strings.ToLower(string(osRelease))
	switch {
	case strings.Contains(content, "ubuntu") || strings.Contains(content, "debian"):
		if kind == "vulkan" {
			return "sudo apt update && sudo apt install -y libvulkan1 vulkan-tools"
		}
		return "sudo apt update && sudo apt install -y libgomp1"
	case strings.Contains(content, "fedora") || strings.Contains(content, "rhel"):
		if kind == "vulkan" {
			return "sudo dnf install -y vulkan-loader"
		}
		return "sudo dnf install -y libgomp"
	case strings.Contains(content, "arch"):
		if kind == "vulkan" {
			return "sudo pacman -S vulkan-icd-loader"
		}
		return "sudo pacman -S gcc-libs"
	default:
		if kind == "vulkan" {
			return "Instale o pacote libvulkan / vulkan-icd-loader da sua distribuição"
		}
		return "Instale o pacote libgomp / gcc-libs da sua distribuição"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
