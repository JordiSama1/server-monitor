package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileServerServesIndex(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(FileServer())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "server-monitor") {
		t.Errorf("body must contain 'server-monitor', got: %s", body)
	}
}

func TestFileServerReturns404OnMissingAsset(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(FileServer())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/no-such-asset.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
