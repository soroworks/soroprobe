package scval

import (
	"fmt"
	"math/big"
	"strings"
)

// FormatTokenAmount formats val as a decimal number with decimals places after
// the point. val must be a string containing a decimal integer, or one of
// int32, uint32, int64, or *big.Int. It returns ("", false) if decimals is
// negative, if val is not a supported type, or if a string val is not a valid
// integer.
func FormatTokenAmount(val any, decimals int) (string, bool) {
	if decimals < 0 {
		return "", false
	}

	var n *big.Int
	neg := false

	switch v := val.(type) {
	case string:
		s := strings.TrimSpace(v)
		if len(s) > 0 && s[0] == '-' {
			neg = true
			s = s[1:]
		}
		// "-0" normalises to positive zero
		i, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return "", false
		}
		n = i
	case int32:
		if v < 0 {
			neg = true
			n = big.NewInt(-int64(v))
		} else {
			n = big.NewInt(int64(v))
		}
	case uint32:
		n = new(big.Int).SetUint64(uint64(v))
	case int64:
		if v < 0 {
			neg = true
			n = big.NewInt(-v)
		} else {
			n = big.NewInt(v)
		}
	case *big.Int:
		if v == nil {
			return "", false
		}
		if v.Sign() < 0 {
			neg = true
			n = new(big.Int).Neg(v)
		} else {
			n = new(big.Int).Set(v)
		}
	default:
		return "", false
	}

	if decimals == 0 {
		s := n.String()
		if neg && n.Sign() != 0 {
			s = "-" + s
		}
		return s, true
	}

	digits := n.String()
	// Pad to at least decimals+1 digits
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	intPart := digits[:len(digits)-decimals]
	fracPart := digits[len(digits)-decimals:]
	result := fmt.Sprintf("%s.%s", intPart, fracPart)
	if neg && n.Sign() != 0 {
		result = "-" + result
	}
	return result, true
}
