package collector

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// buildLiveSnapshotter wires a Snapshotter pointing at the host's real
// /proc, /sys and Docker socket. Used by integration tests; sub-collectors
// degrade gracefully when individual sources are absent.
func buildLiveSnapshotter(t *testing.T) *Snapshotter {
	t.Helper()
	cfg := SnapshotterConfig{
		CPU:        NewCPUCollector("/proc", "/sys"),
		Memory:     NewMemoryCollector("/proc"),
		Disk:       NewDiskCollector("/proc", nil),
		Network:    NewNetworkCollector("/proc", "/sys", []string{"lo"}),
		Battery:    NewBatteryCollector("/sys", "BAT1"),
		Docker:     NewDockerCollector("/var/run/docker.sock"),
		Sensors:    NewSensorsCollector("/sys", "card1"),
		Processes:  NewProcessesCollector("/proc", 10),
		System:     NewSystemCollector("/proc"),
		GPUName:    "AMD Vega 8",
		SmartTTL:   60 * time.Second,
		SensorsTTL: 1 * time.Second,
	}
	return NewSnapshotter(cfg)
}

func TestSnapshotterPopulatesAllSubtreesAgainstLive(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no live /proc")
	}
	s := buildLiveSnapshotter(t)
	snap, _ := s.Snapshot()

	if snap.Timestamp.IsZero() {
		t.Error("Timestamp must be set")
	}
	if snap.CPU.FreqMHzAvg == 0 {
		t.Error("CPU.FreqMHzAvg must be > 0 on live host")
	}
	if snap.Memory.TotalBytes == 0 {
		t.Error("Memory.TotalBytes must be > 0")
	}
	if snap.System.UptimeSeconds == 0 {
		t.Error("System.UptimeSeconds must be > 0")
	}
	if len(snap.Networks) == 0 {
		t.Error("Networks must contain at least the configured iface (lo)")
	}
	if len(snap.TopProcesses) == 0 {
		t.Error("TopProcesses must contain at least one process")
	}
}

func TestSnapshotterCachesSmartctlPerDevice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stat"), loadFixture(t, "proc/stat_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "diskstats"), loadFixture(t, "proc/diskstats_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), loadFixture(t, "proc/meminfo"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uptime"), []byte("1.0 1.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte("0 0 0 1/1 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "net", "dev"), loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
		t.Fatal(err)
	}

	mp := t.TempDir()
	var smartCalls int64
	smartFn := func(device string) float64 {
		atomic.AddInt64(&smartCalls, 1)
		return 42.0
	}
	cfg := SnapshotterConfig{
		CPU:          NewCPUCollector(dir, "testdata/sys"),
		Memory:       NewMemoryCollector(dir),
		Disk:         NewDiskCollector(dir, []DiskTarget{{Mountpoint: mp, Device: "/dev/sdb", DiskstatName: "sdb"}}),
		Network:      NewNetworkCollector(dir, "testdata/sys", []string{"enp1s0f1"}),
		Battery:      NewBatteryCollector(t.TempDir(), "BAT1"),
		Docker:       NewDockerCollector(filepath.Join(t.TempDir(), "no.sock")),
		Sensors:      NewSensorsCollector("testdata/sys", "card1"),
		Processes:    NewProcessesCollector(t.TempDir(), 5),
		System:       NewSystemCollector(dir),
		GPUName:      "test",
		SmartTTL:     10 * time.Second,
		SensorsTTL:   10 * time.Second,
		SmartctlExec: smartFn,
	}
	s := NewSnapshotter(cfg)

	for i := 0; i < 5; i++ {
		s.Snapshot()
	}
	if got := atomic.LoadInt64(&smartCalls); got != 1 {
		t.Errorf("smartctl called %d times in 5 snapshots within TTL, want 1", got)
	}
}

