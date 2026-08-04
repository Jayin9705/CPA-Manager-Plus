//go:build linux

package servermetrics

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readRawSample() (rawSample, error) {
	cpuTotal, cpuIdle, err := readCPU()
	if err != nil {
		return rawSample{}, err
	}
	memoryTotal, memoryAvailable, err := readMemory()
	if err != nil {
		return rawSample{}, err
	}
	diskMountPoint := metricsDiskPath()
	diskTotal, diskFree, diskAvailable, err := readDisk(diskMountPoint)
	if err != nil {
		return rawSample{}, err
	}
	networkReceive, networkTransmit, err := readNetwork()
	if err != nil {
		return rawSample{}, err
	}
	load1, load5, load15 := readLoadAverage()
	hostname := readHostname()

	return rawSample{
		collectedAt:          time.Now(),
		cpuTotal:             cpuTotal,
		cpuIdle:              cpuIdle,
		load1:                load1,
		load5:                load5,
		load15:               load15,
		memoryTotal:          memoryTotal,
		memoryAvailable:      memoryAvailable,
		diskTotal:            diskTotal,
		diskFree:             diskFree,
		diskAvailable:        diskAvailable,
		diskMountPoint:       diskMountPoint,
		networkReceive:       networkReceive,
		networkTransmit:      networkTransmit,
		hostname:             hostname,
		operatingSystem:      readOperatingSystem(),
		systemUptimeSeconds:  readSystemUptime(),
		processResidentBytes: readProcessResidentMemory(),
	}, nil
}

func readCPU() (uint64, uint64, error) {
	contents, err := os.ReadFile(metricsProcPath("stat"))
	if err != nil {
		return 0, 0, fmt.Errorf("read cpu counters: %w", err)
	}
	line, _, _ := strings.Cut(string(contents), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, errors.New("read cpu counters: unexpected /proc/stat format")
	}
	var total uint64
	var idle uint64
	// guest and guest_nice are already included in user and nice, so only the
	// first eight counters participate in the total.
	counters := fields[1:]
	if len(counters) > 8 {
		counters = counters[:8]
	}
	for index, field := range counters {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("parse cpu counter: %w", parseErr)
		}
		total += value
		if index == 3 || index == 4 {
			idle += value
		}
	}
	return total, idle, nil
}

func readMemory() (uint64, uint64, error) {
	file, err := os.Open(metricsProcPath("meminfo"))
	if err != nil {
		return 0, 0, fmt.Errorf("read memory counters: %w", err)
	}
	defer file.Close()

	values := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" && key != "MemFree" && key != "Buffers" && key != "Cached" {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr == nil {
			values[key] = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan memory counters: %w", err)
	}
	total := values["MemTotal"]
	if total == 0 {
		return 0, 0, errors.New("read memory counters: MemTotal is unavailable")
	}
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if available > total {
		available = total
	}
	return total, available, nil
}

func readDisk(path string) (uint64, uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0, fmt.Errorf("read filesystem %s: %w", path, err)
	}
	blockSize := uint64(stat.Bsize)
	return stat.Blocks * blockSize, stat.Bfree * blockSize, stat.Bavail * blockSize, nil
}

func readNetwork() (uint64, uint64, error) {
	file, err := os.Open(metricsProcPath("net/dev"))
	if err != nil {
		return 0, 0, fmt.Errorf("read network counters: %w", err)
	}
	defer file.Close()

	var receive uint64
	var transmit uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		namePart, countersPart, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(namePart) == "lo" {
			continue
		}
		fields := strings.Fields(countersPart)
		if len(fields) < 9 {
			continue
		}
		received, receiveErr := strconv.ParseUint(fields[0], 10, 64)
		transmitted, transmitErr := strconv.ParseUint(fields[8], 10, 64)
		if receiveErr == nil {
			receive += received
		}
		if transmitErr == nil {
			transmit += transmitted
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan network counters: %w", err)
	}
	return receive, transmit, nil
}

func readLoadAverage() (float64, float64, float64) {
	contents, err := os.ReadFile(metricsProcPath("loadavg"))
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(contents))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	load1, _ := strconv.ParseFloat(fields[0], 64)
	load5, _ := strconv.ParseFloat(fields[1], 64)
	load15, _ := strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15
}

func readSystemUptime() float64 {
	contents, err := os.ReadFile(metricsProcPath("uptime"))
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(contents))
	if len(fields) == 0 {
		return 0
	}
	uptime, _ := strconv.ParseFloat(fields[0], 64)
	return uptime
}

func readProcessResidentMemory() uint64 {
	contents, err := os.ReadFile("/proc/self/statm")
	if err == nil {
		fields := strings.Fields(string(contents))
		if len(fields) >= 2 {
			pages, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}

func readOperatingSystem() string {
	contents, err := os.ReadFile(metricsEtcPath("os-release"))
	if err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if !ok || key != "PRETTY_NAME" {
				continue
			}
			value = strings.TrimSpace(value)
			if unquoted, unquoteErr := strconv.Unquote(value); unquoteErr == nil {
				value = unquoted
			}
			if value != "" {
				return value
			}
		}
	}
	return runtime.GOOS
}

func readHostname() string {
	if contents, err := os.ReadFile(metricsEtcPath("hostname")); err == nil {
		if hostname := strings.TrimSpace(string(contents)); hostname != "" {
			return hostname
		}
	}
	hostname, _ := os.Hostname()
	return hostname
}

func metricsProcPath(name string) string {
	return filepath.Join(metricsRoot("CPAMP_METRICS_PROC_DIR", "/proc"), name)
}

func metricsEtcPath(name string) string {
	return filepath.Join(metricsRoot("CPAMP_METRICS_ETC_DIR", "/etc"), name)
}

func metricsDiskPath() string {
	return metricsRoot("CPAMP_METRICS_DISK_PATH", "/")
}

func metricsRoot(envName string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value
	}
	return fallback
}
