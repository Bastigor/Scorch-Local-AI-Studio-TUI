package hardware

import (
	"encoding/json"
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

type Prereq struct {
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
	Supported         bool
	UnsupportedReason string
	MissingPrereqs    []Prereq
}

func Detect() Info {
	info := Info{
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		CPUName:         "Unknown CPU",
		CPUCores:        runtime.NumCPU(),
		GPUName:         "CPU Fallback",
		GPUVendor:       "none",
		SelectedBackend: BackendCPU,
		Supported:       true,
	}

	if cpuInfos, err := cpu.Info(); err == nil && len(cpuInfos) > 0 {
		if name := strings.TrimSpace(cpuInfos[0].ModelName); name != "" {
			info.CPUName = name
		}
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		info.TotalRAMBytes = vm.Total
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		info.GPUName = "Apple Silicon GPU"
		info.GPUVendor = "apple"
		info.SelectedBackend = BackendMetal
		if info.TotalRAMBytes > 0 {
			info.VRAMMB = int(info.TotalRAMBytes / (1024 * 1024))
		}
		return info
	}

	if gpuName, vram, ok := detectNvidiaSMI(); ok {
		info.GPUName = gpuName
		info.GPUVendor = "nvidia"
		info.VRAMMB = vram
		info.SelectedBackend = BackendCUDA
		return info
	}

	if gpuName, vram, ok := detectRocmSMI(); ok {
		info.GPUName = gpuName
		info.GPUVendor = "amd"
		info.VRAMMB = vram
		info.SelectedBackend = BackendROCm
		return info
	}

	if runtime.GOOS == "windows" {
		if gpuName, vendor, vram, ok := detectWindowsVideoController(); ok {
			info.GPUName = gpuName
			info.GPUVendor = vendor
			info.VRAMMB = vram
			switch vendor {
			case "nvidia":
				info.SelectedBackend = BackendCUDA
			case "amd", "intel":
				info.SelectedBackend = BackendVulkan
			default:
				info.SelectedBackend = BackendCPU
			}
			return info
		}
	}

	return info
}

func FindNvidiaSMI() string {
	candidates := []string{"nvidia-smi"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\Windows\System32\nvidia-smi.exe`,
			`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`,
		)
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func detectNvidiaSMI() (string, int, bool) {
	bin := FindNvidiaSMI()
	if bin == "" {
		return "", 0, false
	}
	out, err := exec.Command(bin, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return "", 0, false
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	parts := strings.Split(line, ",")
	if len(parts) < 2 {
		return "", 0, false
	}
	name := strings.TrimSpace(parts[0])
	vram, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	return name, vram, name != ""
}

func detectRocmSMI() (string, int, bool) {
	bin, err := exec.LookPath("rocm-smi")
	if err != nil {
		return "", 0, false
	}
	out, err := exec.Command(bin, "--showproductname").Output()
	if err != nil {
		return "", 0, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Card series:") || strings.Contains(line, "Card model:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), 0, true
			}
		}
	}
	return "", 0, false
}

func detectWindowsVideoController() (string, string, int, bool) {
	psCmd := `Get-CimInstance Win32_VideoController | Select-Object Name,AdapterRAM,AdapterCompatibility | ConvertTo-Json -Compress`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output()
	if err != nil {
		return "", "", 0, false
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", "", 0, false
	}

	type winGPU struct {
		Name                 string `json:"Name"`
		AdapterRAM           uint64 `json:"AdapterRAM"`
		AdapterCompatibility string `json:"AdapterCompatibility"`
	}

	var list []winGPU
	if strings.HasPrefix(raw, "[") {
		_ = json.Unmarshal([]byte(raw), &list)
	} else {
		var single winGPU
		if err := json.Unmarshal([]byte(raw), &single); err == nil {
			list = append(list, single)
		}
	}

	var best winGPU
	bestScore := -1
	for _, g := range list {
		name := strings.TrimSpace(g.Name)
		low := strings.ToLower(name + " " + g.AdapterCompatibility)
		if name == "" || strings.Contains(low, "microsoft basic") || strings.Contains(low, "virtual") || strings.Contains(low, "parsec") {
			continue
		}
		score := 1
		if strings.Contains(low, "nvidia") || strings.Contains(low, "geforce") || strings.Contains(low, "rtx") || strings.Contains(low, "gtx") {
			score = 3
		} else if strings.Contains(low, "amd") || strings.Contains(low, "radeon") {
			score = 3
		} else if strings.Contains(low, "intel") && (strings.Contains(low, "arc") || strings.Contains(low, "iris")) {
			score = 2
		}
		if score > bestScore {
			bestScore = score
			best = g
		}
	}

	if bestScore < 0 {
		return "", "", 0, false
	}

	low := strings.ToLower(best.Name + " " + best.AdapterCompatibility)
	vendor := "unknown"
	switch {
	case strings.Contains(low, "nvidia") || strings.Contains(low, "geforce") || strings.Contains(low, "rtx") || strings.Contains(low, "gtx"):
		vendor = "nvidia"
	case strings.Contains(low, "amd") || strings.Contains(low, "radeon") || strings.Contains(low, "advanced micro devices"):
		vendor = "amd"
	case strings.Contains(low, "intel"):
		vendor = "intel"
	}

	vramMB := int(best.AdapterRAM / (1024 * 1024))
	return strings.TrimSpace(best.Name), vendor, vramMB, true
}
