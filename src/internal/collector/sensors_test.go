package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jordisama/server-monitor/internal/model"
)

func TestParseSensorsAMD(t *testing.T) {
	t.Parallel()
	r, err := parseSensors(loadFixture(t, "sensors_real.json"))
	if err != nil {
		t.Fatalf("parseSensors: %v", err)
	}
	if r.CPUTempCelsius != 43.0 {
		t.Errorf("CPUTempCelsius = %f, want 43", r.CPUTempCelsius)
	}
	if r.GPUTempCelsius != 43.0 {
		t.Errorf("GPUTempCelsius = %f, want 43", r.GPUTempCelsius)
	}
	if len(r.PerCoreTemps) != 0 {
		t.Errorf("AMD k10temp has no per-core temps, got %d", len(r.PerCoreTemps))
	}
}

func TestParseSensorsIntel(t *testing.T) {
	t.Parallel()
	r, err := parseSensors(loadFixture(t, "sensors_intel.json"))
	if err != nil {
		t.Fatalf("parseSensors: %v", err)
	}
	if r.CPUTempCelsius != 55.0 {
		t.Errorf("CPUTempCelsius = %f, want 55 (Package id 0)", r.CPUTempCelsius)
	}
	if r.GPUTempCelsius != 48.0 {
		t.Errorf("GPUTempCelsius = %f, want 48 (nouveau)", r.GPUTempCelsius)
	}
	want := []float64{53.0, 54.0, 56.0, 55.0}
	if len(r.PerCoreTemps) != len(want) {
		t.Fatalf("PerCoreTemps len = %d, want %d", len(r.PerCoreTemps), len(want))
	}
	for i := range want {
		if r.PerCoreTemps[i] != want[i] {
			t.Errorf("PerCoreTemps[%d] = %f, want %f", i, r.PerCoreTemps[i], want[i])
		}
	}
}

func TestParseSensorsEmptyJSON(t *testing.T) {
	t.Parallel()
	r, err := parseSensors([]byte("{}"))
	if err != nil {
		t.Fatalf("parseSensors: %v", err)
	}
	if r.CPUTempCelsius != 0 || r.GPUTempCelsius != 0 || len(r.PerCoreTemps) != 0 {
		t.Errorf("empty input should yield zero values, got %+v", r)
	}
}

func TestParseSensorsInvalidJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseSensors([]byte("not json")); err == nil {
		t.Error("expected error from invalid JSON")
	}
}

func TestReadGPUBusyPercentFromFixture(t *testing.T) {
	t.Parallel()
	pct, err := readGPUBusyPercent("testdata/sys", "card1")
	if err != nil {
		t.Fatalf("readGPUBusyPercent: %v", err)
	}
	if pct != 0 {
		t.Errorf("got %f, want 0 (idle)", pct)
	}
}

func TestReadGPUBusyPercentMissingCard(t *testing.T) {
	t.Parallel()
	if _, err := readGPUBusyPercent("testdata/sys", "card99"); err == nil {
		t.Error("expected error for missing card")
	}
}

func TestApplySensorsToCPU(t *testing.T) {
	t.Parallel()
	cpu := model.CPU{
		PerCore: []model.CoreCPU{
			{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3},
		},
	}
	r := SensorsResult{
		CPUTempCelsius: 60.0,
		PerCoreTemps:   nil, // AMD case
	}
	cpu = applySensorsToCPU(cpu, r)
	if cpu.TempCelsius != 60 {
		t.Errorf("CPU.TempCelsius = %f, want 60", cpu.TempCelsius)
	}
	for i, c := range cpu.PerCore {
		if c.TempCelsius != 60 {
			t.Errorf("PerCore[%d].TempCelsius = %f, want 60 (broadcast)", i, c.TempCelsius)
		}
	}
}

func TestApplySensorsToCPUIntelPerCore(t *testing.T) {
	t.Parallel()
	cpu := model.CPU{
		PerCore: []model.CoreCPU{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}},
	}
	r := SensorsResult{
		CPUTempCelsius: 55.0,
		PerCoreTemps:   []float64{53, 54, 56, 55},
	}
	cpu = applySensorsToCPU(cpu, r)
	wantPerCore := []float64{53, 54, 56, 55}
	for i := range wantPerCore {
		if cpu.PerCore[i].TempCelsius != wantPerCore[i] {
			t.Errorf("PerCore[%d] = %f, want %f", i, cpu.PerCore[i].TempCelsius, wantPerCore[i])
		}
	}
}

func TestSensorsCollectorAgainstLiveBinaries(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sensors"); err != nil {
		t.Skip("no sensors binary")
	}
	c := NewSensorsCollector("/sys", "card1")
	r, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	// Best-effort assertions: temps should be plausibly above 0, below 110.
	if r.CPUTempCelsius < 0 || r.CPUTempCelsius > 110 {
		t.Errorf("live CPU temp = %f, out of plausible range", r.CPUTempCelsius)
	}
	if r.GPUBusyPercent < 0 || r.GPUBusyPercent > 100 {
		t.Errorf("live GPU busy %% = %f, out of range", r.GPUBusyPercent)
	}
}

func TestSensorsCollectorMissingSensorsBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := &SensorsCollector{
		sysPath:     dir,
		gpuCard:     "card1",
		sensorsPath: filepath.Join(dir, "no-such-sensors"),
	}
	r, err := c.Collect()
	if err != nil {
		t.Errorf("missing sensors binary should not error, got: %v", err)
	}
	// Without sensors output, temps stay at zero; that's allowed.
	if r.CPUTempCelsius != 0 || r.GPUTempCelsius != 0 {
		t.Errorf("temps should be 0 with missing binary, got %+v", r)
	}
}
