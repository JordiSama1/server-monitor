package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

type containerSummary struct {
	State string `json:"State"`
}

// parseContainers reads the response body of /containers/json?all=true
// and returns the running and total counts.
func parseContainers(data []byte) (running, total int, err error) {
	var list []containerSummary
	if err := json.Unmarshal(data, &list); err != nil {
		return 0, 0, fmt.Errorf("decode containers list: %w", err)
	}
	total = len(list)
	for _, c := range list {
		if c.State == "running" {
			running++
		}
	}
	return running, total, nil
}

// DockerCollector consulta el daemon de Docker vía el socket unix
// /var/run/docker.sock. Solo cuenta contenedores corriendo y totales —
// no toca filesystem, configs ni logs. La query es read-only.
//
// Si el socket no existe (Docker no instalado o no montado dentro del
// container), Collect devuelve nil sin error: Docker es opcional, no
// debe romper la colección general.
type DockerCollector struct {
	socketPath string
	client     *http.Client
}

// NewDockerCollector returns a collector that talks to the Docker daemon
// through a unix socket. socketPath is typically "/var/run/docker.sock".
func NewDockerCollector(socketPath string) *DockerCollector {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &DockerCollector{
		socketPath: socketPath,
		client: &http.Client{
			Transport: transport,
			Timeout:   2 * time.Second,
		},
	}
}

// Collect returns the current Docker snapshot. If the socket is missing
// or unreachable the collector degrades to (nil, nil): a server with no
// Docker is a normal state, not a failure to surface.
func (c *DockerCollector) Collect() (out *model.Docker, err error) {
	if _, statErr := os.Stat(c.socketPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", c.socketPath, statErr)
	}

	// The host part of the URL is irrelevant — the dialer always goes to
	// the unix socket — but http.NewRequest demands a syntactically valid one.
	req, err := http.NewRequest(http.MethodGet, "http://docker/containers/json?all=true", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close docker response: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker API returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read docker response: %w", err)
	}
	running, total, err := parseContainers(body)
	if err != nil {
		return nil, err
	}
	return &model.Docker{
		RunningContainers: running,
		TotalContainers:   total,
	}, nil
}
