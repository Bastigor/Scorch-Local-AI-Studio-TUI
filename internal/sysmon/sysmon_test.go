package sysmon

import (
	"testing"
	"time"
)

func TestParseWindowsGPU(t *testing.T) {
	s, ok := parseWindowsGPU("37.5;2150;8176\r\n")
	if !ok || s.Percent != 37.5 || s.UsedMB != 2150 || s.TotalMB != 8176 {
		t.Fatalf("parse inesperado: %+v ok=%v", s, ok)
	}
	if _, ok := parseWindowsGPU("lixo"); ok {
		t.Fatal("esperava falha para saída inválida")
	}
	if s, _ := parseWindowsGPU("250.0;10;20"); s.Percent != 100 {
		t.Fatalf("utilização deveria ser limitada a 100, obteve %v", s.Percent)
	}
}

func TestCollectorVRAMFromAsyncSampler(t *testing.T) {
	c := NewCollector()
	c.gpuInterval = 0
	c.SetFallbackVRAMTotal(8192)
	c.sampler = func() gpuSample {
		return gpuSample{Available: true, Source: "teste", Percent: 10, UsedMB: 4096}
	}
	var m Metrics
	for i := 0; i < 50; i++ {
		m = c.Sample()
		if m.VRAMUsedMB == 4096 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.VRAMUsedMB != 4096 || m.VRAMTotalMB != 8192 {
		t.Fatalf("VRAM inesperada: %d/%d", m.VRAMUsedMB, m.VRAMTotalMB)
	}
	if m.VRAMPercent < 49 || m.VRAMPercent > 51 {
		t.Fatalf("percentual de VRAM inesperado: %v", m.VRAMPercent)
	}
}
