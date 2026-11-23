package config_test

import (
	"fmt"

	"github.com/soroworks/soroprobe/internal/config"
)

// ExampleParseLogFormat shows the canonical names ParseLogFormat returns,
// including the empty-name default and the error for an unknown format.
func ExampleParseLogFormat() {
	for _, name := range []string{"", "text", " JSON ", "yaml"} {
		format, err := config.ParseLogFormat(name)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Printf("%q -> %s\n", name, format)
	}
	// Output:
	// "" -> text
	// "text" -> text
	// " JSON " -> json
	// error: unknown log format "yaml" (want text or json)
}
