package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	ncconfig "github.com/gatework/go-net-connector/config"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/internal/input"
)

func TestConfigurationEntryPointsPreserveCancellation(t *testing.T) {
	dir := t.TempDir()
	discovery := Discovery{CWD: dir, Home: dir, Directories: []string{dir}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := map[string]func() error{
		"configuration": func() error {
			_, err := LoadConfiguration(ctx, Request{}, nil, discovery, nil)
			return err
		},
		"initialization load": func() error {
			_, err := LoadConfiguration(ctx, Request{Initialize: true}, nil, discovery, nil)
			return err
		},
		"environment": func() error {
			_, _, err := LoadEnvironment(ctx, nil, "", discovery)
			return err
		},
		"apply preparation": func() error {
			_, err := PrepareApply(ctx, nil, "", discovery, nil, nil, change.Plan{}, 22)
			return err
		},
		"initialize publication": func() error {
			return NewOptions(Request{Initialize: true}, discovery, nil).Initialize(ctx, io.Discard)
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, context.Canceled) {
				t.Fatal("configuration lost cancellation", err)
			}
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancelled initialization wrote files", entries, err)
	}
}

func TestConfigurationBudgetDoesNotLimitEvidenceDocuments(t *testing.T) {
	for _, name := range []string{".env", "wireless.yml", "inventory.yml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(ncconfig.MaxBytes + 1); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if name == "inventory.yml" {
				if err := os.WriteFile(filepath.Join(dir, "wireless.yml"), []byte("netdisco_config: inventory.yml\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err = LoadConfiguration(t.Context(), Request{}, nil, Discovery{CWD: dir, Home: dir, Directories: []string{dir}}, nil)
			if !errors.Is(err, ErrOptions) {
				t.Fatal("configuration accepted a file above the shared budget", err)
			}
			if name == "wireless.yml" {
				body, err := input.Document(t.Context(), path)
				if err != nil || int64(len(body)) != ncconfig.MaxBytes+1 {
					t.Fatal("evidence document inherited the configuration budget", len(body), err)
				}
			}
		})
	}
}
