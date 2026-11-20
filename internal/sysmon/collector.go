package sysmon

import (
	"sync"
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
	VRAMPercent  float64
	CPUHistory   []float64
	RAMHistory   []float64
	GPUHistory   []float64
	VRAMHistory  []float64
}

type Collector struct {
	mu       sync.Mutex
	cpuHist  []float64
	ramHist  []float64
	gpuHist  []float64
	vramHist []float64
	maxPts   int

	gpu         gpuSample
	gpuBusy     bool
	gpuLastRun  time.Time
	gpuInterval time.Duration
	fallbackMB  int

	// sampler pode ser trocado nos testes.
	sampler func() gpuSample
}

func NewCollector() *Collector {
	return &Collector{maxPts: 30, gpuInterval: 1500 * time.Millisecond, sampler: sampleGPU}
}

// SetFallbackVRAMTotal define o total de VRAM (MB) usado quando a fonte de
// leitura não informa o total (ex.: valor detectado na inicialização).
func (c *Collector) SetFallbackVRAMTotal(mb int) {
	c.mu.Lock()
	c.fallbackMB = mb
	c.mu.Unlock()
}

// refreshGPU dispara a leitura da GPU em segundo plano. As ferramentas externas
// (PowerShell, nvidia-smi) podem levar mais de 1 s, então nunca bloqueiam a interface.
func (c *Collector) refreshGPU() {
	if c.gpuBusy || time.Since(c.gpuLastRun) < c.gpuInterval {
		return
	}
	c.gpuBusy = true
	c.gpuLastRun = time.Now()
	sampler := c.sampler
	go func() {
		s := sampler()
		c.mu.Lock()
		if s.Available || !c.gpu.Available {
			c.gpu = s
		}
		c.gpuBusy = false
		c.gpuLastRun = time.Now()
		c.mu.Unlock()
	}()
}

func (c *Collector) Sample() Metrics {
	var m Metrics

	// Com intervalo 0, o gopsutil usa o tempo desde a chamada anterior (sem bloquear).
	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		m.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		m.RAMUsedMB = vm.Used / (1024 * 1024)
		m.RAMTotalMB = vm.Total / (1024 * 1024)
		m.RAMPercent = vm.UsedPercent
	}

	c.mu.Lock()
	c.refreshGPU()
	g := c.gpu
	fallback := c.fallbackMB
	c.mu.Unlock()

	m.GPUAvailable = g.Available
	m.GPUSource = g.Source
	m.GPUPercent = g.Percent
	m.VRAMUsedMB = g.UsedMB
	m.VRAMTotalMB = g.TotalMB
	if m.VRAMTotalMB <= 0 {
		m.VRAMTotalMB = fallback
	}
	if m.VRAMTotalMB > 0 {
		m.VRAMPercent = float64(m.VRAMUsedMB) / float64(m.VRAMTotalMB) * 100
		if m.VRAMPercent > 100 {
			m.VRAMPercent = 100
		}
	}

	c.mu.Lock()
	c.cpuHist = appendRing(c.cpuHist, m.CPUPercent, c.maxPts)
	c.ramHist = appendRing(c.ramHist, m.RAMPercent, c.maxPts)
	c.gpuHist = appendRing(c.gpuHist, m.GPUPercent, c.maxPts)
	c.vramHist = appendRing(c.vramHist, m.VRAMPercent, c.maxPts)
	m.CPUHistory = append([]float64(nil), c.cpuHist...)
	m.RAMHistory = append([]float64(nil), c.ramHist...)
	m.GPUHistory = append([]float64(nil), c.gpuHist...)
	m.VRAMHistory = append([]float64(nil), c.vramHist...)
	c.mu.Unlock()
	return m
}

func appendRing(s []float64, v float64, max int) []float64 {
	if len(s) >= max {
		return append(s[1:], v)
	}
	return append(s, v)
}
