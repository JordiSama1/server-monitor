// Package collector orquesta la lectura de métricas reales del host
// (/proc, /sys, comandos externos) y las traduce a los structs del paquete
// internal/model. Cada collector es dueño de una sub-categoría de métricas
// y mantiene sólo el estado mínimo necesario para deltas entre lecturas.
//
// Los collectors NO comparten estado y NO son thread-safe; el orquestador
// se encarga de serializar los accesos.
package collector

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jordisama/server-monitor/internal/model"
)

type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (t cpuTimes) total() uint64 {
	return t.user + t.nice + t.system + t.idle + t.iowait + t.irq + t.softirq + t.steal
}

type cpuSnapshot struct {
	overall cpuTimes
	cores   []cpuTimes
}

func parseCPUStat(r io.Reader) (cpuSnapshot, error) {
	var snap cpuSnapshot
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	seenAggregate := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		var t cpuTimes
		dest := []*uint64{&t.user, &t.nice, &t.system, &t.idle, &t.iowait, &t.irq, &t.softirq, &t.steal}
		for i, dst := range dest {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return snap, fmt.Errorf("parse %s field %d: %w", fields[0], i, err)
			}
			*dst = v
		}
		if fields[0] == "cpu" {
			snap.overall = t
			seenAggregate = true
		} else {
			snap.cores = append(snap.cores, t)
		}
	}
	if err := sc.Err(); err != nil {
		return snap, fmt.Errorf("scan /proc/stat: %w", err)
	}
	if !seenAggregate {
		return snap, fmt.Errorf("no aggregate cpu line found in /proc/stat")
	}
	return snap, nil
}

func saturatingSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// computePercent returns the busy percentage between two cpuTimes samples.
// Returns 0 when there is no measurable delta or when curr appears to roll
// back compared to prev (counter anomaly).
func computePercent(prev, curr cpuTimes) float64 {
	totalDelta := saturatingSub(curr.total(), prev.total())
	if totalDelta == 0 {
		return 0
	}
	idleDelta := saturatingSub(curr.idle+curr.iowait, prev.idle+prev.iowait)
	if idleDelta > totalDelta {
		return 0
	}
	busy := totalDelta - idleDelta
	return float64(busy) / float64(totalDelta) * 100.0
}

// readFreqMHz reads scaling_cur_freq (kHz) under /sys for the given logical
// CPU and returns the value in MHz.
func readFreqMHz(sysPath string, cpuID int) (float64, error) {
	p := filepath.Join(sysPath, "devices", "system", "cpu", fmt.Sprintf("cpu%d", cpuID), "cpufreq", "scaling_cur_freq")
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", p, err)
	}
	raw := strings.TrimSpace(string(data))
	khz, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s = %q: %w", p, raw, err)
	}
	return float64(khz) / 1000.0, nil
}

// CPUCollector reads CPU usage and frequency from /proc/stat and
// /sys/devices/system/cpu. It maintains the previous /proc/stat snapshot
// to compute usage as a delta between calls; the first Collect call
// returns 0% for usage but populates per-core IDs and live frequencies.
//
// Per-core temperatures and the aggregate CPU temperature are NOT filled
// in here; they come from the sensors collector and are merged upstream
// by the orchestrator.
//
// CPUCollector is NOT safe for concurrent use.
type CPUCollector struct {
	procPath string
	sysPath  string
	last     *cpuSnapshot
}

// NewCPUCollector returns a CPUCollector reading /proc/stat under procPath
// and CPU frequency files under sysPath. Pass /proc and /sys for live use,
// or fixture paths for tests.
func NewCPUCollector(procPath, sysPath string) *CPUCollector {
	return &CPUCollector{procPath: procPath, sysPath: sysPath}
}

// Collect returns the current CPU snapshot. The first call returns 0%
// usage because no delta is available yet; subsequent calls return the
// percentage between the current sample and the previous one.
func (c *CPUCollector) Collect() (model.CPU, error) {
	statPath := filepath.Join(c.procPath, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return model.CPU{}, fmt.Errorf("read %s: %w", statPath, err)
	}
	snap, err := parseCPUStat(bytes.NewReader(data))
	if err != nil {
		return model.CPU{}, err
	}

	out := model.CPU{
		PerCore: make([]model.CoreCPU, len(snap.cores)),
	}
	if c.last != nil && len(c.last.cores) == len(snap.cores) {
		out.OverallPercent = computePercent(c.last.overall, snap.overall)
		for i := range snap.cores {
			out.PerCore[i].Percent = computePercent(c.last.cores[i], snap.cores[i])
		}
	}
	var freqSum float64
	var freqCount int
	for i := range snap.cores {
		out.PerCore[i].ID = i
		if mhz, err := readFreqMHz(c.sysPath, i); err == nil {
			out.PerCore[i].FreqMHz = mhz
			freqSum += mhz
			freqCount++
		}
	}
	if freqCount > 0 {
		out.FreqMHzAvg = freqSum / float64(freqCount)
	}
	c.last = &snap
	return out, nil
}
