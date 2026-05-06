package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

// fakeSnapshotter returns a fixed snapshot and counts calls. Used in
// every test; lets us assert about Server behavior without spinning up
// the real collector tree.
type fakeSnapshotter struct {
	snap  model.MetricsSnapshot
	err   error
	count int64
}

func (f *fakeSnapshotter) Snapshot() (model.MetricsSnapshot, error) {
	atomic.AddInt64(&f.count, 1)
	return f.snap, f.err
}

func newTestServer(t *testing.T, snap *fakeSnapshotter, refresh time.Duration) *httptest.Server {
	t.Helper()
	if refresh == 0 {
		refresh = 50 * time.Millisecond
	}
	s := NewServer(snap, refresh)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func sampleSnapshot() model.MetricsSnapshot {
	return model.MetricsSnapshot{
		Timestamp: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
		CPU:       model.CPU{OverallPercent: 25.0, FreqMHzAvg: 2400},
		Memory:    model.Memory{TotalBytes: 8 * 1024 * 1024 * 1024},
		System:    model.System{UptimeSeconds: 3600},
	}
}

func TestHealthzReturns200WithStatusOK(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, &fakeSnapshotter{snap: sampleSnapshot()}, 0)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want ok", body["status"])
	}
}

func TestMetricsReturnsValidSnapshot(t *testing.T) {
	t.Parallel()
	want := sampleSnapshot()
	srv := newTestServer(t, &fakeSnapshotter{snap: want}, 0)
	resp, err := http.Get(srv.URL + "/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var got model.MetricsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("timestamp diverged: got %v, want %v", got.Timestamp, want.Timestamp)
	}
	if got.CPU.OverallPercent != want.CPU.OverallPercent {
		t.Errorf("CPU diverged: got %+v", got.CPU)
	}
}

func TestCORSHeadersPresent(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, &fakeSnapshotter{snap: sampleSnapshot()}, 0)
	resp, err := http.Get(srv.URL + "/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q, want *", got)
	}
}

func TestCORSPreflightOPTIONS(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, &fakeSnapshotter{snap: sampleSnapshot()}, 0)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", resp.StatusCode)
	}
}

func TestStreamPushesEventsPeriodically(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{snap: sampleSnapshot()}
	srv := newTestServer(t, fake, 30*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	rd := bufio.NewReader(resp.Body)
	events := 0
	for events < 3 {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "data:") {
			events++
		}
	}
	if events < 2 {
		t.Errorf("got %d data events, want >= 2", events)
	}
	if atomic.LoadInt64(&fake.count) < 2 {
		t.Errorf("snapshotter called %d times, want >= 2", fake.count)
	}
}

func TestStreamStopsOnClientDisconnect(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{snap: sampleSnapshot()}
	srv := newTestServer(t, fake, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(resp.Body)
	// Wait for the first event to confirm streaming started, then cancel.
	if _, err := rd.ReadString('\n'); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	cancel()
	// Drain — server should close shortly after we cancel.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func TestStreamEventDataIsValidSnapshot(t *testing.T) {
	t.Parallel()
	want := sampleSnapshot()
	fake := &fakeSnapshotter{snap: want}
	srv := newTestServer(t, fake, 30*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	rd := bufio.NewReader(resp.Body)
	for i := 0; i < 5; i++ {
		line, err := rd.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var got model.MetricsSnapshot
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Errorf("event payload not JSON: %v\nraw: %s", err, payload)
			return
		}
		if got.CPU.OverallPercent != want.CPU.OverallPercent {
			t.Errorf("event diverged: got %+v", got)
		}
		return
	}
	t.Error("no data event received")
}

func TestUnknownRouteReturns404(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, &fakeSnapshotter{snap: sampleSnapshot()}, 0)
	resp, err := http.Get(srv.URL + "/no/such/route")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestConfigEndpointDisabledByDefault(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, &fakeSnapshotter{snap: sampleSnapshot()}, 0)
	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no thresholds set)", resp.StatusCode)
	}
}

func TestConfigEndpointReturnsJSONWhenSet(t *testing.T) {
	t.Parallel()
	s := NewServer(&fakeSnapshotter{snap: sampleSnapshot()}, 50*time.Millisecond)
	s.SetThresholds(map[string]float64{
		"cpu_temp_warn": 70.0,
		"cpu_temp_crit": 80.0,
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["cpu_temp_warn"] != 70 || body["cpu_temp_crit"] != 80 {
		t.Errorf("body = %+v", body)
	}
}

func TestStaticHandlerServesAtRoot(t *testing.T) {
	t.Parallel()
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "static:%s", r.URL.Path)
	})
	s := NewServer(&fakeSnapshotter{snap: sampleSnapshot()}, 50*time.Millisecond)
	s.SetStaticHandler(stub)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/anything")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "static:/anything" {
		t.Errorf("body = %q, static handler not invoked", body)
	}
}

func TestStaticHandlerDoesNotShadowAPI(t *testing.T) {
	t.Parallel()
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("static-fallback"))
	})
	s := NewServer(&fakeSnapshotter{snap: sampleSnapshot()}, 50*time.Millisecond)
	s.SetStaticHandler(stub)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("/api/metrics shadowed by static; CT=%q", ct)
	}
}

func TestRecovererCatchesPanic(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{
		snap: sampleSnapshot(),
		err:  fmt.Errorf("boom"),
	}
	// We don't have a panicking handler in production, so simulate one by
	// constructing a Server with a snapshotter that panics.
	s := NewServer(&panickingSnapshotter{}, 50*time.Millisecond)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	_ = fake
}

type panickingSnapshotter struct{}

func (panickingSnapshotter) Snapshot() (model.MetricsSnapshot, error) {
	panic("simulated handler panic")
}

func TestKillProcessRejectsBadPID(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{}
	srv := newTestServer(t, fake, 0)

	resp, err := http.Post(srv.URL+"/api/processes/abc/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestKillProcessRejectsSystemPID(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{}
	srv := newTestServer(t, fake, 0)

	resp, err := http.Post(srv.URL+"/api/processes/1/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestKillProcessRejectsOwnPID(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{}
	srv := newTestServer(t, fake, 0)

	ownPID := fmt.Sprintf("%d", os.Getpid())
	resp, err := http.Post(srv.URL+"/api/processes/"+ownPID+"/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestKillProcessNonExistentPID(t *testing.T) {
	t.Parallel()
	fake := &fakeSnapshotter{}
	srv := newTestServer(t, fake, 0)

	// PID 999999 almost certainly does not exist
	resp, err := http.Post(srv.URL+"/api/processes/999999/kill", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.StatusCode)
	}
}
