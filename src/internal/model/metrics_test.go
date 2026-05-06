package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
)

// newPopulatedSnapshot returns a MetricsSnapshot with all optional pointers set
// and realistic field values, for use as a round-trip fixture.
func newPopulatedSnapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Timestamp: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
		CPU: CPU{
			OverallPercent: 23.5,
			PerCore: []CoreCPU{
				{ID: 0, Percent: 21.0, TempCelsius: 39.8, FreqMHz: 2375.0},
				{ID: 1, Percent: 25.5, TempCelsius: 40.1, FreqMHz: 2400.0},
				{ID: 2, Percent: 22.0, TempCelsius: 38.9, FreqMHz: 2350.0},
				{ID: 3, Percent: 25.5, TempCelsius: 40.4, FreqMHz: 2400.0},
			},
			TempCelsius: 39.8,
			FreqMHzAvg:  2381.25,
		},
		Memory: Memory{
			TotalBytes:     12 * 1024 * 1024 * 1024,
			UsedBytes:      4 * 1024 * 1024 * 1024,
			AvailableBytes: 8 * 1024 * 1024 * 1024,
			CachedBytes:    1500 * 1024 * 1024,
			BuffersBytes:   200 * 1024 * 1024,
			SwapTotalBytes: 4 * 1024 * 1024 * 1024,
			SwapUsedBytes:  0,
		},
		Disks: []Disk{
			{
				Mountpoint:         "/",
				Device:             "/dev/sdb",
				TotalBytes:         120_000_000_000,
				UsedBytes:          60_000_000_000,
				AvailableBytes:     60_000_000_000,
				UsedPercent:        50.0,
				TempCelsius:        38.0,
				IOReadBytesPerSec:  1024 * 100,
				IOWriteBytesPerSec: 1024 * 200,
			},
			{
				Mountpoint:         "/mnt/datos",
				Device:             "/dev/sda",
				TotalBytes:         1_000_000_000_000,
				UsedBytes:          400_000_000_000,
				AvailableBytes:     600_000_000_000,
				UsedPercent:        40.0,
				TempCelsius:        42.0,
				IOReadBytesPerSec:  1024 * 50,
				IOWriteBytesPerSec: 1024 * 80,
			},
		},
		Networks: []NetworkIface{
			{Name: "enp1s0f1", RxBytes: 1_000_000_000, TxBytes: 500_000_000, RxBytesPerSec: 1024 * 50, TxBytesPerSec: 1024 * 30, IsUp: true},
			{Name: "tailscale0", RxBytes: 100_000_000, TxBytes: 80_000_000, RxBytesPerSec: 1024 * 10, TxBytesPerSec: 1024 * 8, IsUp: true},
			{Name: "wlp2s0", RxBytes: 50_000_000, TxBytes: 30_000_000, RxBytesPerSec: 0, TxBytesPerSec: 0, IsUp: false},
		},
		GPU: &GPU{Name: "AMD Vega 8", TempCelsius: 39.0, BusyPercent: 5.5},
		Battery: &Battery{
			Name:                  "BAT1",
			Percent:               87.0,
			Status:                "Discharging",
			CapacityHealthPercent: 82.5,
			EnergyNowWh:           30.5,
			EnergyFullWh:          36.9,
			EnergyDesignWh:        44.7,
		},
		Docker: &Docker{RunningContainers: 7, TotalContainers: 10},
		System: System{
			UptimeSeconds:  86400,
			LoadAvg1m:      0.45,
			LoadAvg5m:      0.52,
			LoadAvg15m:     0.60,
			ProcessesCount: 245,
		},
		TopProcesses: []Process{
			{PID: 1234, Name: "node", RSSBytes: 512 * 1024 * 1024, CPUPercent: 12.5, ElapsedSeconds: 7200},
			{PID: 5678, Name: "postgres", RSSBytes: 256 * 1024 * 1024, CPUPercent: 4.2, ElapsedSeconds: 86400},
		},
	}
}

func TestMetricsSnapshotRoundTrip(t *testing.T) {
	original := newPopulatedSnapshot()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded MetricsSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("round-trip mismatch\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}

func TestEachStructMarshalsZeroValue(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"CPU", CPU{}},
		{"CoreCPU", CoreCPU{}},
		{"Memory", Memory{}},
		{"Disk", Disk{}},
		{"NetworkIface", NetworkIface{}},
		{"GPU", GPU{}},
		{"Battery", Battery{}},
		{"Docker", Docker{}},
		{"System", System{}},
		{"Process", Process{}},
		{"MetricsSnapshot", MetricsSnapshot{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("zero %s must marshal: %v", tc.name, err)
			}
			if len(data) == 0 {
				t.Errorf("zero %s produced empty bytes", tc.name)
			}
		})
	}
}

func TestSnapshotOptionalFieldsOmittedWhenNil(t *testing.T) {
	snap := newPopulatedSnapshot()
	snap.GPU = nil
	snap.Battery = nil
	snap.Docker = nil
	snap.TopProcesses = nil

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)

	for _, key := range []string{`"gpu"`, `"battery"`, `"docker"`, `"top_processes"`} {
		if strings.Contains(out, key) {
			t.Errorf("nil optional %s must be omitted, got: %s", key, out)
		}
	}
}

