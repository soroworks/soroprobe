package scval_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/soroworks/soroprobe/internal/scval"
)

func TestFormatTokenAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		val      any
		decimals int
		want     string
		wantOk   bool
	}{
		{
			name:     "7 decimals standard token balance",
			val:      "100000000",
			decimals: 7,
			want:     "10.0000000",
			wantOk:   true,
		},
		{
			name:     "7 decimals small amount with leading zeros",
			val:      "1",
			decimals: 7,
			want:     "0.0000001",
			wantOk:   true,
		},
		{
			name:     "7 decimals zero",
			val:      "0",
			decimals: 7,
			want:     "0.0000000",
			wantOk:   true,
		},
		{
			name:     "negative value",
			val:      "-100000000",
			decimals: 7,
			want:     "-10.0000000",
			wantOk:   true,
		},
		{
			name:     "negative small amount",
			val:      "-50",
			decimals: 7,
			want:     "-0.0000050",
			wantOk:   true,
		},
		{
			name:     "negative zero",
			val:      "-0",
			decimals: 7,
			want:     "0.0000000",
			wantOk:   true,
		},
		{
			name:     "zero decimals",
			val:      "12345",
			decimals: 0,
			want:     "12345",
			wantOk:   true,
		},
		{
			name:     "zero decimals negative",
			val:      "-12345",
			decimals: 0,
			want:     "-12345",
			wantOk:   true,
		},
		{
			name:     "int32 value",
			val:      int32(70000000),
			decimals: 7,
			want:     "7.0000000",
			wantOk:   true,
		},
		{
			name:     "uint32 value",
			val:      uint32(7),
			decimals: 7,
			want:     "0.0000007",
			wantOk:   true,
		},
		{
			name:     "int64 value",
			val:      int64(-500),
			decimals: 2,
			want:     "-5.00",
			wantOk:   true,
		},
		{
			name:     "big.Int value",
			val:      big.NewInt(150),
			decimals: 2,
			want:     "1.50",
			wantOk:   true,
		},
		{
			name:     "negative decimals rejected",
			val:      "100",
			decimals: -1,
			want:     "",
			wantOk:   false,
		},
		{
			name:     "non integer string rejected",
			val:      "not_a_number",
			decimals: 7,
			want:     "",
			wantOk:   false,
		},
		{
			name:     "boolean type rejected",
			val:      true,
			decimals: 7,
			want:     "",
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := scval.FormatTokenAmount(tt.val, tt.decimals)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
