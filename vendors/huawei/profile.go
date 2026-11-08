// Package huawei parses Huawei wireless controller evidence.
package huawei

import (
	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

// Profile declares the supported Huawei query responses.
func Profile() parser.ProfileSpec {
	spec := parser.ProfileSpec{Platform: "huawei", ConnectorPlatform: "huawei", Preparation: "screen-length 0 temporary"}
	for _, query := range []parser.Query{{Command: "display ap all", Dimensions: []wireless.Dimension{wireless.AccessPoints}}, {Command: "display station all", Dimensions: []wireless.Dimension{wireless.Clients}}} {
		name := query.Dimensions[0]
		spec.Queries = append(spec.Queries, parser.QuerySpec{Query: query, Parse: func(text string, complete bool) map[wireless.Dimension]parser.Result {
			return map[wireless.Dimension]parser.Result{name: parseHuawei(name, text, complete)}
		}})
	}
	return spec
}
