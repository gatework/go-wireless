package bootstrap

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func options(t *testing.T, body string, env map[string]string) (*Options, error) {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "wireless.yml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return LoadConfiguration(Request{}, env, Discovery{CWD: d, Home: d, Program: filepath.Join(d, "bin/wireless"), Directories: []string{d}}, nil)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
