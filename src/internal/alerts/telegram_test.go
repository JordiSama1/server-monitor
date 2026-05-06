package alerts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramNotifierSendsCorrectPayload(t *testing.T) {
	t.Parallel()
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	n := &TelegramNotifier{
		token:   "testtoken",
		chatID:  "12345",
		client:  srv.Client(),
		baseURL: srv.URL,
	}

	if err := n.Send("hello *world*"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody["chat_id"] != "12345" {
		t.Errorf("chat_id = %q, want 12345", gotBody["chat_id"])
	}
	if gotBody["text"] != "hello *world*" {
		t.Errorf("text = %q, want hello *world*", gotBody["text"])
	}
	if gotBody["parse_mode"] != "Markdown" {
		t.Errorf("parse_mode = %q, want Markdown", gotBody["parse_mode"])
	}
}

func TestTelegramNotifierErrorOnNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	n := &TelegramNotifier{
		token:   "badtoken",
		chatID:  "12345",
		client:  srv.Client(),
		baseURL: srv.URL,
	}

	if err := n.Send("test"); err == nil {
		t.Error("expected error on HTTP 401, got nil")
	}
}