func TestSnapshotOptionalFieldsPresentWhenSet(t *testing.T) {
	snap := newPopulatedSnapshot()

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)

	for _, key := range []string{`"gpu"`, `"battery"`, `"docker"`, `"top_processes"`} {
		if !strings.Contains(out, key) {
			t.Errorf("populated optional %s must be present, got: %s", key, out)
		}
	}
}

// TestSnakeCaseJSONKeys verifies every public struct field is exposed under a
// snake_case JSON key. Frontends and external consumers read these keys, so
// drift here is a wire-protocol break.
func TestSnakeCaseJSONKeys(t *testing.T) {
	snap := newPopulatedSnapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(data)

	required := []string{
		`"timestamp"`,
		`"cpu"`, `"memory"`, `"disks"`, `"networks"`, `"system"`,
		`"overall_percent"`, `"per_core"`, `"temp_celsius"`, `"freq_mhz_avg"`,
		`"id"`, `"percent"`, `"freq_mhz"`,
		`"total_bytes"`, `"used_bytes"`, `"available_bytes"`,
		`"cached_bytes"`, `"buffers_bytes"`,
		`"swap_total_bytes"`, `"swap_used_bytes"`,
		`"mountpoint"`, `"device"`, `"used_percent"`,
		`"io_read_bytes_per_sec"`, `"io_write_bytes_per_sec"`,
		`"name"`,
		`"rx_bytes"`, `"tx_bytes"`,
		`"rx_bytes_per_sec"`, `"tx_bytes_per_sec"`,
		`"is_up"`,
		`"busy_percent"`,
		`"status"`, `"capacity_health_percent"`,
		`"energy_now_wh"`, `"energy_full_wh"`, `"energy_design_wh"`,
		`"running_containers"`, `"total_containers"`,
		`"uptime_seconds"`,
		`"load_avg_1m"`, `"load_avg_5m"`, `"load_avg_15m"`,
		`"processes_count"`,
		`"top_processes"`,
		`"pid"`, `"rss_bytes"`, `"cpu_percent"`, `"elapsed_seconds"`,
	}
	for _, key := range required {
		if !strings.Contains(out, key) {
			t.Errorf("missing snake_case key %s in JSON output:\n%s", key, out)
		}
	}
}

func TestEdgeCaseLargeUint64(t *testing.T) {
	snap := MetricsSnapshot{
		Memory: Memory{
			TotalBytes:     math.MaxUint64,
			UsedBytes:      math.MaxUint64 - 1,
			AvailableBytes: math.MaxUint64 - 2,
			CachedBytes:    math.MaxUint64 / 2,
			BuffersBytes:   math.MaxUint64 / 4,
			SwapTotalBytes: math.MaxUint64,
			SwapUsedBytes:  math.MaxUint64,
		},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded MetricsSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Memory != snap.Memory {
		t.Errorf("uint64 max round-trip diverged\n  original: %+v\n  decoded:  %+v",
			snap.Memory, decoded.Memory)
	}
}

func TestEdgeCaseNonFiniteFloatRejected(t *testing.T) {
	cases := []struct {
		name  string
		value float64
	}{
		{"NaN", math.NaN()},
		{"PositiveInfinity", math.Inf(1)},
		{"NegativeInfinity", math.Inf(-1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := MetricsSnapshot{CPU: CPU{TempCelsius: tc.value}}
			_, err := json.Marshal(snap)
			if err == nil {
				t.Fatalf("expected marshal error for %s, got nil", tc.name)
			}
			var unsupported *json.UnsupportedValueError
			if !errors.As(err, &unsupported) {
				t.Errorf("expected *json.UnsupportedValueError, got %T: %v", err, err)
			}
		})
	}
}

// TestNoCamelCaseKeys is defense-in-depth on top of TestSnakeCaseJSONKeys.
// TestSnakeCaseJSONKeys verifies expected keys exist; this one verifies no
// unexpected non-snake_case keys leak in. Catches the classic regression of a
// new field added without a `json:"…"` tag, where Go falls back to the Go
// field name (CamelCase) and the wire format silently breaks.
func TestNoCamelCaseKeys(t *testing.T) {
	snap := newPopulatedSnapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch n := v.(type) {
		case map[string]any:
			for k, child := range n {
				if hasUpperCase(k) {
					t.Errorf("non-snake_case JSON key %q at %s", k, path)
				}
				walk(path+"."+k, child)
			}
		case []any:
			for i, child := range n {
				walk(fmt.Sprintf("%s[%d]", path, i), child)
			}
		}
	}
	walk("$", raw)
}

func hasUpperCase(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func TestTimestampRoundTripPreservesUTC(t *testing.T) {
	want := time.Date(2026, 5, 6, 12, 30, 45, 0, time.UTC)
	snap := MetricsSnapshot{Timestamp: want}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded MetricsSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !decoded.Timestamp.Equal(want) {
		t.Errorf("timestamp round-trip: got %v, want %v", decoded.Timestamp, want)
	}
}
