// Command library shows the parser used as a library: it parses an H3C
// configuration offline and prints the observation as JSON.
package main

import (
	"fmt"
	"os"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
)

func main() {
	parser, err := vendors.NewBuiltinRegistry().Fetch("h3c", "")
	if err != nil {
		panic(err)
	}
	observation, err := parser.Parse(wireless.Configuration, "sysname Example-WLC\nreturn\n", true)
	if err != nil {
		panic(err)
	}
	// Explicit fact export; ordinary formatting exposes metadata only.
	body, err := wireless.MarshalJSONIndent(observation, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stdout, string(body))
}
