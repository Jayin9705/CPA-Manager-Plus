package servermetrics

import (
	"math"
	"runtime"
	"testing"
	"time"
)

func TestCollectorReadsHostSnapshot(t *testing.T) {
	collector := New(time.Now().Add(-2 * time.Second).UnixMilli())
	snapshot, err := collector.Snapshot()
	if err != nil {
		t.Fatalf("collect snapshot: %v", err)
	}
	if snapshot.TimestampMS <= 0 || snapshot.CPU.Cores <= 0 || snapshot.Host.Architecture == "" {
		t.Fatalf("incomplete snapshot: %#v", snapshot)
	}
	if runtime.GOOS == "linux" && (snapshot.Memory.TotalBytes == 0 || snapshot.Disk.TotalBytes == 0) {
		t.Fatalf("linux resource totals are missing: %#v", snapshot)
	}
}

func TestBuildSnapshotCalculatesCounterDeltas(t *testing.T) {
	startedAt := time.Unix(100, 0)
	previous := rawSample{
		collectedAt:     time.Unix(110, 0),
		cpuTotal:        1_000,
		cpuIdle:         700,
		networkReceive:  1_000,
		networkTransmit: 2_000,
	}
	current := rawSample{
		collectedAt:          time.Unix(115, 0),
		cpuTotal:             1_500,
		cpuIdle:              1_000,
		memoryTotal:          1_000,
		memoryAvailable:      250,
		diskTotal:            2_000,
		diskFree:             500,
		diskAvailable:        400,
		diskMountPoint:       "/metrics-volume",
		networkReceive:       2_000,
		networkTransmit:      4_500,
		systemUptimeSeconds:  1_000,
		processResidentBytes: 123,
	}

	snapshot := buildSnapshot(current, &previous, startedAt.UnixMilli())
	assertClose(t, snapshot.CPU.UsagePercent, 40)
	assertClose(t, snapshot.Memory.UsagePercent, 75)
	assertClose(t, snapshot.Disk.UsagePercent, 75)
	assertClose(t, snapshot.Network.ReceiveBytesPerSecond, 200)
	assertClose(t, snapshot.Network.TransmitBytesPerSecond, 500)
	assertClose(t, snapshot.Process.UptimeSeconds, 15)
	if snapshot.Disk.MountPoint != "/metrics-volume" {
		t.Fatalf("disk mount point = %q", snapshot.Disk.MountPoint)
	}
}

func assertClose(t *testing.T, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 0.001 {
		t.Fatalf("actual = %v, want %v", actual, expected)
	}
}
