package probe

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// limitsCacheTTL is how long network limits are reused. They change only
// when validators vote a protocol or config upgrade, so a minute is fresh
// enough and keeps a batch of simulations from re-reading them every call.
const limitsCacheTTL = time.Minute

// limitSettings are the config settings the limits are read from.
var limitSettings = []xdr.ConfigSettingId{
	xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes,
	xdr.ConfigSettingIdConfigSettingContractComputeV0,
	xdr.ConfigSettingIdConfigSettingContractLedgerCostV0,
	xdr.ConfigSettingIdConfigSettingContractEventsV0,
	xdr.ConfigSettingIdConfigSettingContractBandwidthV0,
	xdr.ConfigSettingIdConfigSettingContractDataKeySizeBytes,
	xdr.ConfigSettingIdConfigSettingContractDataEntrySizeBytes,
	xdr.ConfigSettingIdConfigSettingContractLedgerCostExtV0,
}

// Limit is one network limit.
type Limit struct {
	// Name identifies the limit, such as "tx_max_instructions".
	Name string `json:"name"`
	// Scope is "transaction", "ledger" or "entry".
	Scope string `json:"scope"`
	// Description says what the limit bounds, in words.
	Description string `json:"description"`
	// Value is the limit.
	Value int64 `json:"value"`
	// Unit is "instructions", "bytes" or "entries".
	Unit string `json:"unit"`
}

// NetworkLimits are the Soroban resource limits the network enforces, read
// from its config setting ledger entries.
type NetworkLimits struct {
	// Limits are in a stable order: transaction limits first.
	Limits []Limit `json:"limits"`
	// Missing names settings the network did not return.
	Missing []string `json:"missing,omitempty"`
	// Unrecognized names settings whose value SoroProbe could not decode,
	// such as a type a newer protocol introduced.
	Unrecognized []string `json:"unrecognized,omitempty"`
	// LatestLedger is the ledger the limits were read at.
	LatestLedger uint32 `json:"latest_ledger"`
}

// Get returns the named limit and whether the network reported it.
func (n *NetworkLimits) Get(name string) (int64, bool) {
	if n == nil {
		return 0, false
	}
	for _, l := range n.Limits {
		if l.Name == name {
			return l.Value, true
		}
	}
	return 0, false
}

// limitsCache keeps the last read for limitsCacheTTL.
type limitsCache struct {
	mu      sync.Mutex
	limits  *NetworkLimits
	fetched time.Time
	now     func() time.Time
}

func (c *limitsCache) get() *NetworkLimits {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limits != nil && c.clock().Sub(c.fetched) < limitsCacheTTL {
		return c.limits
	}
	return nil
}

func (c *limitsCache) put(l *NetworkLimits) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits, c.fetched = l, c.clock()
}

func (c *limitsCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Limits reads the network's Soroban resource limits. Results are cached
// for a minute.
func (p *Prober) Limits(ctx context.Context) (*NetworkLimits, error) {
	if cached := p.limits.get(); cached != nil {
		return cached, nil
	}
	keys := make([]string, 0, len(limitSettings))
	ids := make(map[string]xdr.ConfigSettingId, len(limitSettings))
	for _, id := range limitSettings {
		key, err := stellar.EncodeKey(stellar.SettingKey(id))
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		ids[key] = id
	}
	resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: keys})
	if err != nil {
		return nil, err
	}

	out := &NetworkLimits{LatestLedger: resp.LatestLedger}
	seen := map[xdr.ConfigSettingId]bool{}
	for _, entry := range resp.Entries {
		id, ok := ids[entry.KeyXDR]
		if !ok {
			continue
		}
		seen[id] = true
		var data xdr.LedgerEntryData
		if err := xdr.SafeUnmarshalBase64(entry.DataXDR, &data); err != nil {
			out.Unrecognized = append(out.Unrecognized, settingName(id))
			continue
		}
		setting, ok := data.GetConfigSetting()
		if !ok || !addLimits(out, setting) {
			out.Unrecognized = append(out.Unrecognized, settingName(id))
		}
	}
	for _, id := range limitSettings {
		if !seen[id] {
			out.Missing = append(out.Missing, settingName(id))
		}
	}
	sort.SliceStable(out.Limits, func(i, j int) bool {
		return scopeRank[out.Limits[i].Scope] < scopeRank[out.Limits[j].Scope]
	})
	p.limits.put(out)
	return out, nil
}

