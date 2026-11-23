package probe_test

import (
	"fmt"
	"math/big"

	"github.com/soroworks/soroprobe/internal/probe"
)

// ExampleFormatUnits renders raw token amounts using the token's decimals,
// trimming trailing zeros from the fractional part.
func ExampleFormatUnits() {
	fmt.Println(probe.FormatUnits(big.NewInt(125_000_000), 7))
	fmt.Println(probe.FormatUnits(big.NewInt(10_000_000), 7))
	fmt.Println(probe.FormatUnits(big.NewInt(5), 7))
	fmt.Println(probe.FormatUnits(big.NewInt(-2_500_000), 6))
	fmt.Println(probe.FormatUnits(big.NewInt(42), 0))
	// Output:
	// 12.5
	// 1
	// 0.0000005
	// -2.5
	// 42
}
