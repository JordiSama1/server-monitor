package collector

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMemInfoFromFixture(t *testing.T) {
	mem, err := parseMemInfo(bytes.NewReader(loadFixture(t, "proc/meminfo")))
	if err != nil {
		t.Fatalf("parseMemInfo: %v", err)
	}
	const KiB = uint64(1024)
	want := map[string]uint64{
		"TotalBytes":     10116812 * KiB,
		"AvailableBytes": 5462740 * KiB,
		"CachedBytes":    4199992 * KiB,
		"BuffersBytes":   158040 * KiB,
		"SwapTotalBytes": 4194300 * KiB,
		"SwapUsedBytes":  0,
	}
	got := map[string]uint64{
		"TotalBytes":     mem.TotalBytes,
		"AvailableBytes": mem.AvailableBytes,
		"CachedBytes":    mem.CachedBytes,
		"BuffersBytes":   mem.BuffersBytes,
		"SwapTotalBytes": mem.SwapTotalBytes,
		"SwapUsedBytes":  mem.SwapUsedBytes,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	wantUsed := (uint64(10116812) - 5462740) * KiB
	if mem.UsedBytes != wantUsed {
		t.Errorf("UsedBytes = %d, want %d", mem.UsedBytes, wantUsed)
	}
}

func TestParseMemInfoMissingTotalErrors(t *testing.T) {
	t.Parallel()
	_, err := parseMemInfo(bytes.NewReader([]byte("MemFree: 100 kB\n")))
	if err == nil {
		t.Error("expected error when MemTotal is missing")
	}
}

func TestParseMemInfoTolerantOfUnknownLines(t *testing.T) {
	t.Parallel()
	input := "MemTotal: 1024 kB\nMemFree: 256 kB\nMemAvailable: 512 kB\nFooBar: garbage\n"
	mem, err := parseMemInfo(bytes.NewReader([]byte(input)))
	if err != nil {
		t.Fatalf("parseMemInfo: %v", err)
	}
	if mem.TotalBytes != 1024*1024 {
		t.Errorf("TotalBytes = %d", mem.TotalBytes)
	}
}

func TestMemoryCollectorReadsFixturePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), loadFixture(t, "proc/meminfo"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewMemoryCollector(dir)
	mem, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if mem.TotalBytes == 0 || mem.AvailableBytes == 0 {
		t.Error("totals must be populated")
	}
}

func TestMemoryCollectorErrorsOnMissingMeminfo(t *testing.T) {
	t.Parallel()
	c := NewMemoryCollector(t.TempDir())
	if _, err := c.Collect(); err == nil {
		t.Error("expected error for missing meminfo")
	}
}

func TestMemoryCollectorAgainstLiveProc(t *testing.T) {
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("no live /proc/meminfo available")
	}
	c := NewMemoryCollector("/proc")
	mem, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if mem.TotalBytes < 256*1024*1024 {
		t.Errorf("live TotalBytes = %d, suspiciously small", mem.TotalBytes)
	}
	if mem.AvailableBytes > mem.TotalBytes {
		t.Errorf("AvailableBytes %d > TotalBytes %d", mem.AvailableBytes, mem.TotalBytes)
	}
	if mem.UsedBytes+mem.AvailableBytes != mem.TotalBytes {
		t.Errorf("Used+Available = %d, TotalBytes = %d", mem.UsedBytes+mem.AvailableBytes, mem.TotalBytes)
	}
}
