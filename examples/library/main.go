package main

import (
	"fmt"
	"os"

	wireless "github.com/gatework/go-wireless"
)

func main() {
	parser, err := wireless.NewConfig("h3c", "")
	if err != nil {
		panic(err)
	}
	observation, err := parser.Parse("sysname Example-WLC\nreturn\n", true)
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
