package wireless

import "context"

// ApplySession is borrowed for one owned operation. Implementations must not
// reconnect, retry, save configuration or automatically roll back. Reads must
// return exactly one complete parsed configuration, never partial observations.
type ApplySession interface {
	ReadConfiguration(context.Context) (Record, error)
	Write(context.Context, string) error
}
