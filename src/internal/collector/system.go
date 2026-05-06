package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jordisama/server-monitor/internal/model"
)

func parseUptime(data []byte) (uint64, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse uptime %q: %w", fields[0], err)
	}
	if v < 0 {
		v = 0
	}
	return uint64(v), nil
}

type loadavg struct {
	load1m, load5m, load15m float64
}

func parseLoadavg(data []byte) (loadavg, error) {
	var la loadavg
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return la, fmt.Errorf("/proc/loadavg has %d fields, need >= 3", len(fields))
	}
	dest := []*float64{&la.load1m, &la.load5m, &la.load15m}
	for i, dst := range dest {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return la, fmt.Errorf("parse loadavg field %d: %w", i, err)
		}
		*dst = v
	}
	return la, nil
}

func countProcesses(procPath string) (int, error) {
	entries, err := os.ReadDir(procPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", procPath, err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err == nil {
			n++
		}
	}
	return n, nil
}

// SystemCollector lee /proc/uptime, /proc/loadavg y cuenta los dirs
// numéricos en /proc/ para producir un model.System.
//
// Stateless: cada Collect produce un snapshot independiente.
type SystemCollector struct {
	procPath string
}

// NewSystemCollector returns a collector reading the standard /proc files
// under procPath.
func NewSystemCollector(procPath string) *SystemCollector {
	return &SystemCollector{procPath: procPath}
}

// Collect returns the current System snapshot. Both /proc/uptime and
// /proc/loadavg are required; their absence is treated as a fatal error
// because they're trivially present on any Linux host.
func (c *SystemCollector) Collect() (model.System, error) {
	var sys model.System

	upData, err := os.ReadFile(filepath.Join(c.procPath, "uptime"))
	if err != nil {
		return sys, fmt.Errorf("read uptime: %w", err)
	}
	uptime, err := parseUptime(upData)
	if err != nil {
		return sys, err
	}
	sys.UptimeSeconds = uptime

	laData, err := os.ReadFile(filepath.Join(c.procPath, "loadavg"))
	if err != nil {
		return sys, fmt.Errorf("read loadavg: %w", err)
	}
	la, err := parseLoadavg(laData)
	if err != nil {
		return sys, err
	}
	sys.LoadAvg1m = la.load1m
	sys.LoadAvg5m = la.load5m
	sys.LoadAvg15m = la.load15m

	count, err := countProcesses(c.procPath)
	if err != nil {
		return sys, err
	}
	sys.ProcessesCount = count

	return sys, nil
}
