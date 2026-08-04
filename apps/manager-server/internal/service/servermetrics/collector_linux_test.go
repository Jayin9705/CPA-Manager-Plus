//go:build linux

package servermetrics

import "testing"

func TestMetricsPathsUseConfiguredHostMounts(t *testing.T) {
	t.Setenv("CPAMP_METRICS_PROC_DIR", "/host/proc")
	t.Setenv("CPAMP_METRICS_ETC_DIR", "/host/etc")
	t.Setenv("CPAMP_METRICS_DISK_PATH", "/manager-data")

	if got := metricsProcPath("net/dev"); got != "/host/proc/net/dev" {
		t.Fatalf("metrics proc path = %q", got)
	}
	if got := metricsEtcPath("hostname"); got != "/host/etc/hostname" {
		t.Fatalf("metrics etc path = %q", got)
	}
	if got := metricsDiskPath(); got != "/manager-data" {
		t.Fatalf("metrics disk path = %q", got)
	}
}
