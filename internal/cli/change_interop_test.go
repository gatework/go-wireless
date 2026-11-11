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
	"time"

	"github.com/gatework/go-wireless/internal/bootstrap"

	"github.com/gatework/go-net-connector/connector"
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/change"
	"github.com/gatework/go-wireless/collect"
)

type configSession struct{ body string }

func (s configSession) State(context.Context) (bool, error) { return true, nil }
func (s configSession) Execute(_ context.Context, cmd string) (collect.Receipt, error) {
	body := ""
	switch cmd {
	case "screen-length disable":
	case "display current-configuration":
		body = s.body
	default:
		return collect.Receipt{}, fmt.Errorf("unexpected query")
	}
	return collect.NewReceipt(collect.ReceiptOptions{Attempted: true, Command: cmd, Raw: cmd + "\n" + body + "<WLC>", Body: body, Steps: 1, Success: true}), nil
}
func collectChangeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source, e := os.ReadFile("../../testdata/ruby-interop/source.cfg")
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, "wireless.yml"), "platform: h3c_wireless\ndirectory: reports\ndimensions: [configuration]\ndevices: [{host: '192.0.2.10', name: Synthetic-WLC, model: WX3540}]\n")
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"collect", "--scripts", "--json"}, Dependencies{Environment: map[string]string{"NC_DEVICE_USERNAME": "fixture"}, Input: strings.NewReader(""), Output: &out, Error: &errs, Discovery: discovery(root), PrepareResources: func(context.Context, int) error { return nil }, SessionFactory: func(context.Context, wireless.Record, connector.ConnectionConfig) (bootstrap.Connection, error) {
		return bootstrap.Connection{Platform: "h3c_wireless", Open: func(ctx context.Context, run func(collect.Session) error) error {
			return run(configSession{body: string(source)})
		}}, nil
	}})
	if code != 0 {
		t.Fatal(code, errs.String())
	}
	summary, e := wireless.DecodeJSONDocument(out.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	batch := summary["directory"].(string)
	plans, e := filepath.Glob(filepath.Join(batch, "recommendations", "*.plan.json"))
	if e != nil || len(plans) != 1 {
		t.Fatal(plans, e)
	}
	return batch, plans[0]
}
func TestCollectReviewApplyFullFingerprintRoundtrip(t *testing.T) {
	batch, collectPlan := collectChangeFixture(t)
	var out, errs bytes.Buffer
	root := t.TempDir()
	if code := Run(context.Background(), []string{"review", "--batch", batch, "--scripts", "--output", filepath.Join(root, "review")}, Dependencies{Output: &out, Error: &errs}); code != 0 {
		t.Fatal(code, errs.String())
	}
	reviewPlans, _ := filepath.Glob(filepath.Join(root, "review", "*", "recommendations", "*.plan.json"))
	if len(reviewPlans) != 1 {
		t.Fatal(reviewPlans)
	}
	for _, path := range []string{collectPlan, reviewPlans[0]} {
		body, _ := os.ReadFile(path)
		plan, e := wireless.DecodeJSONDocument(body)
		if e != nil {
			t.Fatal(e)
		}
		if plan["expected_config_sha256"] != "294aea47c2657702a541a4d791b3376d5a96c3a9bcadbda1a606d64545d5d573" {
			t.Fatal(plan["expected_config_sha256"])
		}
		source, _ := os.ReadFile(plan["source"].(string))
		if fmt.Sprintf("%x", sha256.Sum256(source)) != plan["source_sha256"] {
			t.Fatal("raw source SHA changed")
		}
		_, before, after := applySetup(t)
		session := &applyFixture{before: before, after: after}
		hash := fmt.Sprintf("%x", sha256.Sum256(body))
		code := Run(context.Background(), []string{"apply", "--plan", path, "--directory", filepath.Join(root, "executions")}, Dependencies{Input: strings.NewReader("APPLY 192.0.2.10 " + hash[:12] + "\n"), Output: &out, Error: &errs, Terminal: func(any) bool { return true }, Environment: map[string]string{"NC_DEVICE_USERNAME": "x"}, Discovery: bootstrap.Discovery{Directories: []string{}}, ApplyFactory: func(ctx context.Context, _ wireless.Record, _ connector.ConnectionConfig, run func(context.Context, change.ApplySession) error) (error, error) {
			return run(ctx, session), nil
		}})
		if code != 0 || session.writes != 8 {
			t.Fatal(code, session.writes, errs.String())
		}
	}
}
func TestRubyChangeInterop(t *testing.T) {
	reference := os.Getenv("WIRELESS_RUBY_SOURCE")
	if reference == "" {
		t.Skip("optional read-only Ruby oracle; pure Go fixtures run by default")
	}
	ruby := os.Getenv("WIRELESS_RUBY")
	if ruby == "" {
		ruby = "ruby"
	}
	batch, plan := collectChangeFixture(t)
	script, _ := filepath.Abs("../../scripts/check-ruby-change-interop.rb")
	source, _ := filepath.Abs("../../testdata/ruby-interop/source.cfg")
	cmd, _ := testCommand(t, 2*time.Minute, ruby, "-S", "bundle", "exec", ruby, script, batch, plan, source, t.TempDir())
	cmd.Dir = reference
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(ruby)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	} else {
		t.Log(strings.TrimSpace(string(b)))
	}
}
