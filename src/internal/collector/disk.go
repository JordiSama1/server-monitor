package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

const sectorBytes = 512

type diskstatCounters struct {
	sectorsRead    uint64
	sectorsWritten uint64
}

type diskIOSnapshot struct {
	counters diskstatCounters
	at       time.Time
}

func parseDiskstats(r io.Reader, devName string) (diskstatCounters, error) {
	var zero diskstatCounters
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 14 || fields[2] != devName {
			continue
		}
		read, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return zero, fmt.Errorf("parse sectors_read for %s: %w", devName, err)
		}
		written, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return zero, fmt.Errorf("parse sectors_written for %s: %w", devName, err)
		}
		return diskstatCounters{sectorsRead: read, sectorsWritten: written}, nil
	}
	if err := sc.Err(); err != nil {
		return zero, fmt.Errorf("scan diskstats: %w", err)
	}
	return zero, fmt.Errorf("device %q not found in diskstats", devName)
}

type smartctlOutput struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
		Messages   []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	Temperature struct {
		Current *int `json:"current"`
	} `json:"temperature"`
	ATASmartAttributes struct {
		Table []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Raw  struct {
				Value int `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
}

// parseSmartctlTemp extracts the disk temperature in °C from `smartctl -j`
// output. Prefers the top-level "temperature.current" field; falls back to
// SMART attribute 194 (Temperature_Celsius).
//
// Returns an error if smartctl reported a non-zero exit_status, if the JSON
// is invalid, or if no temperature source is present in the document.
func parseSmartctlTemp(data []byte) (float64, error) {
	var out smartctlOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, fmt.Errorf("decode smartctl json: %w", err)
	}
	if out.Smartctl.ExitStatus != 0 {
		var errMsg string
		for _, m := range out.Smartctl.Messages {
			if m.Severity == "error" {
				errMsg = m.String
				break
			}
		}
		if errMsg == "" {
			errMsg = fmt.Sprintf("smartctl exit_status=%d", out.Smartctl.ExitStatus)
		}
		return 0, fmt.Errorf("smartctl reported error: %s", errMsg)
	}
	if out.Temperature.Current != nil {
		return float64(*out.Temperature.Current), nil
	}
	for _, attr := range out.ATASmartAttributes.Table {
		if attr.ID == 194 {
			return float64(attr.Raw.Value), nil
		}
	}
	return 0, fmt.Errorf("no temperature source found in smartctl output")
}

func statfsBytes(mountpoint string) (total, available uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mountpoint, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", mountpoint, err)
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

// DiskTarget describes a single mountpoint+device pair to monitor. The
// DiskstatName is the kernel device basename ("sdb", "dm-0") used to look
// up IO counters in /proc/diskstats. Device is the path passed to smartctl
// for temperature.
type DiskTarget struct {
	Mountpoint   string
	Device       string
	DiskstatName string
}

// DiskCollector lee espacio (statfs), tasas de IO (/proc/diskstats con
// delta entre llamadas) y temperatura (smartctl -j) para cada DiskTarget.
//
// La primera llamada a Collect devuelve IO/s = 0 por construcción. Si
// smartctl falla por permisos o no está instalado, la temperatura queda
// en 0 y el resto del Disk se entrega normal (degradación graceful).
//
// La fuente de temperatura es reemplazable vía SetTempProvider para que
// el orquestador pueda envolver smartctl con caché TTL.
type DiskCollector struct {
	procPath string
	targets  []DiskTarget
	lastIO   map[string]diskIOSnapshot
	tempFn   func(device string) float64
}

// NewDiskCollector returns a DiskCollector watching the given targets.
// procPath is used to locate /proc/diskstats; pass /proc for live use.
func NewDiskCollector(procPath string, targets []DiskTarget) *DiskCollector {
	c := &DiskCollector{
		procPath: procPath,
		targets:  targets,
		lastIO:   make(map[string]diskIOSnapshot),
	}
	c.tempFn = c.tempFromSmartctl
	return c
}

// SetTempProvider replaces the disk-temperature source. Pass a function
// that returns 0 when no temperature is available. The orchestrator uses
// this to inject a TTL-cached wrapper around smartctl so that the
// expensive shell-out runs at most once per cache window.
func (c *DiskCollector) SetTempProvider(fn func(device string) float64) {
	if fn == nil {
		c.tempFn = c.tempFromSmartctl
		return
	}
	c.tempFn = fn
}

func (c *DiskCollector) readDiskstats() ([]byte, error) {
	p := filepath.Join(c.procPath, "diskstats")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return data, nil
}

// tempFromSmartctl shells out to smartctl and parses its JSON. Returns 0
// silently if smartctl isn't installed, fails with permission denied, or
// emits a payload without temperature data — disk temperature is best
// effort and we don't want to fail the whole collection because of it.
func (c *DiskCollector) tempFromSmartctl(device string) float64 {
	out, err := exec.Command("smartctl", "-j", "-A", device).Output()
	// smartctl exits non-zero when it can't open the device, but still
	// emits valid JSON on stdout; we only bail when there's nothing to
	// parse (binary missing) or when the parser rejects the payload.
	if err != nil && len(out) == 0 {
		return 0
	}
	temp, err := parseSmartctlTemp(out)
	if err != nil {
		return 0
	}
	return temp
}

// Collect returns the current snapshot for each configured target. Errors
// from /proc/diskstats are fatal (return error); errors from smartctl or
// statfs on a single target degrade that target's fields to zero rather
// than failing the whole collection.
func (c *DiskCollector) Collect() ([]model.Disk, error) {
	raw, err := c.readDiskstats()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]model.Disk, 0, len(c.targets))
	for _, tgt := range c.targets {
		d := model.Disk{
			Mountpoint: tgt.Mountpoint,
			Device:     tgt.Device,
		}
		if total, avail, err := statfsBytes(tgt.Mountpoint); err == nil {
			d.TotalBytes = total
			d.AvailableBytes = avail
			if total >= avail {
				d.UsedBytes = total - avail
			}
			if total > 0 {
				d.UsedPercent = float64(d.UsedBytes) / float64(total) * 100.0
			}
		}
		curr, err := parseDiskstats(bytes.NewReader(raw), tgt.DiskstatName)
		if err == nil {
			if last, ok := c.lastIO[tgt.DiskstatName]; ok {
				dt := now.Sub(last.at).Seconds()
				if dt > 0 {
					readDelta := saturatingSub(curr.sectorsRead, last.counters.sectorsRead)
					writeDelta := saturatingSub(curr.sectorsWritten, last.counters.sectorsWritten)
					d.IOReadBytesPerSec = float64(readDelta*sectorBytes) / dt
					d.IOWriteBytesPerSec = float64(writeDelta*sectorBytes) / dt
				}
			}
			c.lastIO[tgt.DiskstatName] = diskIOSnapshot{counters: curr, at: now}
		}
		d.TempCelsius = c.tempFn(tgt.Device)
		out = append(out, d)
	}
	return out, nil
}
