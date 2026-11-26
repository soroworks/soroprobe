package probe_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

// TestInspectEdgeCasesTable covers empty and invalid inputs to Inspect.
func TestInspectEdgeCasesTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     func() probe.InspectRequest
		wantErr bool
		check   func(t *testing.T, r *probe.InspectResult)
	}{
		{
			name:    "empty contract id is rejected",
			req:     func() probe.InspectRequest { return probe.InspectRequest{ContractID: ""} },
			wantErr: true,
		},
		{
			name:    "invalid contract id is rejected",
			req:     func() probe.InspectRequest { return probe.InspectRequest{ContractID: "nope"} },
			wantErr: true,
		},
		{
			name: "unknown durability is rejected",
			req: func() probe.InspectRequest {
				return probe.InspectRequest{
					ContractID:     stellartest.SACContract,
					DataDurability: health.Durability("forever"),
				}
			},
			wantErr: true,
		},
		{
			name: "no data keys reports no data entries",
			req: func() probe.InspectRequest {
				return probe.InspectRequest{ContractID: stellartest.SACContract}
			},
			wantErr: false,
			check: func(t *testing.T, r *probe.InspectResult) {
				assert.Empty(t, r.Data)
				assert.True(t, r.Deployed)
			},
		},
		{
			name: "estimate extend of zero prices nothing",
			req: func() probe.InspectRequest {
				return probe.InspectRequest{ContractID: stellartest.SACContract, EstimateExtend: 0}
			},
			wantErr: false,
			check: func(t *testing.T, r *probe.InspectResult) {
				assert.Empty(t, r.ExtendEstimates)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := stellartest.NewFake(t)
			p := newProber(t, fake, health.DefaultThresholds)

			result, err := p.Inspect(context.Background(), tt.req())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

// TestInspectTTLBoundaryTable covers TTL status boundaries at the thresholds.
func TestInspectTTLBoundaryTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		offset int64
		want   health.Status
	}{
		{"one beyond the warn threshold is healthy", 120961, health.StatusHealthy},
		{"exactly at the warn threshold warns", 120960, health.StatusWarning},
		{"one beyond the critical threshold warns", 17281, health.StatusWarning},
		{"exactly at the critical threshold is critical", 17280, health.StatusCritical},
		{"expiring this ledger is critical, not expired", 0, health.StatusCritical},
		{"one ledger past is expired", -1, health.StatusExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := stellartest.NewFake(t)
			instanceKey := stellartest.InstanceKeyOf(t, stellartest.SACContract)
			fake.SetTTL(t, instanceKey, uint32(int64(fake.LatestLedger)+tt.offset)) //nolint:gosec // G115: sum stays within ledger range

			p := newProber(t, fake, health.DefaultThresholds)
			result, err := p.Inspect(context.Background(), probe.InspectRequest{
				ContractID: stellartest.SACContract,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.Instance.Status)
			assert.Equal(t, tt.want, result.Status)
		})
	}
}
