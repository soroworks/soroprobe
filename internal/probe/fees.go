package probe

import (
	"fmt"
	"math"
	"strconv"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
)

// stroopsPerXLM converts stroops to XLM.
const stroopsPerXLM = 10_000_000

// FeeOption is one recommended inclusion fee and the total it gives.
type FeeOption struct {
	// Level is "low", "median" or "high".
	Level string `json:"level"`
	// Percentile is the getFeeStats percentile the level reads.
	Percentile int `json:"percentile"`
	// InclusionFee is the recommended inclusion fee in stroops.
	InclusionFee int64 `json:"inclusion_fee_stroops"`
	// TotalFee is the resource fee plus InclusionFee, in stroops.
	TotalFee int64 `json:"total_fee_stroops"`
	// TotalXLM is TotalFee in XLM, as a decimal string.
	TotalXLM string `json:"total_xlm"`
}

// FeeRecommendation turns recent Soroban inclusion fees into the fee to
// offer for a simulated call.
type FeeRecommendation struct {
	// ResourceFee is the simulated resource fee in stroops.
	ResourceFee int64       `json:"resource_fee_stroops"`
	Options     []FeeOption `json:"options"`
	// Source says where the inclusion fees come from.
	Source string `json:"source"`
	// LedgerCount and TransactionCount describe the sample the
	// percentiles were taken over; LatestLedger is its newest ledger.
	LedgerCount      uint32 `json:"ledger_count"`
	TransactionCount uint32 `json:"transaction_count"`
	LatestLedger     uint32 `json:"latest_ledger"`
	// Note explains a recommendation that falls back to the base fee.
	Note string `json:"note,omitempty"`
}

// feeLevels are the percentiles the recommendation offers.
var feeLevels = []struct {
	level      string
	percentile int
	read       func(protocol.FeeDistribution) uint64
}{
	{"low", 10, func(d protocol.FeeDistribution) uint64 { return d.P10 }},
	{"median", 50, func(d protocol.FeeDistribution) uint64 { return d.P50 }},
	{"high", 90, func(d protocol.FeeDistribution) uint64 { return d.P90 }},
}

// RecommendFees builds the fee recommendation for a call with the given
// resource fee from a getFeeStats response. It does no I/O, so it can be
// tested with fixed inputs.
//
// No inclusion fee is offered below baseFee, the network minimum per
// operation: an empty or quiet network reports zero percentiles, and a
// transaction offering less than the minimum is rejected outright.
func RecommendFees(resourceFee, baseFee int64, stats protocol.GetFeeStatsResponse) FeeRecommendation {
	if baseFee <= 0 {
		baseFee = DefaultBaseFee
	}
	dist := stats.SorobanInclusionFee
	rec := FeeRecommendation{
		ResourceFee:      resourceFee,
		Source:           "getFeeStats sorobanInclusionFee",
		LedgerCount:      dist.LedgerCount,
		TransactionCount: dist.TransactionCount,
		LatestLedger:     stats.LatestLedger,
	}
	if dist.TransactionCount == 0 {
		rec.Note = fmt.Sprintf("no Soroban transactions in the sampled ledgers; the minimum inclusion fee (%d stroops) is offered", baseFee)
	}
	for _, l := range feeLevels {
		fee := baseFee
		if v := l.read(dist); v <= math.MaxInt64 && int64(v) > fee {
			fee = int64(v)
		}
		total := resourceFee + fee
		rec.Options = append(rec.Options, FeeOption{
			Level:        l.level,
			Percentile:   l.percentile,
			InclusionFee: fee,
			TotalFee:     total,
			TotalXLM:     FormatXLM(total),
		})
	}
	return rec
}

// FormatXLM renders stroops as XLM with all seven decimals.
func FormatXLM(stroops int64) string {
	sign := ""
	if stroops < 0 {
		sign, stroops = "-", -stroops
	}
	whole, frac := stroops/stroopsPerXLM, stroops%stroopsPerXLM
	return sign + strconv.FormatInt(whole, 10) + "." + fmt.Sprintf("%07d", frac)
}
