package sysmon

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type Metrics struct {
	CPUPercent   float64
	RAMUsedMB    uint64
	RAMTotalMB   uint64
	RAMPercent   float64
	GPUAvailable bool
	GPUSource    string
	GPUPercent   float64
	VRAMUsedMB   int
	VRAMTotalMB  int
	CPUHistory   []float64
	RAMHistory   []float64
	GPUHistory   []float64
	VRAMHistory  []float64
}

type Collector struct {
	cpuHist  []float64
	ramHist  []float64
	gpuHist  []float64
	vramHist []float64
	maxPts   int
}

func NewCollector() *Collector {
	return &Collector{maxPts: 30}
}

func (c *Collector) Sample() Metrics {
	var m Metrics

	if pcts, err := cpu.Percent(100*time.Millisecond, false); err == nil && len(pcts) > 0 {
		m.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.RAMUsedMB = vm.Used / (1024 * 1024)
		m.RAMTotalMB = vm.Total / (1024 * 1024)
		m.RAMPercent = vm.UsedPercent
	}

	if out, err := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits").Output(); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ",")
		if len(parts) >= 3 {
			m.GPUAvailable = true
			m.GPUSource = "nvidia-smi"
			m.GPUPercent, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			m.VRAMUsedMB, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
			m.VRAMTotalMB, _ = strconv.Atoi(strings.TrimSpace(parts[2]))
		}
	} else if out, err := exec.Command("rocm-smi", "--showuse", "--showmeminfo", "vram", "--json").Output(); err == nil {
		var raw map[string]map[string]string
		if json.Unmarshal(out, &raw) == nil {
			for _, card := range raw {
				m.GPUAvailable = true
				m.GPUSource = "rocm-smi"
				if useStr, ok := card["GPU use (%)"]; ok {
					m.GPUPercent, _ = strconv.ParseFloat(strings.TrimSpace(useStr), 64)
				}
				if totStr, ok := card["VRAM Total Memory (B)"]; ok {
					if b, err := strconv.ParseInt(strings.TrimSpace(totStr), 10, 64); err == nil {
						m.VRAMTotalMB = int(b / (1024 * 1024))
					}
				}
				if usedStr, ok := card["VRAM Total Used Memory (B)"]; ok {
					if b, err := strconv.ParseInt(strings.TrimSpace(usedStr), 10, 64); err == nil {
						m.VRAMUsedMB = int(b / (1024 * 1024))
					}
				}
				break
			}
		}
	}

	c.cpuHist = appendRing(c.cpuHist, m.CPUPercent, c.maxPts)
	c.ramHist = appendRing(c.ramHist, m.RAMPercent, c.maxPts)
	c.gpuHist = appendRing(c.gpuHist, m.GPUPercent, c.maxPts)
	vramPct := 0.0
	if m.VRAMTotalMB > 0 {
		vramPct = float64(m.VRAMUsedMB) / float64(m.VRAMTotalMB) * 100
	}
	c.vramHist = appendRing(c.vramHist, vramPct, c.maxPts)

	m.CPUHistory = append([]float64(nil), c.cpuHist...)
	m.RAMHistory = append([]float64(nil), c.ramHist...)
	m.GPUHistory = append([]float64(nil), c.gpuHist...)
	m.VRAMHistory = append([]float64(nil), c.vramHist...)
	return m
}

func appendRing(s []float64, v float64, max int) []float64 {
	if len(s) >= max {
		return append(s[1:], v)
	}
	return append(s, v)
}
