package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/soroworks/soroprobe/internal/health"
)

// ParseThresholdDuration converts a human-readable duration string into a
// ledger count. It accepts the standard Go duration units (h, m, s) plus a
// day unit "d" (24 h). An empty string or a negative value is an error.
func ParseThresholdDuration(s string) (uint32, error) {
	if s == "" {
		return 0, fmt.Errorf("threshold duration must not be empty")
	}

	total, err := parseDayDuration(s)
	if err != nil {
		return 0, err
	}
	if total < 0 {
		return 0, fmt.Errorf("threshold duration must not be negative")
	}

	ledgers := uint32(total / health.LedgerCloseTime) // #nosec G115 -- value validated non-negative above
	if total > 0 && ledgers == 0 {
		ledgers = 1
	}
	return ledgers, nil
}

// dayRe matches one or more "Nd" day tokens at the front of a duration string.
var dayRe = regexp.MustCompile(`^(\d+)d`)

// parseDayDuration parses a duration that may include a "d" day unit in
// addition to Go's standard units.
func parseDayDuration(s string) (time.Duration, error) {
	var total time.Duration
	rest := s
	for {
		m := dayRe.FindStringSubmatch(rest)
		if m == nil {
			break
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		total += time.Duration(n) * 24 * time.Hour
		rest = rest[len(m[0]):]
	}
	if rest != "" {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		total += d
	} else if total == 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return total, nil
}
