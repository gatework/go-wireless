package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/bootstrap"
)

func TestDefaultOfflineReviewAndNoOutputOnInvalidInput(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "legacy", false: "invalid"}[valid], func(t *testing.T) {
			batch := t.TempDir()
			os.Mkdir(filepath.Join(batch, "devices"), 0700)
			body := `{"device":{"name":"old","host":"192.0.2.1","platform":"h3c_wireless"},"snapshot":{"dimensions":{"configuration":{"status":"failed","records":[]}}}}`
			if !valid {
				body = strings.Replace(body, `"records":[]`, `"records":null`, 1)
			}
			os.WriteFile(filepath.Join(batch, "devices/x.json"), []byte(body), 0600)
			out := filepath.Join(batch, "review-output")
			var output, errs bytes.Buffer
			code := Run(context.Background(), []string{"review", "--batch", batch, "--output", out}, Dependencies{Output: &output, Error: &errs, Environment: map[string]string{}, Discovery: bootstrap.Discovery{Directories: []string{}}})
			if valid {
				if code != 0 {
					t.Fatal(code, errs.String())
				}
				files, _ := filepath.Glob(filepath.Join(out, "*", "review.json"))
				if len(files) != 1 {
					t.Fatal(files)
				}
				plans, _ := filepath.Glob(filepath.Join(out, "*", "recommendations", "*.plan.json"))
				if len(plans) != 0 {
					t.Fatal(plans)
				}
			} else {
				if code != 2 {
					t.Fatal(code)
				}
				if _, e := os.Stat(out); !os.IsNotExist(e) {
					t.Fatal("output created before validation")
				}
			}
		})
	}
}
