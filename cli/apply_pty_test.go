//go:build darwin || linux

package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyRealTTYConfirmationAndCancellation(t *testing.T) {
	for _, cancelInput := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelInput), func(t *testing.T) {
			console, e := expect.NewConsole(expect.WithRaw())
			if e != nil {
				t.Fatal(e)
			}
			defer console.Close()
			plan, before, after := applySetup(t)
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			os.WriteFile(path, plan, 0600)
			sha := fmt.Sprintf("%x", sha256.Sum256(plan))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			session := &applyFixture{before: before, after: after}
			opened := 0
			done := make(chan int, 1)
			go func() {
				done <- Run(ctx, []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: console.Tty(), Output: console.Tty(), Error: console.Tty(), Environment: map[string]string{"NC_DEVICE_USERNAME": "x"}, Discovery: Discovery{Directories: []string{}}, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(wireless.ApplySession) error) (error, error) {
					opened++
					return run(session), nil
				}})
			}()
			if cancelInput {
				time.AfterFunc(30*time.Millisecond, cancel)
			} else {
				if _, e = console.Session().SendLine(context.Background(), "APPLY 192.0.2.10 "+sha[:12]); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case code := <-done:
				if cancelInput {
					if code != 130 || opened != 0 {
						t.Fatal(code, opened)
					}
				} else if code != 0 || opened != 1 || session.writes != 8 {
					t.Fatal(code, opened, session.writes)
				}
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("TTY input did not complete/cancel")
			}
			if _, e := console.Tty().Stat(); e != nil {
				t.Fatal("borrowed terminal closed", e)
			}
		})
	}
}
