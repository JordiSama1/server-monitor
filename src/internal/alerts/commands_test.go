package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

func makeDigest(n Notifier) *DailyDigest {
	return NewDailyDigest(n, func() (model.MetricsSnapshot, error) {
		return model.MetricsSnapshot{
			CPU:    model.CPU{OverallPercent: 10, TempCelsius: 40},
			Memory: model.Memory{TotalBytes: 8 * 1024 * 1024 * 1024, UsedBytes: 2 * 1024 * 1024 * 1024},
			System: model.System{UptimeSeconds: 3600},
		}, nil
	}, 10, "http://localhost", "America/Santiago")
}

func TestCommandHandlerDispatchesStatus(t *testing.T) {
	n := &captureNotifier{}
	d := makeDigest(n)

	// Serve one /status update then an empty response to stop the loop
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if call == 0 {
			call++
			resp := tgUpdatesResponse{
				OK: true,
				Result: []tgUpdate{
					{
						UpdateID: 1,
						Message: &struct {
							Text string `json:"text"`
							Chat struct {
								ID int64 `json:"id"`
							} `json:"chat"`
						}{Text: "/status"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Subsequent calls: empty — let context cancel stop the loop
		_ = json.NewEncoder(w).Encode(tgUpdatesResponse{OK: true})
	}))
	defer srv.Close()

	h := &CommandHandler{
		token:  "tok",
		digest: d,
		client: srv.Client(),
		offset: 0,
	}
	// Override poll URL to test server
	origPoll := h.poll
	_ = origPoll // h.poll is a method, not overrideable directly — test via Run with short ctx

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Replace client transport to redirect to test server
	h.client.Transport = &hostRewriter{target: srv.URL}

	h.Run(ctx)

	if n.count() != 1 {
		t.Errorf("expected 1 digest sent for /status, got %d", n.count())
	}
}

func TestCommandHandlerIgnoresUnknownCommands(t *testing.T) {
	n := &captureNotifier{}
	d := makeDigest(n)

	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if call == 0 {
			call++
			resp := tgUpdatesResponse{
				OK: true,
				Result: []tgUpdate{
					{
						UpdateID: 1,
						Message: &struct {
							Text string `json:"text"`
							Chat struct {
								ID int64 `json:"id"`
							} `json:"chat"`
						}{Text: "/unknown"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(tgUpdatesResponse{OK: true})
	}))
	defer srv.Close()

	h := &CommandHandler{token: "tok", digest: d, client: srv.Client()}
	h.client.Transport = &hostRewriter{target: srv.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	h.Run(ctx)

	if n.count() != 0 {
		t.Errorf("expected 0 messages for unknown command, got %d", n.count())
	}
}

func TestCommandHandlerStatusWithBotSuffix(t *testing.T) {
	n := &captureNotifier{}
	d := makeDigest(n)

	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if call == 0 {
			call++
			resp := tgUpdatesResponse{
				OK: true,
				Result: []tgUpdate{
					{
						UpdateID: 1,
						Message: &struct {
							Text string `json:"text"`
							Chat struct {
								ID int64 `json:"id"`
							} `json:"chat"`
						}{Text: "/status@jordisama_server_bot"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(tgUpdatesResponse{OK: true})
	}))
	defer srv.Close()

	h := &CommandHandler{token: "tok", digest: d, client: srv.Client()}
	h.client.Transport = &hostRewriter{target: srv.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	h.Run(ctx)

	if n.count() != 1 {
		t.Errorf("/status@bot should trigger digest, got %d messages", n.count())
	}
}

// hostRewriter redirects all requests to the given target host.
type hostRewriter struct {
	target string
}

func (hr *hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	base := strings.TrimPrefix(hr.target, "http://")
	r2.URL.Scheme = "http"
	r2.URL.Host = base
	return http.DefaultTransport.RoundTrip(r2)
}
