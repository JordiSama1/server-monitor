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

const kibToBytes uint64 = 1024

// parseMemInfo reads /proc/meminfo-formatted input and returns the relevant
// fields converted to bytes. MemTotal is required; if it is missing, an
// error is returned because the resulting struct would be meaningless.
func parseMemInfo(r io.Reader) (model.Memory, error) {
	var mem model.Memory
	values := map[string]uint64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := line[:colon]
		rest := strings.TrimSpace(line[colon+1:])
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[key] = v
	}
	if err := sc.Err(); err != nil {
		return mem, fmt.Errorf("scan meminfo: %w", err)
	}
	total, ok := values["MemTotal"]
	if !ok {
		return mem, fmt.Errorf("MemTotal not found in meminfo input")
	}
	available := values["MemAvailable"]
	mem.TotalBytes = total * kibToBytes
	mem.AvailableBytes = available * kibToBytes
	mem.UsedBytes = (total - available) * kibToBytes
	mem.CachedBytes = values["Cached"] * kibToBytes
	mem.BuffersBytes = values["Buffers"] * kibToBytes
	mem.SwapTotalBytes = values["SwapTotal"] * kibToBytes
	swapFree := values["SwapFree"]
	if swapFree > values["SwapTotal"] {
		swapFree = values["SwapTotal"]
	}
	mem.SwapUsedBytes = (values["SwapTotal"] - swapFree) * kibToBytes
	return mem, nil
}

// MemoryCollector lee /proc/meminfo y convierte los campos relevantes a
// bytes. Es stateless: cada llamada produce un snapshot independiente.
type MemoryCollector struct {
	procPath string
}

// NewMemoryCollector returns a MemoryCollector reading meminfo under procPath.
func NewMemoryCollector(procPath string) *MemoryCollector {
	return &MemoryCollector{procPath: procPath}
}

// Collect returns the current memory snapshot.
func (c *MemoryCollector) Collect() (model.Memory, error) {
	p := filepath.Join(c.procPath, "meminfo")
	data, err := os.ReadFile(p)
	if err != nil {
		return model.Memory{}, fmt.Errorf("read %s: %w", p, err)
	}
	return parseMemInfo(bytes.NewReader(data))
}
