// Command server es el entry point del proceso server-monitor.
//
// Carga config desde env vars, cablea los 9 collectors detrás de un
// Snapshotter y monta el API HTTP. Hace shutdown ordenado en SIGTERM
// para que CasaOS pueda reiniciar el container sin colgar requests
// en vuelo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jordisama/server-monitor/internal/alerts"
	"github.com/jordisama/server-monitor/internal/api"
	"github.com/jordisama/server-monitor/internal/collector"
	"github.com/jordisama/server-monitor/internal/config"
	"github.com/jordisama/server-monitor/internal/model"
	"github.com/jordisama/server-monitor/internal/web"
)

// version is the build-time-injected semantic version string. Defaults to
// "dev" for local builds; release builds override via -ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.Printf("server-monitor %s starting", version)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log.Printf("config: port=%d refresh=%v sensors_ttl=%v smartctl_ttl=%v",
		cfg.Port, cfg.RefreshInterval, cfg.SensorsTTL, cfg.SmartctlTTL)

	snapshotter := buildSnapshotter(cfg)
	srv := api.NewServer(snapshotter, cfg.RefreshInterval)
	srv.SetThresholds(cfg.Thresholds)
	srv.SetStaticHandler(web.FileServer())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.TelegramToken != "" && cfg.TelegramChatID != "" {
		notifier := alerts.NewTelegramNotifier(cfg.TelegramToken, cfg.TelegramChatID)
		engine := alerts.NewAlertEngine(notifier, alerts.ThresholdConfig{
			CPUTempWarn:   cfg.Thresholds.CPUTempWarn,
			CPUTempCrit:   cfg.Thresholds.CPUTempCrit,
			GPUTempWarn:   cfg.Thresholds.GPUTempWarn,
			GPUTempCrit:   cfg.Thresholds.GPUTempCrit,
			CPUUsageWarn:  cfg.Thresholds.CPUUsageWarn,
			CPUUsageCrit:  cfg.Thresholds.CPUUsageCrit,
			MemUsageWarn:  cfg.Thresholds.MemUsageWarn,
			MemUsageCrit:  cfg.Thresholds.MemUsageCrit,
			DiskUsageWarn: cfg.Thresholds.DiskUsageWarn,
			DiskUsageCrit: cfg.Thresholds.DiskUsageCrit,
			BatteryWarn:   cfg.Thresholds.BatteryWarn,
			BatteryCrit:   cfg.Thresholds.BatteryCrit,
		}, cfg.AlertCooldown)
		snapshotter.OnSnapshot(engine.Evaluate)
		log.Printf("telegram alerts enabled (cooldown=%v)", cfg.AlertCooldown)

		digest := alerts.NewDailyDigest(notifier, func() (model.MetricsSnapshot, error) {
			return snapshotter.Snapshot()
		}, cfg.DigestHour, cfg.DashboardURL, cfg.CasaOSURL, cfg.Timezone)
		go digest.Run(ctx)
		log.Printf("telegram digest enabled (hour=%d tz=%s url=%s)", cfg.DigestHour, cfg.Timezone, cfg.DashboardURL)

		cmdHandler := alerts.NewCommandHandler(cfg.TelegramToken, digest)
		go cmdHandler.Run(ctx)
	}

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// WriteTimeout intentionally NOT set: SSE streams long-lived.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Printf("server-monitor stopped cleanly")
	return nil
}

func buildSnapshotter(cfg config.Config) *collector.Snapshotter {
	diskTargets := make([]collector.DiskTarget, 0, len(cfg.DiskTargets))
	for _, dt := range cfg.DiskTargets {
		diskTargets = append(diskTargets, collector.DiskTarget{
			Mountpoint:   dt.Mountpoint,
			Device:       dt.Device,
			DiskstatName: dt.DiskstatName,
		})
	}
	return collector.NewSnapshotter(collector.SnapshotterConfig{
		CPU:        collector.NewCPUCollector(cfg.HostProc, cfg.HostSys),
		Memory:     collector.NewMemoryCollector(cfg.HostProc),
		Disk:       collector.NewDiskCollector(cfg.HostProc, diskTargets),
		Network:    collector.NewNetworkCollector(cfg.HostProc, cfg.HostSys, cfg.NetworkInterfaces),
		Battery:    collector.NewBatteryCollector(cfg.HostSys, cfg.BatteryName),
		Docker:     collector.NewDockerCollector("/var/run/docker.sock"),
		Sensors:    collector.NewSensorsCollector(cfg.HostSys, cfg.GPUCard),
		Processes:  collector.NewProcessesCollector(cfg.HostProc, 10),
		System:     collector.NewSystemCollector(cfg.HostProc),
		GPUName:    cfg.GPUName,
		SmartTTL:   cfg.SmartctlTTL,
		SensorsTTL: cfg.SensorsTTL,
	})
}
