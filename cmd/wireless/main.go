package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gatework/go-wireless/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.Run(ctx, os.Args[1:], cli.Dependencies{})
	stop()
	os.Exit(code)
}
