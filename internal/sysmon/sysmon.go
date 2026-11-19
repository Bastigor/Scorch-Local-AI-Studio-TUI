package sysmon

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"local-ai-studio-tui/internal/hardware"
)

type Metrics struct {
	Timestamp   time.Time
	CPUPercent  float64
	RAMUsedMB   uint64
	RAMTotalMB  uint64
	RAMPercent  float64
	GPUSource   string
	GPUPercent  float64
	VRAMUsedMB  uint64
	VRAMTotalMB uint64
}

type Collector struct {
	mu      sync.Mutex
	history []Metrics
	maxLen  int
	hw      hardware.Info
}

func NewCollector() *Collector {
	return &Collector{
		maxLen: 60,
		hw:     hardware.Detect(),
	}
}

func (c *Collector) Sample() Metrics {
	m := Metrics{
		Timestamp:   time.Now(),
		GPUSource:   c.hw.GPUName,
		VRAMTotalMB: uint64(c.hw.VRAMMB),
	}

	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		m.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		m.RAMUsedMB = vm.Used / (1024 * 1024)
		m.RAMTotalMB = vm.Total / (1024 * 1024)
		m.RAMPercent = vm.UsedPercent
	}

	if src, gpuPct, vUsed, vTotal, ok := sampleNvidia(); ok {
		m.GPUSource = src
		m.GPUPercent = gpuPct
		m.VRAMUsedMB = vUsed
		m.VRAMTotalMB = vTotal
	} else if runtime.GOOS == "windows" && c.hw.GPUVendor != "none" {
		if gpuPct, vUsed, ok := sampleWindowsGPUCounters(); ok {
			m.GPUPercent = gpuPct
			m.VRAMUsedMB = vUsed
		}
	}

	c.mu.Lock()
	c.history = append(c.history, m)
	if len(c.history) > c.maxLen {
		c.history = c.history[len(c.history)-c.maxLen:]
	}
	c.mu.Unlock()

	return m
}

func (c *Collector) History() []Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Metrics, len(c.history))
	copy(out, c.history)
	return out
}

func sampleNvidia() (string, float64, uint64, uint64, bool) {
	bin := hardware.FindNvidiaSMI()
	if bin == "" {
		return "", 0, 0, 0, false
	}
	out, err := exec.Command(bin, "--query-gpu=name,utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return "", 0, 0, 0, false
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return "", 0, 0, 0, false
	}
	name := strings.TrimSpace(parts[0])
	util, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	used, _ := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64)
	total, _ := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64)
	return name, util, used, total, true
}

func sampleWindowsGPUCounters() (float64, uint64, bool) {
	ps := `$u=(Get-Counter '\GPU Engine(*engtype_3D)\Utilization Percentage' -ErrorAction SilentlyContinue).CounterSamples | Measure-Object CookedValue -Sum; $m=(Get-Counter '\GPU Adapter Memory(*)\Dedicated Usage' -ErrorAction SilentlyContinue).CounterSamples | Measure-Object CookedValue -Sum; Write-Output ("{0},{1}" -f [math]::Round($u.Sum,1), [math]::Round($m.Sum/1MB,0))`
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Output()
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) < 2 {
		return 0, 0, false
	}
	util, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if util > 100 {
		util = 100
	}
	vUsed, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
	return util, vUsed, true
}
