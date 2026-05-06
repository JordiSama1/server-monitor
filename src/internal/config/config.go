// Package config carga la configuración del proceso desde variables de
// entorno, aplicando defaults razonables y validando rangos. Es la
// fuente única de verdad para opciones runtime: el resto del código
// recibe un *config.Config y nunca lee env vars directamente.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DiskTargetConfig describe un (mountpoint, device) a monitorear.
// DiskstatName se deriva de Device si no se provee explícitamente.
type DiskTargetConfig struct {
	Mountpoint   string
	Device       string
	DiskstatName string
}

// Thresholds agrupa los umbrales warn/crit de cada métrica para que el
// frontend coloree gauges sin necesitar lógica propia de límites. Las
// tags JSON snake_case matchean el wire format del resto del API.
type Thresholds struct {
	CPUTempWarn   float64 `json:"cpu_temp_warn"`
	CPUTempCrit   float64 `json:"cpu_temp_crit"`
	GPUTempWarn   float64 `json:"gpu_temp_warn"`
	GPUTempCrit   float64 `json:"gpu_temp_crit"`
	DiskTempWarn  float64 `json:"disk_temp_warn"`
	DiskTempCrit  float64 `json:"disk_temp_crit"`
	CPUUsageWarn  float64 `json:"cpu_usage_warn"`
	CPUUsageCrit  float64 `json:"cpu_usage_crit"`
	MemUsageWarn  float64 `json:"mem_usage_warn"`
	MemUsageCrit  float64 `json:"mem_usage_crit"`
	DiskUsageWarn float64 `json:"disk_usage_warn"`
	DiskUsageCrit float64 `json:"disk_usage_crit"`
	BatteryWarn   float64 `json:"battery_warn"`
	BatteryCrit   float64 `json:"battery_crit"`
}

// Config es el snapshot completo de configuración del proceso. Se
// construye una vez al boot vía Load() y se pasa por valor (es chico).
type Config struct {
	Port              int
	RefreshInterval   time.Duration
	SensorsTTL        time.Duration
	SmartctlTTL       time.Duration
	LogLevel          string
	HostProc          string
	HostSys           string
	BatteryName       string
	GPUCard           string
	GPUName           string
	DiskTargets       []DiskTargetConfig
	NetworkInterfaces []string
	Thresholds        Thresholds
	TelegramToken     string
	TelegramChatID    string
	AlertCooldown     time.Duration
	DigestHour        int
	DashboardURL      string
	Timezone          string
}

