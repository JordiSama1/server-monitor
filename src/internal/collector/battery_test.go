package collector

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestBatteryCollectorChargeBased(t *testing.T) {
	t.Parallel()
	c := NewBatteryCollector("testdata/sys", "BAT1")
	bat, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if bat == nil {
		t.Fatal("expected non-nil battery")
	}
	if bat.Name != "BAT1" {
		t.Errorf("Name = %q, want BAT1", bat.Name)
	}
	if bat.Percent != 100 {
		t.Errorf("Percent = %f, want 100", bat.Percent)
	}
	if bat.Status != "Full" {
		t.Errorf("Status = %q, want Full", bat.Status)
	}
	// Charge-based conversion: charge_full(3482000 µAh) * voltage_now
	// (8102000 µV) / 1e12 ≈ 28.21 Wh.
	wantFull := float64(3482000) * float64(8102000) / 1e12
	if math.Abs(bat.EnergyFullWh-wantFull) > 0.01 {
		t.Errorf("EnergyFullWh = %f, want ≈ %f", bat.EnergyFullWh, wantFull)
	}
	wantDesign := float64(4870000) * float64(8102000) / 1e12
	if math.Abs(bat.EnergyDesignWh-wantDesign) > 0.01 {
		t.Errorf("EnergyDesignWh = %f, want ≈ %f", bat.EnergyDesignWh, wantDesign)
	}
	// Capacity health = charge_full / charge_full_design × 100 ≈ 71.5%.
	wantHealth := float64(3482000) / float64(4870000) * 100.0
	if math.Abs(bat.CapacityHealthPercent-wantHealth) > 0.01 {
		t.Errorf("CapacityHealthPercent = %f, want ≈ %f", bat.CapacityHealthPercent, wantHealth)
	}
}

func TestBatteryCollectorEnergyBased(t *testing.T) {
	t.Parallel()
	c := NewBatteryCollector("testdata/sys", "BAT_ENERGY")
	bat, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if bat == nil {
		t.Fatal("expected non-nil battery")
	}
	if bat.Percent != 75 {
		t.Errorf("Percent = %f, want 75", bat.Percent)
	}
	if bat.Status != "Discharging" {
		t.Errorf("Status = %q, want Discharging", bat.Status)
	}
	// Energy-based: energy_now is in µWh, so 27000000 → 27 Wh.
	if math.Abs(bat.EnergyNowWh-27.0) > 0.001 {
		t.Errorf("EnergyNowWh = %f, want 27", bat.EnergyNowWh)
	}
	if math.Abs(bat.EnergyFullWh-36.0) > 0.001 {
		t.Errorf("EnergyFullWh = %f, want 36", bat.EnergyFullWh)
	}
	if math.Abs(bat.EnergyDesignWh-44.0) > 0.001 {
		t.Errorf("EnergyDesignWh = %f, want 44", bat.EnergyDesignWh)
	}
	wantHealth := 36.0 / 44.0 * 100.0
	if math.Abs(bat.CapacityHealthPercent-wantHealth) > 0.01 {
		t.Errorf("CapacityHealthPercent = %f, want ≈ %f", bat.CapacityHealthPercent, wantHealth)
	}
}

func TestBatteryCollectorReturnsNilWhenAbsent(t *testing.T) {
	t.Parallel()
	c := NewBatteryCollector(t.TempDir(), "BAT1")
	bat, err := c.Collect()
	if err != nil {
		t.Errorf("absent battery should not error, got: %v", err)
	}
	if bat != nil {
		t.Errorf("absent battery should return nil, got %+v", bat)
	}
}

func TestBatteryCollectorMinimalSysfs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bdir := filepath.Join(dir, "class", "power_supply", "BAT1")
	if err := os.MkdirAll(bdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "capacity"), []byte("42\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "status"), []byte("Charging\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewBatteryCollector(dir, "BAT1")
	bat, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if bat == nil {
		t.Fatal("expected non-nil battery")
	}
	if bat.Percent != 42 || bat.Status != "Charging" {
		t.Errorf("got %+v, want Percent=42 Status=Charging", bat)
	}
	// Energy fields default to 0 when neither energy_* nor charge_* present.
	if bat.EnergyNowWh != 0 || bat.EnergyFullWh != 0 || bat.EnergyDesignWh != 0 {
		t.Errorf("energy fields must be 0 with no source: %+v", bat)
	}
}

func TestBatteryCollectorAgainstLiveSys(t *testing.T) {
	if _, err := os.Stat("/sys/class/power_supply/BAT1"); err != nil {
		t.Skip("no live BAT1")
	}
	c := NewBatteryCollector("/sys", "BAT1")
	bat, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if bat == nil {
		t.Fatal("live BAT1 should not return nil")
	}
	if bat.Percent < 0 || bat.Percent > 100 {
		t.Errorf("live Percent out of range: %f", bat.Percent)
	}
	if bat.CapacityHealthPercent < 0 || bat.CapacityHealthPercent > 100 {
		t.Errorf("live health out of range: %f", bat.CapacityHealthPercent)
	}
}
