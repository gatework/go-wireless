package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCommand(t *testing.T, timeout time.Duration, name string, args ...string) (*exec.Cmd, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = 2 * time.Second
	return command, ctx
}

func TestSubprocessCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess build")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "wireless")
	build, _ := testCommand(t, 2*time.Minute, "go", "build", "-o", binary, "../../cmd/wireless")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	home := filepath.Join(dir, "home")
	cwd := filepath.Join(dir, "cwd")
	if e := os.MkdirAll(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	run := func(input string, args ...string) (int, string, string) {
		t.Helper()
		command, _ := testCommand(t, 10*time.Second, binary, args...)
		command.Dir = cwd
		command.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=" + os.Getenv("PATH"), "WIRELESS_PARALLEL=invalid"}
		command.Stdin = strings.NewReader(input)
		var out, err strings.Builder
		command.Stdout = &out
		command.Stderr = &err
		e := command.Run()
		code := 0
		if e != nil {
			var ok bool
			if ee, yes := errors.AsType[*exec.ExitError](e); yes {
				code = ee.ExitCode()
				ok = true
			}
			if !ok {
				t.Fatal(e)
			}
		}
		return code, out.String(), err.String()
	}
	for _, args := range [][]string{{"--help"}, {"collect", "--help"}, {"parse", "--help"}, {"review", "--help"}, {"apply", "--help"}, {"--version"}} {
		code, _, err := run("", args...)
		if code != 0 {
			t.Fatal(args, code, err)
		}
	}
	code, out, err := run("sysname WLC\nreturn\n", "parse", "--complete", "--json")
	if code != 0 || !json.Valid([]byte(out)) || err != "" {
		t.Fatal(code, out, err)
	}
	write(t, filepath.Join(cwd, "wireless.yml"), "devices: [{host: '192.0.2.10', name: WLC}]\n")
	code, out, err = run("", "--plan", "-p", "2", "--json")
	if code != 0 || !json.Valid([]byte(out)) || !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatal(code, out, err)
	}
	code, out, err = run("", "collect", "--config", "missing", "--json")
	if code != 2 || out != "" || strings.Contains(err, "invalid") {
		t.Fatal(code, out, err)
	}
	files, e := os.ReadDir(cwd)
	if e != nil || len(files) != 1 {
		t.Fatal(files, e)
	}
}
