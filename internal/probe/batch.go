package probe

import (
	"context"
	"errors"
	"fmt"

	"github.com/soroworks/soroprobe/internal/health"
)

// MaxBatchChecks bounds a batch, so one API request cannot queue an
// unbounded amount of RPC work.
const MaxBatchChecks = 100

// ErrInvalidBatch reports a malformed batch: empty, too large, or with
// duplicate or invalid entries. Nothing is checked when it is returned.
var ErrInvalidBatch = errors.New("invalid check batch")

// NamedCheck is one entry in a batch: a CheckRequest with a name that
// identifies it in the results. The name defaults to the contract ID.
type NamedCheck struct {
	Name string `json:"name,omitempty"`
	CheckRequest
}

// BatchRequest is a list of checks to run together.
type BatchRequest struct {
	Checks []NamedCheck `json:"checks"`
}

// BatchItem is the outcome of one check in a batch. Exactly one of Result
// and Error is set: Error means the check could not be run at all.
type BatchItem struct {
	Name       string       `json:"name"`
	ContractID string       `json:"contract_id"`
	Result     *CheckResult `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
}

// BatchResult aggregates a batch.
type BatchResult struct {
	// OK is true only when every check ran and passed.
	OK bool `json:"ok"`
	// Errored is true when at least one check could not be run. It is kept
	// separate from OK so a pipeline can still tell "a contract is
	// unhealthy" from "the tool could not tell".
	Errored bool        `json:"errored"`
	Items   []BatchItem `json:"items"`
}

// CheckBatch runs each check in order and reports them together.
//
// Checks run sequentially: a batch is usually pointed at a public RPC
// endpoint, and firing every check at once would trade a few seconds for
// rate-limit failures. One check failing to run does not stop the others;
// its error is recorded and the batch moves on. Only a malformed batch or a
// cancelled context returns a Go error.
func (p *Prober) CheckBatch(ctx context.Context, req BatchRequest) (*BatchResult, error) {
	if err := validateBatch(req); err != nil {
		return nil, err
	}

	out := &BatchResult{OK: true, Items: make([]BatchItem, 0, len(req.Checks))}
	for _, c := range req.Checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		item := BatchItem{Name: checkName(c), ContractID: c.ContractID}
		result, err := p.Check(ctx, c.CheckRequest)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			item.Error = err.Error()
			out.Errored = true
			out.OK = false
		default:
			item.Result = result
			if !result.OK {
				out.OK = false
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func checkName(c NamedCheck) string {
	if c.Name != "" {
		return c.Name
	}
	return c.ContractID
}

func validateBatch(req BatchRequest) error {
	switch {
	case len(req.Checks) == 0:
		return fmt.Errorf("%w: no checks given", ErrInvalidBatch)
	case len(req.Checks) > MaxBatchChecks:
		return fmt.Errorf("%w: %d checks exceeds the limit of %d", ErrInvalidBatch, len(req.Checks), MaxBatchChecks)
	}

	seen := make(map[string]int, len(req.Checks))
	for i, c := range req.Checks {
		if c.ContractID == "" {
			return fmt.Errorf("%w: check %d has no contract_id", ErrInvalidBatch, i)
		}
		switch c.DataDurability {
		case "", health.DurabilityPersistent, health.DurabilityTemporary:
		default:
			return fmt.Errorf("%w: check %q: unknown data_durability %q (want persistent or temporary)",
				ErrInvalidBatch, checkName(c), c.DataDurability)
		}
		name := checkName(c)
		if prev, dup := seen[name]; dup {
			return fmt.Errorf("%w: checks %d and %d are both named %q; names must be unique",
				ErrInvalidBatch, prev, i, name)
		}
		seen[name] = i
	}
	return nil
}