// Load reads every supported env var and returns a validated Config.
// Returns an error wrapping all validation failures (port out of range,
// non-numeric integers, etc.); fields with malformed input are reported
// rather than silently defaulted.
func Load() (Config, error) {
	c := Config{
		Port:            8080,
		RefreshInterval: 2 * time.Second,
		SensorsTTL:      1 * time.Second,
		SmartctlTTL:     60 * time.Second,
		LogLevel:        "INFO",
		HostProc:        "/proc",
		HostSys:         "/sys",
		BatteryName:     "BAT1",
		GPUCard:         "card1",
		GPUName:         "AMD Vega 8",
		AlertCooldown:   30 * time.Minute,
		DigestHour:      10,
		DashboardURL:    "http://100.94.124.107:8080",
		Timezone:        "America/Santiago",
		Thresholds: Thresholds{
			CPUTempWarn: 70, CPUTempCrit: 80,
			GPUTempWarn: 75, GPUTempCrit: 90,
			DiskTempWarn: 45, DiskTempCrit: 55,
			CPUUsageWarn: 60, CPUUsageCrit: 85,
			MemUsageWarn: 70, MemUsageCrit: 90,
			DiskUsageWarn: 70, DiskUsageCrit: 85,
			BatteryWarn: 50, BatteryCrit: 20,
		},
	}

	var errs []string
	put := func(e error) {
		if e != nil {
			errs = append(errs, e.Error())
		}
	}

	put(loadIntInRange("PORT", &c.Port, 1, 65535))
	put(loadDurationSeconds("REFRESH_INTERVAL_SECONDS", &c.RefreshInterval, 1))
	put(loadDurationSeconds("SENSORS_CACHE_SECONDS", &c.SensorsTTL, 0))
	put(loadDurationSeconds("SMARTCTL_CACHE_SECONDS", &c.SmartctlTTL, 0))
	loadString("TELEGRAM_BOT_TOKEN", &c.TelegramToken)
	loadString("TELEGRAM_CHAT_ID", &c.TelegramChatID)
	loadString("DASHBOARD_URL", &c.DashboardURL)
	loadString("TIMEZONE", &c.Timezone)
	put(loadDurationSeconds("ALERT_COOLDOWN_SECONDS", &c.AlertCooldown, 0))
	put(loadIntInRange("DIGEST_HOUR", &c.DigestHour, 0, 23))
	loadString("LOG_LEVEL", &c.LogLevel)
	loadString("HOST_PROC", &c.HostProc)
	loadString("HOST_SYS", &c.HostSys)
	loadString("BATTERY_NAME", &c.BatteryName)
	loadString("GPU_CARD", &c.GPUCard)
	loadString("GPU_NAME", &c.GPUName)

	deviceSSD := envOr("DEVICE_SSD", "/dev/sdb")
	deviceHDD := envOr("DEVICE_HDD", "/dev/sda")
	c.DiskTargets = []DiskTargetConfig{
		{Mountpoint: "/", Device: deviceSSD, DiskstatName: deriveDiskstatName(deviceSSD)},
		{Mountpoint: "/mnt/datos", Device: deviceHDD, DiskstatName: deriveDiskstatName(deviceHDD)},
	}

	ifacesRaw := envOr("NETWORK_INTERFACES", "enp1s0f1,tailscale0,docker0,wlp2s0")
	for _, name := range strings.Split(ifacesRaw, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			c.NetworkInterfaces = append(c.NetworkInterfaces, trimmed)
		}
	}

	put(loadFloat("THRESHOLD_CPU_TEMP_WARN", &c.Thresholds.CPUTempWarn))
	put(loadFloat("THRESHOLD_CPU_TEMP_CRIT", &c.Thresholds.CPUTempCrit))
	put(loadFloat("THRESHOLD_GPU_TEMP_WARN", &c.Thresholds.GPUTempWarn))
	put(loadFloat("THRESHOLD_GPU_TEMP_CRIT", &c.Thresholds.GPUTempCrit))
	put(loadFloat("THRESHOLD_DISK_TEMP_WARN", &c.Thresholds.DiskTempWarn))
	put(loadFloat("THRESHOLD_DISK_TEMP_CRIT", &c.Thresholds.DiskTempCrit))
	put(loadFloat("THRESHOLD_CPU_USAGE_WARN", &c.Thresholds.CPUUsageWarn))
	put(loadFloat("THRESHOLD_CPU_USAGE_CRIT", &c.Thresholds.CPUUsageCrit))
	put(loadFloat("THRESHOLD_MEM_USAGE_WARN", &c.Thresholds.MemUsageWarn))
	put(loadFloat("THRESHOLD_MEM_USAGE_CRIT", &c.Thresholds.MemUsageCrit))
	put(loadFloat("THRESHOLD_DISK_USAGE_WARN", &c.Thresholds.DiskUsageWarn))
	put(loadFloat("THRESHOLD_DISK_USAGE_CRIT", &c.Thresholds.DiskUsageCrit))
	put(loadFloat("THRESHOLD_BATTERY_WARN", &c.Thresholds.BatteryWarn))
	put(loadFloat("THRESHOLD_BATTERY_CRIT", &c.Thresholds.BatteryCrit))

	if len(errs) > 0 {
		return c, fmt.Errorf("config errors: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func loadString(key string, dst *string) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		*dst = v
	}
}

func loadIntInRange(key string, dst *int, min, max int) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s=%q: not an integer", key, v)
	}
	if n < min || n > max {
		return fmt.Errorf("%s=%d: out of range [%d, %d]", key, n, min, max)
	}
	*dst = n
	return nil
}

func loadFloat(key string, dst *float64) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("%s=%q: not a number", key, v)
	}
	*dst = f
	return nil
}

func loadDurationSeconds(key string, dst *time.Duration, min int) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s=%q: not an integer", key, v)
	}
	if n < min {
		return fmt.Errorf("%s=%d: must be >= %d", key, n, min)
	}
	*dst = time.Duration(n) * time.Second
	return nil
}

// deriveDiskstatName extracts the basename used by /proc/diskstats from a
// /dev/* path. /dev/sdb → sdb; /dev/mapper/foo → foo. If the input is
// already a bare name (no slash), it's returned unchanged.
func deriveDiskstatName(devicePath string) string {
	return filepath.Base(devicePath)
}
