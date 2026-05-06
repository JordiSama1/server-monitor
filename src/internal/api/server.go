// Package api expone el contrato HTTP del server-monitor: endpoints
// REST para snapshots one-shot, SSE para streaming, healthcheck para
// el orchestrator del container y middleware genérico (logging, CORS,
// recovery).
//
// El paquete depende de un SnapshotProvider, no del collector concreto,
// así los tests inyectan fakes y el wiring real vive en cmd/server.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/jordisama/server-monitor/internal/model"
)

// SnapshotProvider produces the current MetricsSnapshot. The collector
// Snapshotter satisfies this; tests use fakes.
type SnapshotProvider interface {
	Snapshot() (model.MetricsSnapshot, error)
}

// Server cablea el router HTTP y guarda los hooks al SnapshotProvider,
// el intervalo del SSE, opcionalmente los thresholds que se exponen
// vía /api/config, y opcionalmente un handler de static para servir
// el dashboard embebido.
//
// No arranca un net.Listener; cmd/server hace ListenAndServe usando
// Handler().
type Server struct {
	snapshotter     SnapshotProvider
	refreshInterval time.Duration
	thresholds      any
	staticHandler   http.Handler
}

// NewServer wires a Server. refreshInterval controls how often /api/stream
// pushes events; 2s is the recommended production value.
func NewServer(snapshotter SnapshotProvider, refreshInterval time.Duration) *Server {
	return &Server{snapshotter: snapshotter, refreshInterval: refreshInterval}
}

// SetThresholds enables the /api/config endpoint, which serializes the
// passed value as JSON for the dashboard to consume on load. Pass nil to
// disable the endpoint.
func (s *Server) SetThresholds(t any) {
	s.thresholds = t
}

// SetStaticHandler mounts a static-asset handler under the catch-all
// route. The dashboard's index.html ends up at /, the rest under /<path>.
// Pass nil to disable; in that case the server only exposes the API.
func (s *Server) SetStaticHandler(h http.Handler) {
	s.staticHandler = h
}

// Handler returns the configured chi router. Compose with http.Server in
// the caller; this lets cmd/server own timeouts and addr binding.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/healthz", s.handleHealthz)
	r.Get("/api/metrics", s.handleMetrics)
	r.Get("/api/stream", s.handleStream)
	r.Post("/api/processes/{pid}/kill", s.handleKillProcess)
	if s.thresholds != nil {
		r.Get("/api/config", s.handleConfig)
	}
	if s.staticHandler != nil {
		r.Handle("/*", s.staticHandler)
	}
	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	snap, err := s.snapshotter.Snapshot()
	if err != nil {
		log.Printf("snapshot error (metrics): %v", err)
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.thresholds)
}

// handleStream pushes a JSON snapshot every refreshInterval until the
// client disconnects (ctx cancellation). Each event uses standard SSE
// framing: "data: <json>\n\n".
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	push := func() bool {
		snap, err := s.snapshotter.Snapshot()
		if err != nil {
			log.Printf("snapshot error (stream): %v", err)
		}
		data, err := json.Marshal(snap)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !push() {
		return
	}

	ticker := time.NewTicker(s.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !push() {
				return
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Body is partially written; nothing useful left to do.
		return
	}
}

// handleKillProcess sends SIGTERM to the given PID. Rejects system PIDs
// (< 100), the monitor's own PID, and processes the current user cannot
// signal. Returns 200 on success, 400 on bad input, 403 on protected PID,
// 422 on signal failure.
func (s *Server) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	pidStr := chi.URLParam(r, "pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "PID inválido"})
		return
	}
	if pid < 100 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "PID de sistema protegido"})
		return
	}
	if pid == os.Getpid() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "No puedes matar el monitor"})
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Proceso no encontrado"})
		return
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("SIGTERM sent to pid %d", pid)
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed", "pid": pidStr})
}

// corsMiddleware permite cualquier origen vía GET, POST y OPTIONS. POST
// es necesario para el endpoint /api/processes/{pid}/kill.
// para que el dashboard sirviendo desde otro host (tablet, otro tab)
// pueda consumir /api/metrics y /api/stream sin extra config. POSTs no
// existen, así que CSRF no aplica.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
