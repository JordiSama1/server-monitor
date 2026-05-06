package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jordisama/server-monitor/internal/model"
)

// SensorsResult is the un-rendered output of the sensors collector.
// The orchestrator merges these values into model.CPU (TempCelsius +
// PerCore[].TempCelsius) and into a *model.GPU.
type SensorsResult struct {
	CPUTempCelsius float64
	PerCoreTemps   []float64 // empty for AMD k10temp; per-core for Intel coretemp
	GPUTempCelsius float64
	GPUBusyPercent float64
}

// parseSensors reads the JSON output of `sensors -j` and extracts CPU
// and GPU temperatures from the chips this monitor cares about. It is
// chip-aware (k10temp, coretemp, amdgpu, nouveau, nvidia) but tolerant:
// missing chips just leave the corresponding fields at zero.
func parseSensors(data []byte) (SensorsResult, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return SensorsResult{}, fmt.Errorf("decode sensors json: %w", err)
	}
	var r SensorsResult
	for chip, v := range raw {
		chipMap, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(chip, "k10temp"):
			if t, ok := readSensorValue(chipMap, "Tctl", "temp1_input"); ok {
				r.CPUTempCelsius = t
			}
		case strings.HasPrefix(chip, "coretemp"):
			if t, ok := readSensorValue(chipMap, "Package id 0", "temp1_input"); ok {
				r.CPUTempCelsius = t
			}
			r.PerCoreTemps = extractIntelCoreTemps(chipMap)
		case strings.HasPrefix(chip, "amdgpu"):
			if t, ok := readSensorValue(chipMap, "edge", "temp1_input"); ok {
				r.GPUTempCelsius = t
			}
		case strings.HasPrefix(chip, "nouveau") || strings.HasPrefix(chip, "nvidia"):
			if t, ok := readSensorValue(chipMap, "temp1", "temp1_input"); ok {
				r.GPUTempCelsius = t
			}
		}
	}
	return r, nil
}

func readSensorValue(chipMap map[string]any, group, key string) (float64, bool) {
	g, ok := chipMap[group].(map[string]any)
	if !ok {
		return 0, false
	}
	raw, ok := g[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// extractIntelCoreTemps walks an Intel coretemp chip and returns the
// per-core temperatures in core ID order. Each core's group key looks
// like "Core N", and its value field is named "tempK_input" where K
// varies. We pick the only temp*_input present in each group.
func extractIntelCoreTemps(chipMap map[string]any) []float64 {
	type coreEntry struct {
		id   int
		temp float64
	}
	var entries []coreEntry
	for groupName, groupVal := range chipMap {
		if !strings.HasPrefix(groupName, "Core ") {
			continue
		}
		idStr := strings.TrimPrefix(groupName, "Core ")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		grp, ok := groupVal.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range grp {
			if !strings.HasPrefix(k, "temp") || !strings.HasSuffix(k, "_input") {
				continue
			}
			temp, ok := v.(float64)
			if !ok {
				continue
			}
			entries = append(entries, coreEntry{id: id, temp: temp})
			break
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	out := make([]float64, len(entries))
	for i, e := range entries {
		out[i] = e.temp
	}
	return out
}

func readGPUBusyPercent(sysPath, card string) (float64, error) {
	p := filepath.Join(sysPath, "class", "drm", card, "device", "gpu_busy_percent")
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", p, err)
	}
	raw := strings.TrimSpace(string(data))
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s = %q: %w", p, raw, err)
	}
	return v, nil
}

// applySensorsToCPU merges sensor readings into a model.CPU. If the
// sensors source has per-core temperatures (Intel coretemp), those win.
// Otherwise the CPU package temperature is broadcast to every core as a
// pragmatic approximation — AMD k10temp doesn't expose per-core data, but
// per-core values being all-zero in the dashboard would be misleading.
func applySensorsToCPU(cpu model.CPU, r SensorsResult) model.CPU {
	cpu.TempCelsius = r.CPUTempCelsius
	if len(r.PerCoreTemps) > 0 {
		for i := range cpu.PerCore {
			if i < len(r.PerCoreTemps) {
				cpu.PerCore[i].TempCelsius = r.PerCoreTemps[i]
			}
		}
		return cpu
	}
	if r.CPUTempCelsius > 0 {
		for i := range cpu.PerCore {
			cpu.PerCore[i].TempCelsius = r.CPUTempCelsius
		}
	}
	return cpu
}

// SensorsCollector orquesta dos fuentes de temperatura:
//   - El binario `sensors -j` (lm-sensors) para CPU y GPU.
//   - /sys/class/drm/<card>/device/gpu_busy_percent para carga de GPU.
//
// Tolera la ausencia de cualquiera de las dos fuentes: temps faltantes
// quedan en cero y el resto del Result se entrega normal.
type SensorsCollector struct {
	sysPath     string
	gpuCard     string
	sensorsPath string
}

// NewSensorsCollector returns a collector reading lm-sensors output and
// the AMDGPU sysfs busy-percent file. gpuCard is typically "card1" on
// the Acer Aspire (no card0 because the iGPU is the only display
// adapter and the kernel skips card0).
func NewSensorsCollector(sysPath, gpuCard string) *SensorsCollector {
	return &SensorsCollector{
		sysPath:     sysPath,
		gpuCard:     gpuCard,
		sensorsPath: "sensors",
	}
}

// Collect returns the current sensor readings. Errors from individual
// sources degrade to zero values rather than failing the whole call.
func (c *SensorsCollector) Collect() (SensorsResult, error) {
	var r SensorsResult
	out, err := exec.Command(c.sensorsPath, "-j").Output()
	// `sensors` exits non-zero when some chips fail to read but still
	// emits valid JSON for the chips that worked, so we keep parsing
	// whenever there's output.
	if err != nil && len(out) == 0 {
		out = nil
	}
	if len(out) > 0 {
		parsed, perr := parseSensors(out)
		if perr == nil {
			r = parsed
		}
	}
	if pct, err := readGPUBusyPercent(c.sysPath, c.gpuCard); err == nil {
		r.GPUBusyPercent = pct
	}
	return r, nil
}
