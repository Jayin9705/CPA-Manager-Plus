//go:build !linux

package servermetrics

import (
	"os"
	"runtime"
	"time"
)

func readRawSample() (rawSample, error) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	hostname, _ := os.Hostname()
	available := uint64(0)
	if stats.Sys >= stats.Alloc {
		available = stats.Sys - stats.Alloc
	}
	return rawSample{
		collectedAt:          time.Now(),
		memoryTotal:          stats.Sys,
		memoryAvailable:      available,
		hostname:             hostname,
		operatingSystem:      runtime.GOOS,
		processResidentBytes: stats.Sys,
	}, nil
}