var scopeRank = map[string]int{"transaction": 0, "entry": 1, "ledger": 2}

func settingName(id xdr.ConfigSettingId) string {
	if name := id.String(); name != "" {
		return name
	}
	return fmt.Sprintf("config setting %d", id)
}

// addLimits records the limits a setting carries. It returns false for a
// setting whose value is not the arm its id promises.
func addLimits(out *NetworkLimits, s xdr.ConfigSettingEntry) bool {
	add := func(name, scope, desc string, v int64, unit string) {
		out.Limits = append(out.Limits, Limit{Name: name, Scope: scope, Description: desc, Value: v, Unit: unit})
	}
	switch s.ConfigSettingId {
	case xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes:
		v, ok := s.GetContractMaxSizeBytes()
		if !ok {
			return false
		}
		add("contract_max_size", "entry", "largest contract WASM", int64(v), "bytes")
	case xdr.ConfigSettingIdConfigSettingContractComputeV0:
		c, ok := s.GetContractCompute()
		if !ok {
			return false
		}
		add(LimitTxInstructions, "transaction", "CPU instructions per transaction", int64(c.TxMaxInstructions), "instructions")
		add("tx_memory_limit", "transaction", "memory per transaction", int64(c.TxMemoryLimit), "bytes")
		add("ledger_max_instructions", "ledger", "CPU instructions per ledger", int64(c.LedgerMaxInstructions), "instructions")
	case xdr.ConfigSettingIdConfigSettingContractLedgerCostV0:
		c, ok := s.GetContractLedgerCost()
		if !ok {
			return false
		}
		add("tx_max_disk_read_entries", "transaction", "ledger entries read from disk per transaction", int64(c.TxMaxDiskReadEntries), "entries")
		add(LimitTxDiskReadBytes, "transaction", "bytes read from disk per transaction", int64(c.TxMaxDiskReadBytes), "bytes")
		add("tx_max_write_entries", "transaction", "ledger entries written per transaction", int64(c.TxMaxWriteLedgerEntries), "entries")
		add(LimitTxWriteBytes, "transaction", "bytes written per transaction", int64(c.TxMaxWriteBytes), "bytes")
		add("ledger_max_disk_read_entries", "ledger", "ledger entries read from disk per ledger", int64(c.LedgerMaxDiskReadEntries), "entries")
		add("ledger_max_disk_read_bytes", "ledger", "bytes read from disk per ledger", int64(c.LedgerMaxDiskReadBytes), "bytes")
		add("ledger_max_write_entries", "ledger", "ledger entries written per ledger", int64(c.LedgerMaxWriteLedgerEntries), "entries")
		add("ledger_max_write_bytes", "ledger", "bytes written per ledger", int64(c.LedgerMaxWriteBytes), "bytes")
	case xdr.ConfigSettingIdConfigSettingContractLedgerCostExtV0:
		c, ok := s.GetContractLedgerCostExt()
		if !ok {
			return false
		}
		add(LimitTxFootprintEntries, "transaction", "ledger entries in a transaction's footprint", int64(c.TxMaxFootprintEntries), "entries")
	case xdr.ConfigSettingIdConfigSettingContractEventsV0:
		c, ok := s.GetContractEvents()
		if !ok {
			return false
		}
		add("tx_max_events_size", "transaction", "contract events and return value per transaction", int64(c.TxMaxContractEventsSizeBytes), "bytes")
	case xdr.ConfigSettingIdConfigSettingContractBandwidthV0:
		c, ok := s.GetContractBandwidth()
		if !ok {
			return false
		}
		add("tx_max_size", "transaction", "transaction size", int64(c.TxMaxSizeBytes), "bytes")
		add("ledger_max_txs_size", "ledger", "total transaction size per ledger", int64(c.LedgerMaxTxsSizeBytes), "bytes")
	case xdr.ConfigSettingIdConfigSettingContractDataKeySizeBytes:
		v, ok := s.GetContractDataKeySizeBytes()
		if !ok {
			return false
		}
		add("contract_data_key_size", "entry", "contract data key", int64(v), "bytes")
	case xdr.ConfigSettingIdConfigSettingContractDataEntrySizeBytes:
		v, ok := s.GetContractDataEntrySizeBytes()
		if !ok {
			return false
		}
		add("contract_data_entry_size", "entry", "contract data entry", int64(v), "bytes")
	default:
		return false
	}
	return true
}

