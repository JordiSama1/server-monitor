package collector

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseDiskstatsLineFound(t *testing.T) {
	data := loadFixture(t, "proc/diskstats_t1")
	got, err := parseDiskstats(bytes.NewReader(data), "sdb")
	if err != nil {
		t.Fatalf("parseDiskstats: %v", err)
	}
	if got.sectorsRead != 2046174 {
		t.Errorf("sectorsRead = %d, want 2046174", got.sectorsRead)
	}
	if got.sectorsWritten != 12222306 {
		t.Errorf("sectorsWritten = %d, want 12222306", got.sectorsWritten)
	}
}

func TestParseDiskstatsLineNotFound(t *testing.T) {
	t.Parallel()
	data := loadFixture(t, "proc/diskstats_t1")
	if _, err := parseDiskstats(bytes.NewReader(data), "nope"); err == nil {
		t.Error("expected error for missing device")
	}
}

func TestParseSmartctlTempFromTemperatureField(t *testing.T) {
	t.Parallel()
	temp, err := parseSmartctlTemp(loadFixture(t, "smartctl_temp_field.json"))
	if err != nil {
		t.Fatalf("parseSmartctlTemp: %v", err)
	}
	if temp != 38.0 {
		t.Errorf("temp = %f, want 38", temp)
	}
}

func TestParseSmartctlTempFromAttribute194(t *testing.T) {
	t.Parallel()
	temp, err := parseSmartctlTemp(loadFixture(t, "smartctl_attr194.json"))
	if err != nil {
		t.Fatalf("parseSmartctlTemp: %v", err)
	}
	if temp != 42.0 {
		t.Errorf("temp = %f, want 42", temp)
	}
}

func TestParseSmartctlTempReturnsErrorOnPermissionDenied(t *testing.T) {
	t.Parallel()
	if _, err := parseSmartctlTemp(loadFixture(t, "smartctl_perm_denied.json")); err == nil {
		t.Error("expected error from permission-denied output")
	}
}

func TestParseSmartctlTempRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseSmartctlTemp([]byte("not json")); err == nil {
		t.Error("expected error from invalid JSON")
	}
}

func TestStatfsBytesAgainstTempDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	total, avail, err := statfsBytes(dir)
	if err != nil {
		t.Fatalf("statfsBytes: %v", err)
	}
	if total == 0 {
		t.Error("total bytes must be > 0")
	}
	if avail > total {
		t.Errorf("available %d > total %d", avail, total)
	}
}

func TestStatfsBytesMissingPath(t *testing.T) {
	t.Parallel()
	if _, _, err := statfsBytes("/nonexistent/path/does/not/exist"); err == nil {
		t.Error("expected error for missing path")
	}
}

func TestDiskCollectorFirstCallReturnsZeroIO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "diskstats"), loadFixture(t, "proc/diskstats_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	mp := t.TempDir()
	c := NewDiskCollector(dir, []DiskTarget{
		{Mountpoint: mp, Device: "/dev/sdb", DiskstatName: "sdb"},
	})
	disks, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(disks) != 1 {
		t.Fatalf("disks len = %d, want 1", len(disks))
	}
	d := disks[0]
	if d.Mountpoint != mp || d.Device != "/dev/sdb" {
		t.Errorf("mountpoint/device mismatch: %+v", d)
	}
	if d.IOReadBytesPerSec != 0 || d.IOWriteBytesPerSec != 0 {
		t.Errorf("first-call IO must be 0, got read=%f write=%f",
			d.IOReadBytesPerSec, d.IOWriteBytesPerSec)
	}
	if d.TotalBytes == 0 {
		t.Error("TotalBytes must be populated from statfs")
	}
}

func TestDiskCollectorComputesIORateAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	stats := filepath.Join(dir, "diskstats")
	if err := os.WriteFile(stats, loadFixture(t, "proc/diskstats_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	mp := t.TempDir()
	c := NewDiskCollector(dir, []DiskTarget{
		{Mountpoint: mp, Device: "/dev/sdb", DiskstatName: "sdb"},
	})
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(stats, loadFixture(t, "proc/diskstats_t2"), 0644); err != nil {
		t.Fatal(err)
	}
	disks, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	d := disks[0]
	if d.IOReadBytesPerSec <= 0 {
		t.Errorf("IOReadBytesPerSec = %f, want > 0", d.IOReadBytesPerSec)
	}
	if d.IOWriteBytesPerSec <= 0 {
		t.Errorf("IOWriteBytesPerSec = %f, want > 0", d.IOWriteBytesPerSec)
	}
}

func TestDiskCollectorErrorsOnMissingDiskstats(t *testing.T) {
	t.Parallel()
	c := NewDiskCollector(t.TempDir(), []DiskTarget{
		{Mountpoint: "/", Device: "/dev/sdb", DiskstatName: "sdb"},
	})
	if _, err := c.Collect(); err == nil {
		t.Error("expected error for missing /proc/diskstats")
	}
}

func TestDiskCollectorAgainstLiveProc(t *testing.T) {
	if _, err := os.Stat("/proc/diskstats"); err != nil {
		t.Skip("no live /proc/diskstats available")
	}
	c := NewDiskCollector("/proc", []DiskTarget{
		{Mountpoint: "/", Device: "/dev/sdb", DiskstatName: "sdb"},
	})
	disks, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if len(disks) != 1 || disks[0].TotalBytes == 0 {
		t.Errorf("live disk = %+v", disks)
	}
	if disks[0].UsedPercent < 0 || disks[0].UsedPercent > 100 {
		t.Errorf("UsedPercent out of range: %f", disks[0].UsedPercent)
	}
}
