package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/config"
	"github.com/soroworks/soroprobe/internal/health"
)

func TestParseThresholdDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantLedgers uint32
		wantErr     bool
	}{
		{
			name:        "7 days",
			input:       "7d",
			wantLedgers: uint32(7 * 24 * time.Hour / health.LedgerCloseTime), // 120960
		},
		{
			name:        "36 hours",
			input:       "36h",
			wantLedgers: uint32(36 * time.Hour / health.LedgerCloseTime), // 25920
		},
		{
			name:        "days and hours combined",
			input:       "1d12h",
			wantLedgers: uint32(36 * time.Hour / health.LedgerCloseTime), // 25920
		},
		{
			name:        "hours and minutes",
			input:       "2h30m",
			wantLedgers: uint32(150 * time.Minute / health.LedgerCloseTime), // 1800
		},
		{
			name:        "5 seconds (1 ledger)",
			input:       "5s",
			wantLedgers: 1,
		},
		{
			name:        "0 seconds (0 ledgers)",
			input:       "0s",
			wantLedgers: 0,
		},
		{
			name:    "empty string error",
			input:   "",
			wantErr: true,
		},
		{
			name:    "negative duration error",
			input:   "-5m",
			wantErr: true,
		},
		{
			name:    "invalid unit error",
			input:   "7x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.ParseThresholdDuration(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantLedgers, got)
			}
		})
	}
}
