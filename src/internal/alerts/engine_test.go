package alerts

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

// captureNotifier collects sent messages for test assertions.
type captureNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureNotifier) Send(msg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *captureNotifier) SendHTML(msg string) error {
	return c.Send(msg)
}

func (c *captureNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *captureNotifier) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return ""
	}
	return c.msgs[len(c.msgs)-1]
}

func defaultThresholds() ThresholdConfig {
	return ThresholdConfig{
		CPUTempWarn: 70, CPUTempCrit: 80,
		GPUTempWarn: 75, GPUTempCrit: 90,
		CPUUsageWarn: 60, CPUUsageCrit: 85,
		MemUsageWarn: 70, MemUsageCrit: 90,
		DiskUsageWarn: 70, DiskUsageCrit: 85,
		BatteryWarn: 50, BatteryCrit: 20,
	}
}

func TestLevelFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value, warn, crit float64
		want              Level
	}{
		{50, 70, 80, LevelOK},
		{70, 70, 80, LevelWarn},
		{75, 70, 80, LevelWarn},
		{80, 70, 80, LevelCrit},
		{95, 70, 80, LevelCrit},
	}
	for _, tc := range cases {
		got := levelFor(tc.value, tc.warn, tc.crit)
		if got != tc.want {
			t.Errorf("levelFor(%.0f, %.0f, %.0f) = %v, want %v",
				tc.value, tc.warn, tc.crit, got, tc.want)
		}
	}
}

func TestEngineFiresOnTransitionOKToWarn(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 72, OverallPercent: 10}}
	e.Evaluate(snap)

	if n.count() != 1 {
		t.Fatalf("expected 1 alert, got %d", n.count())
	}
	if got := n.last(); got == "" {
		t.Error("message must not be empty")
	}
}

func TestEngineDoesNotFireWhenOK(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 50, OverallPercent: 10}}
	e.Evaluate(snap)
	e.Evaluate(snap)

	if n.count() != 0 {
		t.Errorf("expected 0 alerts in OK state, got %d", n.count())
	}
}

func TestEngineFiresOnTransitionWarnToCrit(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	// First: WARN
	e.Evaluate(model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 72}})
	// Second: CRIT — must fire again
	e.Evaluate(model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 85}})

	if n.count() != 2 {
		t.Errorf("expected 2 alerts (warn+crit), got %d", n.count())
	}
}

func TestEngineFiresRecoveryOnCritToOK(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	e.Evaluate(model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 85}}) // CRIT
	e.Evaluate(model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 50}}) // OK recovery

	if n.count() != 2 {
		t.Fatalf("expected 2 alerts (crit+recovery), got %d", n.count())
	}
}

func TestEngineRespectsCooldown(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	// Large cooldown: repeated same-level evals must not fire
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 72}}
	e.Evaluate(snap) // first WARN → fires
	e.Evaluate(snap) // still WARN, within cooldown → silent
	e.Evaluate(snap)

	if n.count() != 1 {
		t.Errorf("cooldown not respected: got %d alerts, want 1", n.count())
	}
}

func TestEngineCooldownExpiry(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	// Zero cooldown: repeated same-level evals always fire
	e := NewAlertEngine(n, defaultThresholds(), 0)

	snap := model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 72}}
	e.Evaluate(snap)
	e.Evaluate(snap)
	e.Evaluate(snap)

	if n.count() != 3 {
		t.Errorf("zero cooldown: expected 3 alerts, got %d", n.count())
	}
}

func TestEngineEvaluatesAllDisks(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{
		Disks: []model.Disk{
			{Mountpoint: "/", UsedPercent: 75},
			{Mountpoint: "/mnt/datos", UsedPercent: 87},
		},
	}
	e.Evaluate(snap)

	if n.count() != 2 {
		t.Errorf("expected 2 disk alerts, got %d: %v", n.count(), n.msgs)
	}
}

func TestEngineBatteryLowFiresAlert(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{
		Battery: &model.Battery{Percent: 15, Status: "Discharging"},
	}
	e.Evaluate(snap)

	if n.count() != 1 {
		t.Fatalf("expected 1 battery alert, got %d", n.count())
	}
}

func TestEngineBatteryFullNoAlert(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)

	snap := model.MetricsSnapshot{
		Battery: &model.Battery{Percent: 90, Status: "Charging"},
	}
	e.Evaluate(snap)

	if n.count() != 0 {
		t.Errorf("no alert expected for full battery, got %d", n.count())
	}
}

func TestEngineNilBatteryDoesNotPanic(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	e := NewAlertEngine(n, defaultThresholds(), time.Hour)
	// Must not panic when Battery/GPU are nil
	e.Evaluate(model.MetricsSnapshot{})
}

// errorNotifier always returns an error — engine must not panic or block.
type errorNotifier struct{}

func (e *errorNotifier) Send(_ string) error       { return fmt.Errorf("network timeout") }
func (e *errorNotifier) SendHTML(_ string) error   { return fmt.Errorf("network timeout") }

func TestEngineToleratesNotifierError(t *testing.T) {
	t.Parallel()
	e := NewAlertEngine(&errorNotifier{}, defaultThresholds(), time.Hour)
	// Must not panic or hang
	e.Evaluate(model.MetricsSnapshot{CPU: model.CPU{TempCelsius: 85}})
}
