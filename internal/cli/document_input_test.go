package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/go-wireless/internal/input"

	"golang.org/x/sys/unix"
)

func TestDocumentInputsRejectNonRegularBeforeConfirmationOrPublication(t *testing.T) {
	for _, kind := range []string{"fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "devices", "input.json")
			if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "fifo" {
				err = unix.Mkfifo(path, 0600)
			} else {
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"apply", "--plan", path}, {"review", "--batch", root}, {"parse", "--file", path}} {
				var out bytes.Buffer
				code := Run(t.Context(), args, Dependencies{Input: panicReader{}, Output: &out, Error: &bytes.Buffer{}, Terminal: func(any) bool { return true }})
				if code != 2 || out.Len() != 0 {
					t.Fatalf("%v: code=%d output=%s", args, code, out.String())
				}
			}
		})
	}
}

func TestDocumentInputRetainsLargeExactBytesAndCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.json")
	body := []byte(strings.Repeat("0123456789\r\n", 300000))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "evidence-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	got, err := input.Document(t.Context(), link)
	if err != nil || sha256.Sum256(got) != sha256.Sum256(body) {
		t.Fatal("evidence bytes changed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err = input.Document(ctx, path); err != context.Canceled || got != nil {
		t.Fatal("cancelled read", err)
	}
	for _, args := range [][]string{{"apply", "--plan", path}, {"review", "--batch", root}} {
		// Review must see a selected file before its first cancellation check.
		if args[0] == "review" {
			if err := os.Mkdir(filepath.Join(root, "devices"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path, filepath.Join(root, "devices", "one.json")); err != nil {
				t.Fatal(err)
			}
		}
		var out bytes.Buffer
		code := Run(ctx, args, Dependencies{Input: panicReader{}, Output: &out, Error: &bytes.Buffer{}, Terminal: func(any) bool { return true }})
		if code != 130 || out.Len() != 0 {
			t.Fatal(fmt.Sprint(args), code, out.String())
		}
	}
}
