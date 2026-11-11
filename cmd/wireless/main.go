// Command wireless collects evidence from H3C and Huawei wireless
// controllers, reviews it into a risk assessment with review-only
// remediation, and applies one confirmed plan at a time.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gatework/go-wireless/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.Run(ctx, os.Args[1:], cli.Dependencies{})
	stop()
	os.Exit(code)
}
