package servermetrics

import (
	"runtime"
	"sync"
	"time"
)

// Snapshot is a point-in-time, read-only view of the host running Manager Server.
// It intentionally contains aggregate resource data only.
type Snapshot struct {
	TimestampMS int64   `json:"timestampMs"`
	CPU         CPU     `json:"cpu"`
	Memory      Memory  `json:"memory"`
	Disk        Disk    `json:"disk"`
	Network     Network `json:"network"`
	Host        Host    `json:"host"`
	Process     Process `json:"process"`
}

type CPU struct {
	UsagePercent float64 `json:"usagePercent"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	Cores        int     `json:"cores"`
}

type Memory struct {
	TotalBytes     uint64  `json:"totalBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	AvailableBytes uint64  `json:"availableBytes"`
	UsagePercent   float64 `json:"usagePercent"`
}

type Disk struct {
	MountPoint     string  `json:"mountPoint"`
	TotalBytes     uint64  `json:"totalBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	AvailableBytes uint64  `json:"availableBytes"`
	UsagePercent   float64 `json:"usagePercent"`
}

type Network struct {
	ReceiveBytesPerSecond  float64 `json:"receiveBytesPerSecond"`
	TransmitBytesPerSecond float64 `json:"transmitBytesPerSecond"`
}

type Host struct {
	Hostname        string  `json:"hostname"`
	OperatingSystem string  `json:"operatingSystem"`
	Architecture    string  `json:"architecture"`
	UptimeSeconds   float64 `json:"uptimeSeconds"`
}

type Process struct {
	UptimeSeconds       float64 `json:"uptimeSeconds"`
	ResidentMemoryBytes uint64  `json:"residentMemoryBytes"`
	Goroutines          int     `json:"goroutines"`
}

type rawSample struct {
	collectedAt          time.Time
	cpuTotal             uint64
	cpuIdle              uint64
	load1                float64
	load5                float64
	load15               float64
	memoryTotal          uint64
	memoryAvailable      uint64
	diskTotal            uint64
	diskFree             uint64
	diskAvailable        uint64
	diskMountPoint       string
	networkReceive       uint64
	networkTransmit      uint64
	hostname             string
	operatingSystem      string
	systemUptimeSeconds  float64
	processResidentBytes uint64
}

// Collector retains only the previous aggregate counters so CPU utilization and
// network throughput can be calculated between requests.
type Collector struct {
	mu          sync.Mutex
	startedAtMS int64
	previous    *rawSample
}

func New(startedAtMS int64) *Collector {
	if startedAtMS <= 0 {
		startedAtMS = time.Now().UnixMilli()
	}
	collector := &Collector{startedAtMS: startedAtMS}
	if sample, err := readRawSample(); err == nil {
		collector.previous = &sample
	}
	return collector
}

func (c *Collector) Snapshot() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current, err := readRawSample()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := buildSnapshot(current, c.previous, c.startedAtMS)
	c.previous = &current
	return snapshot, nil
}

func buildSnapshot(current rawSample, previous *rawSample, startedAtMS int64) Snapshot {
	cpuUsage := ratioPercent(current.cpuTotal-current.cpuIdle, current.cpuTotal)
	receiveRate := float64(0)
	transmitRate := float64(0)
	if previous != nil {
		if current.cpuTotal > previous.cpuTotal && current.cpuIdle >= previous.cpuIdle {
			totalDelta := current.cpuTotal - previous.cpuTotal
			idleDelta := current.cpuIdle - previous.cpuIdle
			if idleDelta <= totalDelta {
				cpuUsage = ratioPercent(totalDelta-idleDelta, totalDelta)
			}
		}
		seconds := current.collectedAt.Sub(previous.collectedAt).Seconds()
		if seconds > 0 {
			if current.networkReceive >= previous.networkReceive {
				receiveRate = float64(current.networkReceive-previous.networkReceive) / seconds
			}
			if current.networkTransmit >= previous.networkTransmit {
				transmitRate = float64(current.networkTransmit-previous.networkTransmit) / seconds
			}
		}
	}

	memoryUsed := uint64(0)
	if current.memoryTotal >= current.memoryAvailable {
		memoryUsed = current.memoryTotal - current.memoryAvailable
	}
	diskUsed := uint64(0)
	if current.diskTotal >= current.diskFree {
		diskUsed = current.diskTotal - current.diskFree
	}
	processUptime := float64(current.collectedAt.UnixMilli()-startedAtMS) / 1000
	if processUptime < 0 {
		processUptime = 0
	}
	diskMountPoint := current.diskMountPoint
	if diskMountPoint == "" {
		diskMountPoint = "/"
	}

	return Snapshot{
		TimestampMS: current.collectedAt.UnixMilli(),
		CPU: CPU{
			UsagePercent: clampPercent(cpuUsage),
			Load1:        current.load1,
			Load5:        current.load5,
			Load15:       current.load15,
			Cores:        runtime.NumCPU(),
		},
		Memory: Memory{
			TotalBytes:     current.memoryTotal,
			UsedBytes:      memoryUsed,
			AvailableBytes: current.memoryAvailable,
			UsagePercent:   clampPercent(ratioPercent(memoryUsed, current.memoryTotal)),
		},
		Disk: Disk{
			MountPoint:     diskMountPoint,
			TotalBytes:     current.diskTotal,
			UsedBytes:      diskUsed,
			AvailableBytes: current.diskAvailable,
			UsagePercent:   clampPercent(ratioPercent(diskUsed, current.diskTotal)),
		},
		Network: Network{
			ReceiveBytesPerSecond:  receiveRate,
			TransmitBytesPerSecond: transmitRate,
		},
		Host: Host{
			Hostname:        current.hostname,
			OperatingSystem: current.operatingSystem,
			Architecture:    runtime.GOARCH,
			UptimeSeconds:   current.systemUptimeSeconds,
		},
		Process: Process{
			UptimeSeconds:       processUptime,
			ResidentMemoryBytes: current.processResidentBytes,
			Goroutines:          runtime.NumGoroutine(),
		},
	}
}

func ratioPercent(numerator, denominator uint64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator) * 100
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