// Names of the per-transaction limits a simulation is compared with.
const (
	LimitTxInstructions     = "tx_max_instructions"
	LimitTxDiskReadBytes    = "tx_max_disk_read_bytes"
	LimitTxWriteBytes       = "tx_max_write_bytes"
	LimitTxFootprintEntries = "tx_max_footprint_entries"
)

// DefaultHeadroomPercent is the share of a limit a call may use before it
// is warned about.
const DefaultHeadroomPercent = 80.0

// ResourceUsage is one simulated resource as a share of its limit.
type ResourceUsage struct {
	Name    string  `json:"name"`
	Used    int64   `json:"used"`
	Limit   int64   `json:"limit"`
	Percent float64 `json:"percent"`
	// OverHeadroom is true when Percent exceeds the headroom threshold.
	OverHeadroom bool `json:"over_headroom"`
}

// UsageReport compares a simulation's resources with the network limits.
type UsageReport struct {
	Resources []ResourceUsage `json:"resources"`
	// HeadroomPercent is the threshold Resources were judged against.
	HeadroomPercent float64 `json:"headroom_percent"`
	// Warning summarizes the resources over the threshold; empty when none.
	Warning string `json:"warning,omitempty"`
}

// CompareWithLimits measures a simulation's cost and footprint against the
// per-transaction limits. A limit the network did not report is skipped.
func CompareWithLimits(cost Cost, fp Footprint, limits *NetworkLimits, headroom float64) *UsageReport {
	if limits == nil {
		return nil
	}
	if headroom <= 0 {
		headroom = DefaultHeadroomPercent
	}
	report := &UsageReport{HeadroomPercent: headroom}
	measure := func(name string, used int64, limitName string) {
		limit, ok := limits.Get(limitName)
		if !ok || limit <= 0 {
			return
		}
		pct := float64(used) / float64(limit) * 100
		report.Resources = append(report.Resources, ResourceUsage{
			Name: name, Used: used, Limit: limit, Percent: pct, OverHeadroom: pct > headroom,
		})
	}
	measure("cpu instructions", int64(cost.Instructions), LimitTxInstructions)
	measure("disk read bytes", int64(cost.DiskReadBytes), LimitTxDiskReadBytes)
	measure("write bytes", int64(cost.WriteBytes), LimitTxWriteBytes)
	measure("footprint entries", int64(len(fp.ReadOnly)+len(fp.ReadWrite)), LimitTxFootprintEntries)
	if len(report.Resources) == 0 {
		return nil
	}
	var over []string
	for _, r := range report.Resources {
		if r.OverHeadroom {
			over = append(over, fmt.Sprintf("%s at %.1f%%", r.Name, r.Percent))
		}
	}
	if len(over) > 0 {
		report.Warning = fmt.Sprintf("near the network limit (over %.0f%%): %s", headroom, strings.Join(over, ", "))
	}
	return report
}
