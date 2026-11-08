package change

import (
	"context"

	wireless "github.com/gatework/go-wireless"
)

// ApplySession is borrowed for one owned operation. Implementations must not
// reconnect, retry, save configuration or automatically roll back. Reads must
// return exactly one complete parsed configuration, never partial observations.
type ApplySession interface {
	ReadConfiguration(context.Context) (wireless.Record, error)
	Write(context.Context, string) error
}