func TestSnapshotterRefetchesSmartctlAfterTTL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []struct {
		name, content string
	}{
		{"stat", string(loadFixture(t, "proc/stat_t1"))},
		{"diskstats", string(loadFixture(t, "proc/diskstats_t1"))},
		{"meminfo", string(loadFixture(t, "proc/meminfo"))},
		{"uptime", "1.0 1.0\n"},
		{"loadavg", "0 0 0 1/1 1\n"},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "net", "dev"), loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
		t.Fatal(err)
	}

	var smartCalls int64
	smartFn := func(device string) float64 {
		atomic.AddInt64(&smartCalls, 1)
		return 42.0
	}
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	mp := t.TempDir()
	cfg := SnapshotterConfig{
		CPU:          NewCPUCollector(dir, "testdata/sys"),
		Memory:       NewMemoryCollector(dir),
		Disk:         NewDiskCollector(dir, []DiskTarget{{Mountpoint: mp, Device: "/dev/sdb", DiskstatName: "sdb"}}),
		Network:      NewNetworkCollector(dir, "testdata/sys", []string{"enp1s0f1"}),
		Battery:      NewBatteryCollector(t.TempDir(), "BAT1"),
		Docker:       NewDockerCollector(filepath.Join(t.TempDir(), "no.sock")),
		Sensors:      NewSensorsCollector("testdata/sys", "card1"),
		Processes:    NewProcessesCollector(t.TempDir(), 5),
		System:       NewSystemCollector(dir),
		GPUName:      "test",
		SmartTTL:     60 * time.Second,
		SensorsTTL:   1 * time.Second,
		SmartctlExec: smartFn,
	}
	s := NewSnapshotter(cfg)
	s.now = clock

	s.Snapshot()
	now = now.Add(30 * time.Second)
	s.Snapshot()
	if got := atomic.LoadInt64(&smartCalls); got != 1 {
		t.Errorf("after 30s: smartCalls = %d, want 1 (still within TTL)", got)
	}
	now = now.Add(31 * time.Second) // total = 61s, past TTL
	s.Snapshot()
	if got := atomic.LoadInt64(&smartCalls); got != 2 {
		t.Errorf("after 61s: smartCalls = %d, want 2 (TTL elapsed)", got)
	}
}

func TestSnapshotterCachesSensors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []struct {
		name, content string
	}{
		{"stat", string(loadFixture(t, "proc/stat_t1"))},
		{"diskstats", string(loadFixture(t, "proc/diskstats_t1"))},
		{"meminfo", string(loadFixture(t, "proc/meminfo"))},
		{"uptime", "1.0 1.0\n"},
		{"loadavg", "0 0 0 1/1 1\n"},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "net", "dev"), loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
		t.Fatal(err)
	}

	var sensorsCalls int64
	sensorsFn := func() (SensorsResult, error) {
		atomic.AddInt64(&sensorsCalls, 1)
		return SensorsResult{CPUTempCelsius: 50, GPUTempCelsius: 45, GPUBusyPercent: 10}, nil
	}
	cfg := SnapshotterConfig{
		CPU:         NewCPUCollector(dir, "testdata/sys"),
		Memory:      NewMemoryCollector(dir),
		Disk:        NewDiskCollector(dir, nil),
		Network:     NewNetworkCollector(dir, "testdata/sys", []string{"enp1s0f1"}),
		Battery:     NewBatteryCollector(t.TempDir(), "BAT1"),
		Docker:      NewDockerCollector(filepath.Join(t.TempDir(), "no.sock")),
		Sensors:     NewSensorsCollector("testdata/sys", "card1"),
		Processes:   NewProcessesCollector(t.TempDir(), 5),
		System:      NewSystemCollector(dir),
		GPUName:     "AMD Vega 8",
		SmartTTL:    10 * time.Second,
		SensorsTTL:  10 * time.Second,
		SensorsExec: sensorsFn,
	}
	s := NewSnapshotter(cfg)

	for i := 0; i < 5; i++ {
		snap, _ := s.Snapshot()
		if snap.GPU == nil {
			t.Error("GPU must be set when sensors return data")
			break
		}
		if snap.GPU.TempCelsius != 45 {
			t.Errorf("GPU.TempCelsius = %f, want 45", snap.GPU.TempCelsius)
		}
		if snap.CPU.TempCelsius != 50 {
			t.Errorf("CPU.TempCelsius = %f, want 50", snap.CPU.TempCelsius)
		}
	}
	if got := atomic.LoadInt64(&sensorsCalls); got != 1 {
		t.Errorf("sensors called %d times in 5 snapshots within TTL, want 1", got)
	}
}

func TestSnapshotterAccumulatesErrors(t *testing.T) {
	t.Parallel()
	// CPU collector pointed at empty dir → error.
	cfg := SnapshotterConfig{
		CPU:        NewCPUCollector(t.TempDir(), "testdata/sys"),
		Memory:     NewMemoryCollector(t.TempDir()),
		Disk:       NewDiskCollector(t.TempDir(), nil),
		Network:    NewNetworkCollector(t.TempDir(), "testdata/sys", nil),
		Battery:    NewBatteryCollector(t.TempDir(), "BAT1"),
		Docker:     NewDockerCollector(filepath.Join(t.TempDir(), "no.sock")),
		Sensors:    NewSensorsCollector("testdata/sys", "card99"),
		Processes:  NewProcessesCollector(t.TempDir(), 5),
		System:     NewSystemCollector(t.TempDir()),
		GPUName:    "test",
		SmartTTL:   time.Second,
		SensorsTTL: time.Second,
	}
	s := NewSnapshotter(cfg)
	snap, err := s.Snapshot()
	if err == nil {
		t.Error("expected non-nil error from broken sub-collectors")
	}
	if snap.Timestamp.IsZero() {
		t.Error("snapshot must still have timestamp even when sub-collectors fail")
	}
}
