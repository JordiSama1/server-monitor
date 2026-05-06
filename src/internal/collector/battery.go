package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jordisama/server-monitor/internal/model"
)

func readBatteryString(dir, name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

func readBatteryUint(dir, name string) (uint64, bool) {
	s, ok := readBatteryString(dir, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readBatteryFloat(dir, name string) (float64, bool) {
	s, ok := readBatteryString(dir, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// BatteryCollector lee /sys/class/power_supply/<name>/* directamente
// (sin upower) y devuelve un *model.Battery, o nil si la batería no
// existe en este sistema (caso typical en un server fijo de escritorio).
//
// Soporta dos formatos de sysfs:
//   - Energy-based (energy_now/full/design en µWh, ej. ThinkPad).
//   - Charge-based (charge_now/full/design en µAh + voltage_now en µV,
//     ej. Acer Aspire). Convierte multiplicando por voltage_now.
type BatteryCollector struct {
	sysPath string
	name    string
}

// NewBatteryCollector returns a collector for the named battery device
// under sysPath/class/power_supply/. Pass /sys + the BATTERY_NAME env
// (typically BAT0 or BAT1) for live use.
func NewBatteryCollector(sysPath, name string) *BatteryCollector {
	return &BatteryCollector{sysPath: sysPath, name: name}
}

// Collect returns the current battery snapshot. If the battery directory
// doesn't exist, returns (nil, nil) rather than an error: a missing
// battery is a normal state for a desktop/server, not a failure.
func (c *BatteryCollector) Collect() (*model.Battery, error) {
	dir := filepath.Join(c.sysPath, "class", "power_supply", c.name)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	bat := &model.Battery{Name: c.name}
	if pct, ok := readBatteryFloat(dir, "capacity"); ok {
		bat.Percent = pct
	}
	if status, ok := readBatteryString(dir, "status"); ok {
		bat.Status = status
	}

	if v, ok := readBatteryUint(dir, "energy_now"); ok {
		bat.EnergyNowWh = float64(v) / 1e6
	}
	if v, ok := readBatteryUint(dir, "energy_full"); ok {
		bat.EnergyFullWh = float64(v) / 1e6
	}
	if v, ok := readBatteryUint(dir, "energy_full_design"); ok {
		bat.EnergyDesignWh = float64(v) / 1e6
	}

	// Charge-based fallback: only fill if energy_* didn't populate.
	voltageUV, hasVoltage := readBatteryUint(dir, "voltage_now")
	if hasVoltage {
		voltageV := float64(voltageUV) / 1e6
		if bat.EnergyNowWh == 0 {
			if chargeUAh, ok := readBatteryUint(dir, "charge_now"); ok {
				bat.EnergyNowWh = float64(chargeUAh) / 1e6 * voltageV
			}
		}
		if bat.EnergyFullWh == 0 {
			if chargeUAh, ok := readBatteryUint(dir, "charge_full"); ok {
				bat.EnergyFullWh = float64(chargeUAh) / 1e6 * voltageV
			}
		}
		if bat.EnergyDesignWh == 0 {
			if chargeUAh, ok := readBatteryUint(dir, "charge_full_design"); ok {
				bat.EnergyDesignWh = float64(chargeUAh) / 1e6 * voltageV
			}
		}
	}

	if bat.EnergyDesignWh > 0 && bat.EnergyFullWh > 0 {
		bat.CapacityHealthPercent = bat.EnergyFullWh / bat.EnergyDesignWh * 100.0
	}
	return bat, nil
}
