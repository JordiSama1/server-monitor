package collector

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestParseContainersMixedStates(t *testing.T) {
	t.Parallel()
	running, total, err := parseContainers(loadFixture(t, "docker_containers_mixed.json"))
	if err != nil {
		t.Fatalf("parseContainers: %v", err)
	}
	if running != 2 {
		t.Errorf("running = %d, want 2", running)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
}

func TestParseContainersRealFixture(t *testing.T) {
	t.Parallel()
	running, total, err := parseContainers(loadFixture(t, "docker_containers_real.json"))
	if err != nil {
		t.Fatalf("parseContainers: %v", err)
	}
	if running != 1 || total != 1 {
		t.Errorf("got running=%d total=%d, want 1/1", running, total)
	}
}

func TestParseContainersEmpty(t *testing.T) {
	t.Parallel()
	running, total, err := parseContainers([]byte("[]"))
	if err != nil {
		t.Fatalf("parseContainers: %v", err)
	}
	if running != 0 || total != 0 {
		t.Errorf("got running=%d total=%d, want 0/0", running, total)
	}
}

func TestParseContainersInvalidJSON(t *testing.T) {
	t.Parallel()
	if _, _, err := parseContainers([]byte("not json")); err == nil {
		t.Error("expected error from invalid JSON")
	}
}

// startUnixDockerStub launches an HTTP server on a unix socket inside a
// tempdir and serves the given fixture for /containers/json. Returns the
// socket path and a cleanup func.
func startUnixDockerStub(t *testing.T, fixture []byte) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	})
	srv := &http.Server{Handler: mux}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(listener)
	}()
	cleanup := func() {
		_ = srv.Close()
		wg.Wait()
	}
	return sock, cleanup
}

func TestDockerCollectorWithUnixSocketStub(t *testing.T) {
	sock, cleanup := startUnixDockerStub(t, loadFixture(t, "docker_containers_mixed.json"))
	defer cleanup()

	c := NewDockerCollector(sock)
	d, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil docker")
	}
	if d.RunningContainers != 2 || d.TotalContainers != 5 {
		t.Errorf("got %+v, want running=2 total=5", d)
	}
}

func TestDockerCollectorReturnsNilWhenSocketAbsent(t *testing.T) {
	t.Parallel()
	c := NewDockerCollector(filepath.Join(t.TempDir(), "no.sock"))
	d, err := c.Collect()
	if err != nil {
		t.Errorf("absent docker should not error, got: %v", err)
	}
	if d != nil {
		t.Errorf("absent docker must return nil, got %+v", d)
	}
}

func TestDockerCollectorAgainstLiveSocket(t *testing.T) {
	const sock = "/var/run/docker.sock"
	if _, err := os.Stat(sock); err != nil {
		t.Skip("no live docker socket")
	}
	c := NewDockerCollector(sock)
	d, err := c.Collect()
	if err != nil {
		t.Skipf("live docker unreachable: %v", err)
	}
	if d == nil {
		t.Skip("live docker reachable but returned nil")
	}
	if d.RunningContainers < 0 || d.TotalContainers < d.RunningContainers {
		t.Errorf("invalid live counts: %+v", d)
	}
}
