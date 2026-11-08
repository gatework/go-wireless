// Package vendors assembles the built-in wireless parser catalog.
package vendors

import (
	"sync"

	"github.com/gatework/go-wireless/parser"
	"github.com/gatework/go-wireless/vendors/h3c"
	"github.com/gatework/go-wireless/vendors/huawei"
)

var builtin = sync.OnceValue(func() *parser.Registry {
	registry, err := parser.NewRegistry(h3c.Profile(), huawei.Profile())
	if err != nil {
		panic(err)
	}
	return registry
})

// NewBuiltinRegistry returns the immutable catalog; profiles own detached views.
func NewBuiltinRegistry() *parser.Registry { return builtin() }
