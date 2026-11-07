package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func discovery(dir string) Discovery {
	return Discovery{CWD: dir, Home: dir, Program: filepath.Join(dir, "bin/wireless"), Directories: []string{dir}}
}
func options(t *testing.T, body string, env map[string]string, args ...string) (*Options, error) {
	t.Helper()
	d := t.TempDir()
	write(t, filepath.Join(d, "wireless.yml"), body)
	return LoadOptions(args, env, discovery(d))
}
