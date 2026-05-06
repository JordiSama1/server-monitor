package collector

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCPUStat(t *testing.T) {
	snap, err := parseCPUStat(bytes.NewReader(loadFixture(t, "proc/stat_t1")))
	if err != nil {
		t.Fatalf("parseCPUStat: %v", err)
	}
	if len(snap.cores) != 8 {
		t.Fatalf("cores len = %d, want 8", len(snap.cores))
	}
	want0 := cpuTimes{
		user: 167149, nice: 0, system: 62588, idle: 1223289,
		iowait: 692, irq: 0, softirq: 559, steal: 0,
	}
	if snap.cores[0] != want0 {
		t.Errorf("cores[0] = %+v, want %+v", snap.cores[0], want0)
	}
	wantAgg := cpuTimes{
		user: 1305551, nice: 17, system: 489545, idle: 9897040,
		iowait: 5742, irq: 0, softirq: 6006, steal: 0,
	}
	if snap.overall != wantAgg {
		t.Errorf("overall = %+v, want %+v", snap.overall, wantAgg)
	}
}

func TestParseCPUStatRejectsInputWithoutAggregate(t *testing.T) {
	_, err := parseCPUStat(bytes.NewReader([]byte("intr 1 2 3\nctxt 999\n")))
	if err == nil {
		t.Error("expected error when no aggregate cpu line present")
	}
}

func TestComputePercentInRange(t *testing.T) {
	s1, _ := parseCPUStat(bytes.NewReader(loadFixture(t, "proc/stat_t1")))
	s2, _ := parseCPUStat(bytes.NewReader(loadFixture(t, "proc/stat_t2")))
	overall := computePercent(s1.overall, s2.overall)
	if overall < 0 || overall > 100 {
		t.Errorf("overall = %f, want [0,100]", overall)
	}
	for i := range s2.cores {
		p := computePercent(s1.cores[i], s2.cores[i])
		if p < 0 || p > 100 {
			t.Errorf("core %d = %f, want [0,100]", i, p)
		}
	}
}

func TestComputePercentZeroDelta(t *testing.T) {
	t.Parallel()
	same := cpuTimes{user: 100, system: 50, idle: 1000}
	if p := computePercent(same, same); p != 0 {
		t.Errorf("zero delta = %f, want 0", p)
	}
}

func TestComputePercentSaturatesOnReverseCounter(t *testing.T) {
	t.Parallel()
	older := cpuTimes{user: 200, idle: 1000}
	newer := cpuTimes{user: 100, idle: 1000}
	if p := computePercent(older, newer); p != 0 {
		t.Errorf("reverse delta = %f, want 0", p)
	}
}

func TestReadFreqMHzAllCores(t *testing.T) {
	for i := 0; i < 8; i++ {
		f, err := readFreqMHz("testdata/sys", i)
		if err != nil {
			t.Fatalf("cpu%d: %v", i, err)
		}
		if f < 100 || f > 10000 {
			t.Errorf("cpu%d freq = %f MHz, out of plausible range", i, f)
		}
	}
}

func TestReadFreqMHzMissingCore(t *testing.T) {
	t.Parallel()
	if _, err := readFreqMHz("testdata/sys", 99); err == nil {
		t.Error("expected error for missing cpu99")
	}
}

func TestCollectFirstCallReturnsZeroPercent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stat"), loadFixture(t, "proc/stat_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewCPUCollector(dir, "testdata/sys")
	cpu, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cpu.OverallPercent != 0 {
		t.Errorf("first-call overall = %f, want 0", cpu.OverallPercent)
	}
	if len(cpu.PerCore) != 8 {
		t.Errorf("PerCore len = %d, want 8", len(cpu.PerCore))
	}
	for i, core := range cpu.PerCore {
		if core.ID != i {
			t.Errorf("PerCore[%d].ID = %d", i, core.ID)
		}
		if core.FreqMHz < 100 {
			t.Errorf("PerCore[%d].FreqMHz = %f", i, core.FreqMHz)
		}
	}
	if cpu.FreqMHzAvg < 100 {
		t.Errorf("FreqMHzAvg = %f", cpu.FreqMHzAvg)
	}
}

func TestCollectComputesDeltaAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	statFile := filepath.Join(dir, "stat")
	if err := os.WriteFile(statFile, loadFixture(t, "proc/stat_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewCPUCollector(dir, "testdata/sys")
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if err := os.WriteFile(statFile, loadFixture(t, "proc/stat_t2"), 0644); err != nil {
		t.Fatal(err)
	}
	cpu, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if cpu.OverallPercent < 0 || cpu.OverallPercent > 100 {
		t.Errorf("overall = %f", cpu.OverallPercent)
	}
	for i, core := range cpu.PerCore {
		if core.Percent < 0 || core.Percent > 100 {
			t.Errorf("core %d = %f", i, core.Percent)
		}
	}
}

func TestCollectErrorsOnMissingProcStat(t *testing.T) {
	c := NewCPUCollector(t.TempDir(), "testdata/sys")
	if _, err := c.Collect(); err == nil {
		t.Error("expected error for missing /proc/stat")
	}
}

func TestCollectAgainstLiveProcStat(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no live /proc/stat available")
	}
	c := NewCPUCollector("/proc", "/sys")
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first live Collect: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	cpu, err := c.Collect()
	if err != nil {
		t.Fatalf("second live Collect: %v", err)
	}
	if cpu.OverallPercent < 0 || cpu.OverallPercent > 100 {
		t.Errorf("live overall = %f", cpu.OverallPercent)
	}
	if len(cpu.PerCore) == 0 {
		t.Error("PerCore empty on live system")
	}
}
