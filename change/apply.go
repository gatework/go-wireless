package change

import (
	"context"

	wireless "github.com/gatework/go-wireless"
)

// ApplySession is borrowed for one owned operation. Implementations must not
// reconnect, retry, save configuration or automatically roll back. Reads must
// return exactly one complete parsed configuration, never partial observations.
//
//nolint:iface // shared contract between bootstrap, which opens sessions, and the apply workflow
type ApplySession interface {
	ReadConfiguration(ctx context.Context) (wireless.Record, error)
	Write(ctx context.Context, command string) error
}
