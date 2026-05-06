package alerts

import (
	"fmt"
	"sync"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

// Level represents an alert severity.
type Level int

const (
	// LevelOK means the metric is within normal bounds.
	LevelOK Level = iota
	// LevelWarn means the metric has crossed the warn threshold.
	LevelWarn
	// LevelCrit means the metric has crossed the critical threshold.
	LevelCrit
)

func (l Level) String() string {
	switch l {
	case LevelWarn:
		return "⚠️ WARN"
	case LevelCrit:
		return "🔴 CRIT"
	default:
		return "✅ OK"
	}
}

// ThresholdConfig holds warn/crit boundaries for the alert engine.
type ThresholdConfig struct {
	CPUTempWarn   float64
	CPUTempCrit   float64
	GPUTempWarn   float64
	GPUTempCrit   float64
	CPUUsageWarn  float64
	CPUUsageCrit  float64
	MemUsageWarn  float64
	MemUsageCrit  float64
	DiskUsageWarn float64
	DiskUsageCrit float64
	BatteryWarn   float64
	BatteryCrit   float64
}

type metricState struct {
	level    Level
	firedAt  time.Time
}

// AlertEngine evaluates MetricsSnapshots against thresholds and fires
// notifications on state transitions. A cooldown prevents repeat alerts
// for a metric that stays in the same non-OK state.
type AlertEngine struct {
	notifier  Notifier
	thresholds ThresholdConfig
	cooldown  time.Duration

	mu     sync.Mutex
	states map[string]metricState
}

// NewAlertEngine constructs an engine that fires via notifier when thresholds
// are crossed. cooldown sets the minimum interval between repeated alerts for
// the same metric in the same state.
func NewAlertEngine(notifier Notifier, thresholds ThresholdConfig, cooldown time.Duration) *AlertEngine {
	return &AlertEngine{
		notifier:   notifier,
		thresholds: thresholds,
		cooldown:   cooldown,
		states:     make(map[string]metricState),
	}
}

// Evaluate inspects snap and sends alerts for any threshold transitions.
func (e *AlertEngine) Evaluate(snap model.MetricsSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	t := e.thresholds

	e.check(now, "cpu_temp", snap.CPU.TempCelsius, t.CPUTempWarn, t.CPUTempCrit,
		func(l Level) string {
			return fmt.Sprintf("*CPU Temperatura %s*\n%.1f °C (warn=%.0f crit=%.0f)",
				l, snap.CPU.TempCelsius, t.CPUTempWarn, t.CPUTempCrit)
		})

	e.check(now, "cpu_usage", snap.CPU.OverallPercent, t.CPUUsageWarn, t.CPUUsageCrit,
		func(l Level) string {
			return fmt.Sprintf("*CPU Uso %s*\n%.1f %% (warn=%.0f crit=%.0f)",
				l, snap.CPU.OverallPercent, t.CPUUsageWarn, t.CPUUsageCrit)
		})

	memPct := 0.0
	if snap.Memory.TotalBytes > 0 {
		memPct = float64(snap.Memory.UsedBytes) / float64(snap.Memory.TotalBytes) * 100
	}
	e.check(now, "mem_usage", memPct, t.MemUsageWarn, t.MemUsageCrit,
		func(l Level) string {
			return fmt.Sprintf("*Memoria %s*\n%.1f %% usada (warn=%.0f crit=%.0f)",
				l, memPct, t.MemUsageWarn, t.MemUsageCrit)
		})

	for _, d := range snap.Disks {
		key := "disk_usage:" + d.Mountpoint
		mountpoint := d.Mountpoint
		pct := d.UsedPercent
		e.check(now, key, pct, t.DiskUsageWarn, t.DiskUsageCrit,
			func(l Level) string {
				return fmt.Sprintf("*Disco %s*\n`%s` %.1f %% usado (warn=%.0f crit=%.0f)",
					l, mountpoint, pct, t.DiskUsageWarn, t.DiskUsageCrit)
			})
	}

	if snap.GPU != nil {
		gpuTemp := snap.GPU.TempCelsius
		e.check(now, "gpu_temp", gpuTemp, t.GPUTempWarn, t.GPUTempCrit,
			func(l Level) string {
				return fmt.Sprintf("*GPU Temperatura %s*\n%.1f °C (warn=%.0f crit=%.0f)",
					l, gpuTemp, t.GPUTempWarn, t.GPUTempCrit)
			})
	}

	if snap.Battery != nil {
		batPct := snap.Battery.Percent
		// Battery alert fires when charge is LOW (below threshold), so we
		// invert: treat (100 - pct) as the "value" exceeding thresholds.
		e.check(now, "battery", 100-batPct, 100-t.BatteryWarn, 100-t.BatteryCrit,
			func(l Level) string {
				statusIcon := map[Level]string{LevelWarn: "⚠️ WARN", LevelCrit: "🔴 CRIT", LevelOK: "✅ OK"}[l]
				return fmt.Sprintf("*Batería %s*\n%.0f %% — %s (warn=%.0f%% crit=%.0f%%)",
					statusIcon, batPct, snap.Battery.Status, t.BatteryWarn, t.BatteryCrit)
			})
	}
}

// check evaluates value against warn/crit and fires notifier on transition or cooldown expiry.
func (e *AlertEngine) check(now time.Time, key string, value, warn, crit float64, msg func(Level) string) {
	newLevel := levelFor(value, warn, crit)
	prev := e.states[key]

	transition := newLevel != prev.level
	cooledDown := now.Sub(prev.firedAt) >= e.cooldown

	if !transition && (newLevel == LevelOK || !cooledDown) {
		return
	}

	e.states[key] = metricState{level: newLevel, firedAt: now}
	if newLevel == LevelOK && !transition {
		return
	}

	_ = e.notifier.Send(msg(newLevel))
}

func levelFor(value, warn, crit float64) Level {
	if value >= crit {
		return LevelCrit
	}
	if value >= warn {
		return LevelWarn
	}
	return LevelOK
}
