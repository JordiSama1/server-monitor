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
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

type netCounters struct {
	rxBytes uint64
	txBytes uint64
}

type netSnapshot struct {
	counters netCounters
	at       time.Time
}

// parseNetDev reads /proc/net/dev-formatted input and returns the byte
// counters indexed by interface name. The two header lines are skipped
// implicitly because they don't contain a colon-prefixed interface name.
func parseNetDev(r io.Reader) (map[string]netCounters, error) {
	out := make(map[string]netCounters)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		if name == "" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 16 {
			continue
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rx for %s: %w", name, err)
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse tx for %s: %w", name, err)
		}
		out[name] = netCounters{rxBytes: rx, txBytes: tx}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan net/dev: %w", err)
	}
	return out, nil
}

func readOperstate(sysPath, iface string) (string, error) {
	p := filepath.Join(sysPath, "class", "net", iface, "operstate")
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// isInterfaceUp maps a /sys/class/net/<iface>/operstate value to the boolean
// the dashboard cares about. "up" is obviously up; "unknown" is treated as
// up because tunnel/loopback interfaces (tailscale0, lo) report unknown
// while clearly carrying traffic.
func isInterfaceUp(operstate string) bool {
	switch operstate {
	case "up", "unknown":
		return true
	default:
		return false
	}
}

// NetworkCollector lee /proc/net/dev y /sys/class/net/<iface>/operstate.
// El parámetro ifaces filtra qué interfaces incluir y en qué orden; si es
// nil o vacío, se devuelven todas las que aparecen en /proc/net/dev (orden
// no garantizado).
//
// La primera llamada devuelve tasas instantáneas (RxBytesPerSec,
// TxBytesPerSec) en 0 por construcción; las siguientes calculan la tasa
// como delta_bytes / delta_segundos.
type NetworkCollector struct {
	procPath string
	sysPath  string
	ifaces   []string
	last     map[string]netSnapshot
}

// NewNetworkCollector returns a collector reading /proc/net/dev under
// procPath and operstate files under sysPath. ifaces is the list of
// interface names to expose, in order; pass nil to expose all.
func NewNetworkCollector(procPath, sysPath string, ifaces []string) *NetworkCollector {
	return &NetworkCollector{
		procPath: procPath,
		sysPath:  sysPath,
		ifaces:   ifaces,
		last:     make(map[string]netSnapshot),
	}
}

// Collect returns the per-interface snapshots for the configured names.
// Interfaces present in the configuration but missing from /proc/net/dev
// are silently dropped (e.g., tailscale0 not running yet).
func (c *NetworkCollector) Collect() ([]model.NetworkIface, error) {
	p := filepath.Join(c.procPath, "net", "dev")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	stats, err := parseNetDev(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	now := time.Now()
	names := c.ifaces
	if len(names) == 0 {
		names = make([]string, 0, len(stats))
		for n := range stats {
			names = append(names, n)
		}
	}

	out := make([]model.NetworkIface, 0, len(names))
	for _, name := range names {
		curr, ok := stats[name]
		if !ok {
			continue
		}
		iface := model.NetworkIface{
			Name:    name,
			RxBytes: curr.rxBytes,
			TxBytes: curr.txBytes,
		}
		if last, ok := c.last[name]; ok {
			dt := now.Sub(last.at).Seconds()
			if dt > 0 {
				rxDelta := saturatingSub(curr.rxBytes, last.counters.rxBytes)
				txDelta := saturatingSub(curr.txBytes, last.counters.txBytes)
				iface.RxBytesPerSec = float64(rxDelta) / dt
				iface.TxBytesPerSec = float64(txDelta) / dt
			}
		}
		c.last[name] = netSnapshot{counters: curr, at: now}
		if state, err := readOperstate(c.sysPath, name); err == nil {
			iface.IsUp = isInterfaceUp(state)
		}
		out = append(out, iface)
	}
	return out, nil
}
