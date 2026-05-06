package config

import (
	"testing"
	"time"
)

// withCleanEnv unsets every env var the loader reads so each test starts
// from a known baseline regardless of the calling shell.
func withCleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PORT", "REFRESH_INTERVAL_SECONDS", "SENSORS_CACHE_SECONDS",
		"SMARTCTL_CACHE_SECONDS", "LOG_LEVEL", "HOST_PROC", "HOST_SYS",
		"DEVICE_SSD", "DEVICE_HDD", "BATTERY_NAME", "GPU_CARD", "GPU_NAME",
		"NETWORK_INTERFACES",
		"THRESHOLD_CPU_TEMP_WARN", "THRESHOLD_CPU_TEMP_CRIT",
		"THRESHOLD_GPU_TEMP_WARN", "THRESHOLD_GPU_TEMP_CRIT",
		"THRESHOLD_DISK_TEMP_WARN", "THRESHOLD_DISK_TEMP_CRIT",
		"THRESHOLD_CPU_USAGE_WARN", "THRESHOLD_CPU_USAGE_CRIT",
		"THRESHOLD_MEM_USAGE_WARN", "THRESHOLD_MEM_USAGE_CRIT",
		"THRESHOLD_DISK_USAGE_WARN", "THRESHOLD_DISK_USAGE_CRIT",
		"THRESHOLD_BATTERY_WARN", "THRESHOLD_BATTERY_CRIT",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	withCleanEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, want 8080", c.Port)
	}
	if c.RefreshInterval != 2*time.Second {
		t.Errorf("RefreshInterval = %v, want 2s", c.RefreshInterval)
	}
	if c.SensorsTTL != 1*time.Second {
		t.Errorf("SensorsTTL = %v, want 1s", c.SensorsTTL)
	}
	if c.SmartctlTTL != 60*time.Second {
		t.Errorf("SmartctlTTL = %v, want 60s", c.SmartctlTTL)
	}
	if c.HostProc != "/proc" || c.HostSys != "/sys" {
		t.Errorf("paths got proc=%s sys=%s", c.HostProc, c.HostSys)
	}
	if c.BatteryName != "BAT1" || c.GPUCard != "card1" {
		t.Errorf("device names got bat=%s gpu=%s", c.BatteryName, c.GPUCard)
	}
	if len(c.DiskTargets) != 2 {
		t.Fatalf("DiskTargets len = %d, want 2", len(c.DiskTargets))
	}
	if c.DiskTargets[0].Device != "/dev/sdb" || c.DiskTargets[0].Mountpoint != "/" {
		t.Errorf("disk[0] = %+v", c.DiskTargets[0])
	}
	if c.DiskTargets[1].Device != "/dev/sda" || c.DiskTargets[1].Mountpoint != "/mnt/datos" {
		t.Errorf("disk[1] = %+v", c.DiskTargets[1])
	}
	wantIfaces := []string{"enp1s0f1", "tailscale0", "docker0", "wlp2s0"}
	if len(c.NetworkInterfaces) != len(wantIfaces) {
		t.Fatalf("NetworkInterfaces = %v, want %v", c.NetworkInterfaces, wantIfaces)
	}
	for i, want := range wantIfaces {
		if c.NetworkInterfaces[i] != want {
			t.Errorf("NetworkInterfaces[%d] = %s, want %s", i, c.NetworkInterfaces[i], want)
		}
	}
	if c.Thresholds.CPUTempWarn != 70 || c.Thresholds.CPUTempCrit != 80 {
		t.Errorf("CPU temp thresholds: %+v", c.Thresholds)
	}
	if c.Thresholds.GPUTempCrit != 90 {
		t.Errorf("GPUTempCrit = %f, want 90 (Vega 8 corregido)", c.Thresholds.GPUTempCrit)
	}
}

func TestLoadOverridesFromEnv(t *testing.T) {
	withCleanEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("REFRESH_INTERVAL_SECONDS", "5")
	t.Setenv("SENSORS_CACHE_SECONDS", "3")
	t.Setenv("SMARTCTL_CACHE_SECONDS", "120")
	t.Setenv("HOST_PROC", "/host/proc")
	t.Setenv("DEVICE_SSD", "/dev/nvme0n1")
	t.Setenv("BATTERY_NAME", "BAT0")
	t.Setenv("NETWORK_INTERFACES", "eth0,wlan0,br0")
	t.Setenv("THRESHOLD_CPU_TEMP_CRIT", "85")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Port != 9090 {
		t.Errorf("Port = %d, want 9090", c.Port)
	}
	if c.RefreshInterval != 5*time.Second {
		t.Errorf("RefreshInterval = %v", c.RefreshInterval)
	}
	if c.SensorsTTL != 3*time.Second {
		t.Errorf("SensorsTTL = %v", c.SensorsTTL)
	}
	if c.SmartctlTTL != 120*time.Second {
		t.Errorf("SmartctlTTL = %v", c.SmartctlTTL)
	}
	if c.HostProc != "/host/proc" {
		t.Errorf("HostProc = %s", c.HostProc)
	}
	if c.DiskTargets[0].Device != "/dev/nvme0n1" {
		t.Errorf("disk[0].Device = %s", c.DiskTargets[0].Device)
	}
	if c.DiskTargets[0].DiskstatName != "nvme0n1" {
		t.Errorf("disk[0].DiskstatName = %s, want nvme0n1", c.DiskTargets[0].DiskstatName)
	}
	if c.BatteryName != "BAT0" {
		t.Errorf("BatteryName = %s", c.BatteryName)
	}
	wantIfaces := []string{"eth0", "wlan0", "br0"}
	if len(c.NetworkInterfaces) != 3 || c.NetworkInterfaces[0] != "eth0" {
		t.Errorf("NetworkInterfaces = %v, want %v", c.NetworkInterfaces, wantIfaces)
	}
	if c.Thresholds.CPUTempCrit != 85 {
		t.Errorf("CPUTempCrit = %f", c.Thresholds.CPUTempCrit)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	withCleanEnv(t)
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Error("expected error on non-numeric PORT")
	}
}

func TestLoadRejectsOutOfRangePort(t *testing.T) {
	withCleanEnv(t)
	t.Setenv("PORT", "0")
	if _, err := Load(); err == nil {
		t.Error("expected error on port 0")
	}
	t.Setenv("PORT", "70000")
	if _, err := Load(); err == nil {
		t.Error("expected error on port 70000")
	}
}

func TestLoadRejectsZeroRefreshInterval(t *testing.T) {
	withCleanEnv(t)
	t.Setenv("REFRESH_INTERVAL_SECONDS", "0")
	if _, err := Load(); err == nil {
		t.Error("expected error on REFRESH_INTERVAL_SECONDS=0")
	}
}

func TestDeriveDiskstatNameStripsDevPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/dev/sdb":      "sdb",
		"/dev/sda1":     "sda1",
		"/dev/nvme0n1":  "nvme0n1",
		"/dev/mapper/x": "x",
		"sdc":           "sdc",
	}
	for in, want := range cases {
		if got := deriveDiskstatName(in); got != want {
			t.Errorf("deriveDiskstatName(%q) = %q, want %q", in, got, want)
		}
	}
}
