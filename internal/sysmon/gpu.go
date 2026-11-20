package sysmon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"local-ai-studio-tui/internal/hardware"
)

// gpuSample é o resultado de uma leitura de GPU.
type gpuSample struct {
	Available bool
	Source    string
	Percent   float64
	UsedMB    int
	TotalMB   int
}

// windowsGPUScript usa classes WMI (cujos nomes não são traduzidos, ao contrário
// dos caminhos de contadores de desempenho em Windows pt-BR) e a chave de registro
// do driver para obter o total de VRAM, já que Win32_VideoController.AdapterRAM é
// limitado a 4 GB. A saída usa cultura invariante: "util;usadoMB;totalMB".
const windowsGPUScript = `$ErrorActionPreference='SilentlyContinue'
$inv=[cultureinfo]::InvariantCulture
$u=(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine | Where-Object { $_.Name -like '*engtype_3D*' -or $_.Name -like '*engtype_Compute*' } | Measure-Object UtilizationPercentage -Sum).Sum
$m=(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUAdapterMemory | Measure-Object DedicatedUsage -Sum).Sum
$t=(Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11cd-bfc1-08002be10318}\0*' -Name 'HardwareInformation.qwMemorySize' | ForEach-Object { $_.'HardwareInformation.qwMemorySize' } | Measure-Object -Maximum).Maximum
if ($u -eq $null) { $u = 0 }
if ($m -eq $null) { $m = 0 }
if ($t -eq $null) { $t = 0 }
[string]::Format($inv,'{0:0.0};{1:0};{2:0}', [double]$u, [math]::Round($m/1MB), [math]::Round($t/1MB))`

func parseWindowsGPU(out string) (gpuSample, bool) {
	parts := strings.Split(strings.TrimSpace(out), ";")
	if len(parts) < 3 {
		return gpuSample{}, false
	}
	util, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	used, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	total, err3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return gpuSample{}, false
	}
	if util > 100 {
		util = 100
	}
	if used <= 0 && total <= 0 && util <= 0 {
		return gpuSample{}, false
	}
	return gpuSample{Available: true, Source: "Windows WMI", Percent: util, UsedMB: int(used), TotalMB: int(total)}, true
}

func sampleWindows() (gpuSample, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", windowsGPUScript).Output()
	if err != nil {
		return gpuSample{}, false
	}
	return parseWindowsGPU(string(out))
}

func sampleNvidia() (gpuSample, bool) {
	bin := hardware.FindNvidiaSMI()
	if bin == "" {
		return gpuSample{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpuSample{}, false
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
	parts := strings.Split(line, ",")
	if len(parts) < 3 {
		return gpuSample{}, false
	}
	s := gpuSample{Available: true, Source: "nvidia-smi"}
	s.Percent, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	s.UsedMB, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	s.TotalMB, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
	return s, true
}

func sampleRocm() (gpuSample, bool) {
	bin, err := exec.LookPath("rocm-smi")
	if err != nil {
		return gpuSample{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--showuse", "--showmeminfo", "vram", "--json").Output()
	if err != nil {
		return gpuSample{}, false
	}
	var raw map[string]map[string]string
	if json.Unmarshal(out, &raw) != nil {
		return gpuSample{}, false
	}
	for _, card := range raw {
		s := gpuSample{Available: true, Source: "rocm-smi"}
		if v, ok := card["GPU use (%)"]; ok {
			s.Percent, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
		}
		if v, ok := card["VRAM Total Memory (B)"]; ok {
			if b, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				s.TotalMB = int(b / (1024 * 1024))
			}
		}
		if v, ok := card["VRAM Total Used Memory (B)"]; ok {
			if b, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				s.UsedMB = int(b / (1024 * 1024))
			}
		}
		return s, true
	}
	return gpuSample{}, false
}

func readIntFile(path string) (int64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return v, err == nil
}

// sampleSysfs lê placas AMD (amdgpu) no Linux sem depender do rocm-smi.
func sampleSysfs() (gpuSample, bool) {
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device")
	for _, dev := range cards {
		used, ok1 := readIntFile(filepath.Join(dev, "mem_info_vram_used"))
		total, ok2 := readIntFile(filepath.Join(dev, "mem_info_vram_total"))
		if !ok1 || !ok2 || total <= 0 {
			continue
		}
		s := gpuSample{Available: true, Source: "amdgpu (sysfs)", UsedMB: int(used / (1024 * 1024)), TotalMB: int(total / (1024 * 1024))}
		if busy, ok := readIntFile(filepath.Join(dev, "gpu_busy_percent")); ok {
			s.Percent = float64(busy)
		}
		return s, true
	}
	return gpuSample{}, false
}

func sampleGPU() gpuSample {
	if s, ok := sampleNvidia(); ok {
		return s
	}
	switch runtime.GOOS {
	case "windows":
		if s, ok := sampleWindows(); ok {
			return s
		}
	case "linux":
		if s, ok := sampleRocm(); ok {
			return s
		}
		if s, ok := sampleSysfs(); ok {
			return s
		}
	}
	return gpuSample{}
}
