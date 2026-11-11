package bootstrap

import (
	"context"

	"github.com/gatework/go-net-connector/parallel"
	wireless "github.com/gatework/go-wireless"
)

// Execute runs run for every device on at most workers goroutines and returns
// the first error of a started device.
func Execute(ctx context.Context, workers int, devices []wireless.Record, run func(context.Context, wireless.Record) error) error {
	results, e := parallel.Map(ctx, workers, devices, func(ctx context.Context, d wireless.Record) (struct{}, error) { return struct{}{}, run(ctx, d) })
	for _, r := range results {
		if r.Started && r.Err != nil {
			return r.Err
		}
	}
	return e
}
