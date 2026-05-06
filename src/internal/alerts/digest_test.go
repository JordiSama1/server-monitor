package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

func TestNextFiringFutureToday(t *testing.T) {
	t.Parallel()
	// 08:00 → next 10:00 is today
	now := time.Date(2026, 5, 6, 8, 0, 0, 0, time.UTC)
	got := nextFiring(now, 10)
	want := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextFiring = %v, want %v", got, want)
	}
}

func TestNextFiringAlreadyPassedToday(t *testing.T) {
	t.Parallel()
	// 11:00 → next 10:00 is tomorrow
	now := time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)
	got := nextFiring(now, 10)
	want := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextFiring = %v, want %v", got, want)
	}
}

func TestNextFiringExactHour(t *testing.T) {
	t.Parallel()
	// Exactly 10:00 → next is tomorrow (not today, since !After)
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	got := nextFiring(now, 10)
	want := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextFiring = %v, want %v", got, want)
	}
}

func TestFormatDigestContainsKeyFields(t *testing.T) {
	t.Parallel()
	snap := model.MetricsSnapshot{
		Timestamp: time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC),
		CPU:       model.CPU{OverallPercent: 23.5, TempCelsius: 48.0},
		Memory: model.Memory{
			TotalBytes: 12 * 1024 * 1024 * 1024,
			UsedBytes:  4 * 1024 * 1024 * 1024,
		},
		Disks: []model.Disk{
			{Mountpoint: "/", UsedPercent: 15.9, AvailableBytes: 89 * 1024 * 1024 * 1024},
			{Mountpoint: "/mnt/datos", UsedPercent: 21.9, AvailableBytes: 727 * 1024 * 1024 * 1024},
		},
		GPU:     &model.GPU{TempCelsius: 39.0, BusyPercent: 5.5},
		Battery: &model.Battery{Percent: 87.0, Status: "Discharging"},
		Docker:  &model.Docker{RunningContainers: 7, TotalContainers: 10},
		System:  model.System{UptimeSeconds: 86400, LoadAvg1m: 0.45},
		TopProcesses: []model.Process{
			{Name: "node", RSSBytes: 512 * 1024 * 1024, CPUPercent: 12.5},
		},
	}

	msg := formatDigest(snap, "http://jordisama-server:8080", time.UTC)

	required := []string{
		"jordisama-server",
		"CPU",
		"RAM",
		"Almacenamiento",
		"/",
		"/mnt/datos",
		"GPU",
		"Batería",
		"Docker",
		"Uptime",
		"Top procesos",
		"node",
		"http://jordisama-server:8080",
	}
	for _, want := range required {
		if !strings.Contains(msg, want) {
			t.Errorf("formatDigest missing %q\nmsg:\n%s", want, msg)
		}
	}
}

func TestFormatDigestMetricEmojis(t *testing.T) {
	t.Parallel()
	// CPU at 90% (crit) should show 🔴
	snap := model.MetricsSnapshot{
		CPU: model.CPU{OverallPercent: 90, TempCelsius: 50},
		Memory: model.Memory{
			TotalBytes: 8 * 1024 * 1024 * 1024,
			UsedBytes:  1 * 1024 * 1024 * 1024,
		},
	}
	msg := formatDigest(snap, "http://localhost", time.UTC)
	if !strings.Contains(msg, "🔴") {
		t.Errorf("expected 🔴 for 90%% CPU crit, got:\n%s", msg)
	}
}

func TestFormatDigestNilOptionals(t *testing.T) {
	t.Parallel()
	// GPU, Battery, Docker all nil — must not panic
	snap := model.MetricsSnapshot{
		CPU:    model.CPU{OverallPercent: 10, TempCelsius: 40},
		Memory: model.Memory{TotalBytes: 8 * 1024 * 1024 * 1024, UsedBytes: 1 * 1024 * 1024 * 1024},
		System: model.System{UptimeSeconds: 3600},
	}
	msg := formatDigest(snap, "http://localhost", time.UTC)
	if msg == "" {
		t.Error("formatDigest returned empty string")
	}
}

func TestDailyDigestFiresWhenTimeExpires(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	fired := false
	snapshotFn := func() (model.MetricsSnapshot, error) {
		fired = true
		return model.MetricsSnapshot{
			CPU:    model.CPU{OverallPercent: 10},
			Memory: model.Memory{TotalBytes: 1024 * 1024 * 1024, UsedBytes: 512 * 1024 * 1024},
			System: model.System{UptimeSeconds: 3600},
		}, nil
	}

	// Use hour=0 (midnight) but manipulate by calling send() directly
	d := NewDailyDigest(n, snapshotFn, 0, "http://localhost", "UTC")
	d.send()

	if !fired {
		t.Error("snapshotFn not called")
	}
	if n.count() != 1 {
		t.Errorf("expected 1 digest message, got %d", n.count())
	}
}

func TestDailyDigestRunCancels(t *testing.T) {
	t.Parallel()
	n := &captureNotifier{}
	d := NewDailyDigest(n, func() (model.MetricsSnapshot, error) {
		return model.MetricsSnapshot{}, nil
	}, 3, "http://localhost", "UTC") // hour=3, won't fire in test

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	d.Run(ctx) // must return when ctx expires
}

func TestFmtBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input uint64
		want  string
	}{
		{0, "0 KB"},
		{1024, "1 KB"},
		{512 * 1024 * 1024, "512 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, tc := range cases {
		got := fmtBytes(tc.input)
		if got != tc.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFmtUptime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		secs uint64
		want string
	}{
		{45, "0m"},
		{3600, "1h 0m"},
		{86400, "1d 0h"},
		{90061, "1d 1h"},
	}
	for _, tc := range cases {
		got := fmtUptime(tc.secs)
		if got != tc.want {
			t.Errorf("fmtUptime(%d) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}
