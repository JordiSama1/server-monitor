package collector

import (
	"os"
	"path/filepath"
	"testing"
)

// loadFixture reads a file from testdata/<rel> into bytes, failing the test
// if the file is missing. Centralizes fixture loading across collector tests.
func loadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("loadFixture %s: %v", rel, err)
	}
	return data
}
