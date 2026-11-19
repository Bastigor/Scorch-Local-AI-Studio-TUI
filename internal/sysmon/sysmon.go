package sysmon

import (
	"os/exec"
	"strconv"
	"strings"
)

func SampleWindowsGPUCounters() (float64, uint64, bool) {
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
