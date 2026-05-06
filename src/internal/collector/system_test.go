package collector

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParseUptimeFromFixture(t *testing.T) {
	t.Parallel()
	got, err := parseUptime(loadFixture(t, "proc/uptime"))
	if err != nil {
		t.Fatalf("parseUptime: %v", err)
	}
	// Fixture captured at ~17157s uptime; allow slack but reject anything
	// implausible.
	if got < 1000 || got > 1_000_000 {
		t.Errorf("uptime = %d, out of plausible range", got)
	}
}

func TestParseUptimeRejectsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := parseUptime([]byte("")); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestParseUptimeRejectsMalformed(t *testing.T) {
	t.Parallel()
	if _, err := parseUptime([]byte("not a number\n")); err == nil {
		t.Error("expected error on non-numeric")
	}
}

func TestParseLoadavgFromFixture(t *testing.T) {
	t.Parallel()
	la, err := parseLoadavg(loadFixture(t, "proc/loadavg"))
	if err != nil {
		t.Fatalf("parseLoadavg: %v", err)
	}
	for _, v := range []float64{la.load1m, la.load5m, la.load15m} {
		if v < 0 || v > 100 {
			t.Errorf("load avg out of range: %v", la)
		}
	}
}

func TestParseLoadavgRejectsTooFewFields(t *testing.T) {
	t.Parallel()
	if _, err := parseLoadavg([]byte("1.0 2.0\n")); err == nil {
		t.Error("expected error: only 2 fields")
	}
}

func TestCountProcessesIgnoresNonNumeric(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, pid := range []int{1, 2, 100, 99999} {
		if err := os.MkdirAll(filepath.Join(dir, strconv.Itoa(pid)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"self", "sys", "net", "kmsg"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	n, err := countProcesses(dir)
	if err != nil {
		t.Fatalf("countProcesses: %v", err)
	}
	if n != 4 {
		t.Errorf("got %d, want 4", n)
	}
}

func TestSystemCollectorCollectFromTempDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uptime"), []byte("12345.67 99999.00\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte("0.50 0.75 1.00 3/200 5000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{1, 2, 3} {
		if err := os.MkdirAll(filepath.Join(dir, strconv.Itoa(pid)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	c := NewSystemCollector(dir)
	sys, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if sys.UptimeSeconds != 12345 {
		t.Errorf("UptimeSeconds = %d, want 12345 (truncated)", sys.UptimeSeconds)
	}
	if math.Abs(sys.LoadAvg1m-0.50) > 0.001 {
		t.Errorf("LoadAvg1m = %f, want 0.50", sys.LoadAvg1m)
	}
	if math.Abs(sys.LoadAvg5m-0.75) > 0.001 {
		t.Errorf("LoadAvg5m = %f, want 0.75", sys.LoadAvg5m)
	}
	if math.Abs(sys.LoadAvg15m-1.00) > 0.001 {
		t.Errorf("LoadAvg15m = %f, want 1.00", sys.LoadAvg15m)
	}
	if sys.ProcessesCount != 3 {
		t.Errorf("ProcessesCount = %d, want 3", sys.ProcessesCount)
	}
}

func TestSystemCollectorErrorsOnMissingUptime(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = bytes.NewReader(nil)
	if err := os.WriteFile(filepath.Join(dir, "loadavg"), []byte("0 0 0 1/1 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewSystemCollector(dir)
	if _, err := c.Collect(); err == nil {
		t.Error("expected error: uptime missing")
	}
}

func TestSystemCollectorAgainstLiveProc(t *testing.T) {
	if _, err := os.Stat("/proc/uptime"); err != nil {
		t.Skip("no live /proc")
	}
	c := NewSystemCollector("/proc")
	sys, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if sys.UptimeSeconds == 0 {
		t.Error("live UptimeSeconds == 0")
	}
	if sys.ProcessesCount == 0 {
		t.Error("live ProcessesCount == 0")
	}
	if sys.LoadAvg1m < 0 {
		t.Errorf("LoadAvg1m negative: %f", sys.LoadAvg1m)
	}
}
