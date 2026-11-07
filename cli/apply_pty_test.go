//go:build darwin || linux

package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/gatework/go-expect"
	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
)

func TestApplyRealTTYConfirmationAndCancellation(t *testing.T) {
	for _, cancelInput := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelInput), func(t *testing.T) {
			waitCtx, stopWaiting := context.WithTimeout(t.Context(), 3*time.Second)
			defer stopWaiting()
			ctx, cancel := context.WithCancel(waitCtx)
			defer cancel()
			console, e := expect.NewConsole(expect.WithRaw())
			if e != nil {
				t.Fatal(e)
			}
			var finished <-chan struct{}
			t.Cleanup(func() {
				cancel()
				if err := console.Close(); err != nil {
					t.Error(err)
				}
				if finished != nil {
					select {
					case <-finished:
					case <-time.After(3 * time.Second):
						t.Error("apply did not release the borrowed TTY")
					}
				}
			})
			plan, before, after := applySetup(t)
			root := t.TempDir()
			path := filepath.Join(root, "plan.json")
			if e := os.WriteFile(path, plan, 0600); e != nil {
				t.Fatal(e)
			}
			sha := fmt.Sprintf("%x", sha256.Sum256(plan))
			session := &applyFixture{before: before, after: after}
			opened := 0
			done := make(chan int, 1)
			runDone := make(chan struct{})
			finished = runDone
			go func() {
				defer close(runDone)
				done <- Run(ctx, []string{"apply", "--plan", path, "--directory", filepath.Join(root, "logs")}, Dependencies{Input: console.TTY(), Output: console.TTY(), Error: console.TTY(), Environment: map[string]string{"NC_DEVICE_USERNAME": "x"}, Discovery: Discovery{Directories: []string{}}, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, wireless.ApplySession) error) (error, error) {
					opened++
					return run(ctx, session), nil
				}})
			}()
			if _, e := console.Session().Expect(waitCtx, []expect.Case{{Pattern: expect.Literal("其他输入取消：")}}); e != nil {
				t.Fatal("confirmation prompt was not displayed", e)
			}
			if cancelInput {
				cancel()
			} else {
				if _, e = console.Session().SendLine(waitCtx, "APPLY 192.0.2.10 "+sha[:12]); e != nil {
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
			case <-waitCtx.Done():
				t.Fatal("TTY input did not complete/cancel")
			}
			if _, e := console.TTY().Stat(); e != nil {
				t.Fatal("borrowed terminal closed", e)
			}
		})
	}
}
