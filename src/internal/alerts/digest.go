package alerts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

// SnapshotFunc obtains a current MetricsSnapshot on demand.
type SnapshotFunc func() (model.MetricsSnapshot, error)

// DailyDigest sends a formatted status summary every day at a fixed hour in a given timezone.
type DailyDigest struct {
	notifier     Notifier
	snapshotFn   SnapshotFunc
	hour         int // hour in loc to fire (0-23)
	dashboardURL string
	loc          *time.Location
}

// NewDailyDigest constructs a digest that fires at the given hour in timezone tz each day.
// If tz is empty or unknown, UTC is used.
func NewDailyDigest(notifier Notifier, snapshotFn SnapshotFunc, hour int, dashboardURL, tz string) *DailyDigest {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	return &DailyDigest{
		notifier:     notifier,
		snapshotFn:   snapshotFn,
		hour:         hour,
		dashboardURL: dashboardURL,
		loc:          loc,
	}
}

// Run blocks until ctx is cancelled, firing the digest once per day at d.hour in d.loc.
func (d *DailyDigest) Run(ctx context.Context) {
	for {
		next := nextFiring(time.Now().In(d.loc), d.hour)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			d.send()
		}
	}
}

// SendNow fires the digest immediately, used for /status commands.
func (d *DailyDigest) SendNow() {
	d.send()
}

func (d *DailyDigest) send() {
	snap, err := d.snapshotFn()
	if err != nil {
		_ = d.notifier.SendHTML("⚠️ <b>Resumen diario</b>\nNo se pudo obtener el estado del servidor.")
		return
	}
	_ = d.notifier.SendHTML(formatDigest(snap, d.dashboardURL, d.loc))
}

// nextFiring returns the next wall-clock moment when the local hour ticks over.
func nextFiring(now time.Time, hour int) time.Time {
	loc := now.Location()
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.Add(24 * time.Hour)
	}
	return candidate
}

func formatDigest(snap model.MetricsSnapshot, dashURL string, loc *time.Location) string {
	var b strings.Builder

	b.WriteString("🖥️ <b>Resumen diario — jordisama-server</b>\n")
	b.WriteString(fmt.Sprintf("<i>%s</i>\n\n", snap.Timestamp.In(loc).Format("02 Jan 2006 · 15:04")))

	// CPU
	cpuEmoji := metricEmoji(snap.CPU.OverallPercent, 60, 85)
	b.WriteString(fmt.Sprintf("%s <b>CPU:</b> %.1f%% uso · %.1f°C\n",
		cpuEmoji, snap.CPU.OverallPercent, snap.CPU.TempCelsius))

	// RAM
	memPct := 0.0
	if snap.Memory.TotalBytes > 0 {
		memPct = float64(snap.Memory.UsedBytes) / float64(snap.Memory.TotalBytes) * 100
	}
	memEmoji := metricEmoji(memPct, 70, 90)
	b.WriteString(fmt.Sprintf("%s <b>RAM:</b> %.1f%% · %s usados de %s\n",
		memEmoji, memPct, fmtBytes(snap.Memory.UsedBytes), fmtBytes(snap.Memory.TotalBytes)))

	// Discos
	if len(snap.Disks) > 0 {
		b.WriteString("\n💾 <b>Almacenamiento:</b>\n")
		for _, d := range snap.Disks {
			diskEmoji := metricEmoji(d.UsedPercent, 70, 85)
			b.WriteString(fmt.Sprintf("  %s <code>%s</code> — %s libres (%.0f%% usado)\n",
				diskEmoji, d.Mountpoint, fmtBytes(d.AvailableBytes), d.UsedPercent))
		}
	}

	// GPU
	if snap.GPU != nil {
		gpuEmoji := metricEmoji(snap.GPU.TempCelsius, 75, 90)
		b.WriteString(fmt.Sprintf("\n%s <b>GPU:</b> %.1f°C · %.1f%% carga\n",
			gpuEmoji, snap.GPU.TempCelsius, snap.GPU.BusyPercent))
	}

	// Batería
	if snap.Battery != nil {
		batEmoji := "🔋"
		if snap.Battery.Percent < 20 {
			batEmoji = "🪫"
		} else if snap.Battery.Percent < 50 {
			batEmoji = "⚠️"
		}
		b.WriteString(fmt.Sprintf("\n%s <b>Batería:</b> %.0f%% · %s\n",
			batEmoji, snap.Battery.Percent, snap.Battery.Status))
	}

	// Docker
	if snap.Docker != nil {
		b.WriteString(fmt.Sprintf("\n🐳 <b>Docker:</b> %d/%d contenedores corriendo\n",
			snap.Docker.RunningContainers, snap.Docker.TotalContainers))
	}

	// Sistema
	b.WriteString(fmt.Sprintf("\n⏱️ <b>Uptime:</b> %s · load %.2f\n",
		fmtUptime(snap.System.UptimeSeconds), snap.System.LoadAvg1m))

	// Top procesos por RAM
	if len(snap.TopProcesses) > 0 {
		b.WriteString("\n📊 <b>Top procesos (RAM):</b>\n")
		limit := 5
		if len(snap.TopProcesses) < limit {
			limit = len(snap.TopProcesses)
		}
		for i, p := range snap.TopProcesses[:limit] {
			b.WriteString(fmt.Sprintf("  %d. <code>%s</code> — %s RAM · %.1f%% CPU\n",
				i+1, p.Name, fmtBytes(p.RSSBytes), p.CPUPercent))
		}
	}

	b.WriteString(fmt.Sprintf("\n🔗 <a href=\"%s\">Abrir monitor</a>", dashURL))
	return b.String()
}

func metricEmoji(value, warn, crit float64) string {
	if value >= crit {
		return "🔴"
	}
	if value >= warn {
		return "⚠️"
	}
	return "✅"
}

func fmtBytes(b uint64) string {
	const (
		gb = 1 << 30
		mb = 1 << 20
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0f MB", float64(b)/mb)
	default:
		return fmt.Sprintf("%d KB", b/1024)
	}
}

func fmtUptime(seconds uint64) string {
	d := seconds / 86400
	h := (seconds % 86400) / 3600
	m := (seconds % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
