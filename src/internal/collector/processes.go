package collector

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jordisama/server-monitor/internal/model"
)

type procTimes struct {
	procJiffies  uint64
	totalJiffies uint64
}

type processSnapshot struct {
	pid         int
	name        string
	rssBytes    uint64
	procJiffies uint64
	startTicks  uint64
}

// userHz is the kernel's clock-tick rate. Standard Linux x86_64 builds
// hard-code USER_HZ=100; reading sysconf(_SC_CLK_TCK) would need cgo.
const userHz uint64 = 100

type procStatFields struct {
	cpuJiffies uint64
	startTicks uint64
}

// parseStatus reads /proc/<pid>/status and returns Name (always required)
// and VmRSS in bytes (zero for kernel threads, which have no userland
// memory). The boolean reports whether Name was found; without Name we
// can't display the row.
func parseStatus(data []byte) (name string, rssBytes uint64, ok bool) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "Name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			ok = true
		case strings.HasPrefix(line, "VmRSS:"):
			fields := strings.Fields(strings.TrimPrefix(line, "VmRSS:"))
			if len(fields) >= 1 {
				if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
					rssBytes = v * 1024
				}
			}
		}
	}
	return name, rssBytes, ok
}

// parseStat reads /proc/<pid>/stat and returns utime+stime (CPU jiffies)
// plus starttime (clock ticks since boot). The comm field can contain
// spaces and parentheses, so per proc(5) we use the LAST closing paren
// to split comm from the rest.
func parseStat(data []byte) (procStatFields, error) {
	var z procStatFields
	s := string(data)
	closingParen := strings.LastIndexByte(s, ')')
	if closingParen < 0 {
		return z, fmt.Errorf("malformed /proc/<pid>/stat: no closing paren")
	}
	rest := strings.TrimSpace(s[closingParen+1:])
	fields := strings.Fields(rest)
	// After the closing paren: state(0), ppid(1), pgrp(2), session(3),
	// tty_nr(4), tpgid(5), flags(6), minflt(7), cminflt(8), majflt(9),
	// cmajflt(10), utime(11), stime(12), cutime(13), cstime(14),
	// priority(15), nice(16), num_threads(17), itrealvalue(18),
	// starttime(19).
	if len(fields) < 20 {
		return z, fmt.Errorf("malformed /proc/<pid>/stat: %d fields after comm, need >= 20", len(fields))
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return z, fmt.Errorf("parse utime: %w", err)
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return z, fmt.Errorf("parse stime: %w", err)
	}
	starttime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return z, fmt.Errorf("parse starttime: %w", err)
	}
	return procStatFields{cpuJiffies: utime + stime, startTicks: starttime}, nil
}

// readPID returns a snapshot for /proc/<pid>/. Returns ok=false on any
// read or parse error — usually because the process exited mid-scan,
// which is normal and should not abort the collection.
func readPID(procPath string, pid int) (processSnapshot, bool) {
	pidDir := filepath.Join(procPath, strconv.Itoa(pid))
	statusData, err := os.ReadFile(filepath.Join(pidDir, "status"))
	if err != nil {
		return processSnapshot{}, false
	}
	statData, err := os.ReadFile(filepath.Join(pidDir, "stat"))
	if err != nil {
		return processSnapshot{}, false
	}
	name, rss, ok := parseStatus(statusData)
	if !ok {
		return processSnapshot{}, false
	}
	st, err := parseStat(statData)
	if err != nil {
		return processSnapshot{}, false
	}
	return processSnapshot{
		pid: pid, name: name, rssBytes: rss,
		procJiffies: st.cpuJiffies, startTicks: st.startTicks,
	}, true
}

func listPIDs(procPath string) ([]int, error) {
	entries, err := os.ReadDir(procPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", procPath, err)
	}
	pids := make([]int, 0, 256)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// ProcessesCollector itera sobre /proc/<pid>/, ordena por RSS desc y
// devuelve los top-N como []model.Process.
//
// CPU% se calcula como delta entre dos llamadas, normalizado a "100 %
// por core" (un proceso saturando dos cores reporta 200 %). La primera
// llamada devuelve 0 % por construcción.
//
// Las jiffies de la llamada anterior se guardan para TODOS los procesos
// vistos (no solo los del top-N actual), de modo que un proceso que
// salía del top y vuelve a entrar al snapshot siguiente reporta CPU%
// correcto sin tener que esperar otra ventana.
//
// Procesos que desaparecen entre el listing y la lectura se descartan
// silenciosamente (es la condición de carrera normal en /proc).
type ProcessesCollector struct {
	procPath string
	topN     int
	last     map[int]procTimes
}

// NewProcessesCollector returns a collector reading /proc under procPath
// and emitting at most topN processes per Collect call.
func NewProcessesCollector(procPath string, topN int) *ProcessesCollector {
	return &ProcessesCollector{
		procPath: procPath,
		topN:     topN,
		last:     make(map[int]procTimes),
	}
}

// Collect returns the top-N processes by RSS. Errors only on hard I/O
// failures: a missing /proc/stat or unreadable /proc directory aborts
// the call; per-pid races are absorbed.
func (c *ProcessesCollector) Collect() ([]model.Process, error) {
	statData, err := os.ReadFile(filepath.Join(c.procPath, "stat"))
	if err != nil {
		return nil, fmt.Errorf("read %s/stat: %w", c.procPath, err)
	}
	cpuSnap, err := parseCPUStat(bytes.NewReader(statData))
	if err != nil {
		return nil, err
	}
	totalJiffies := cpuSnap.overall.total()
	numCores := len(cpuSnap.cores)
	if numCores == 0 {
		numCores = 1
	}

	// Best-effort uptime read for ElapsedSeconds; if it fails the field
	// stays at zero rather than aborting the whole snapshot.
	var uptimeSec uint64
	if up, err := os.ReadFile(filepath.Join(c.procPath, "uptime")); err == nil {
		if v, perr := parseUptime(up); perr == nil {
			uptimeSec = v
		}
	}

	pids, err := listPIDs(c.procPath)
	if err != nil {
		return nil, err
	}

	snaps := make([]processSnapshot, 0, len(pids))
	for _, pid := range pids {
		if s, ok := readPID(c.procPath, pid); ok {
			snaps = append(snaps, s)
		}
	}

	newLast := make(map[int]procTimes, len(snaps))
	for _, s := range snaps {
		newLast[s.pid] = procTimes{procJiffies: s.procJiffies, totalJiffies: totalJiffies}
	}

	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].rssBytes > snaps[j].rssBytes
	})
	if len(snaps) > c.topN {
		snaps = snaps[:c.topN]
	}

	out := make([]model.Process, len(snaps))
	for i, s := range snaps {
		p := model.Process{
			PID:      s.pid,
			Name:     s.name,
			RSSBytes: s.rssBytes,
		}
		if uptimeSec > 0 && s.startTicks > 0 {
			startSec := s.startTicks / userHz
			if startSec < uptimeSec {
				p.ElapsedSeconds = uptimeSec - startSec
			}
		}
		if prev, ok := c.last[s.pid]; ok {
			totalDelta := saturatingSub(totalJiffies, prev.totalJiffies)
			procDelta := saturatingSub(s.procJiffies, prev.procJiffies)
			if totalDelta > 0 {
				p.CPUPercent = float64(procDelta) / float64(totalDelta) * 100.0
			}
		}
		out[i] = p
	}
	c.last = newLast
	return out, nil
}
