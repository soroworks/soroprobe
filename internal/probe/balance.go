package probe

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
)

// TokenInfo carries metadata about a token contract.
type TokenInfo struct {
	ContractID string `json:"contract_id"`
	Symbol     string `json:"symbol"`
	Decimals   uint32 `json:"decimals"`
}

// HolderBalance reports the balance of a single holder address.
type HolderBalance struct {
	Address         string `json:"address"`
	RawBalance      string `json:"raw_balance"`
	FormattedAmount string `json:"formatted_amount"`
	Error           string `json:"error,omitempty"`
}

// TokenBalancesReport contains balances for one or more holders of a token.
type TokenBalancesReport struct {
	Token   TokenInfo       `json:"token"`
	Holders []HolderBalance `json:"holders"`
}

// ReadTokenInfo queries decimals and symbol from the token contract via simulation.
func (p *Prober) ReadTokenInfo(ctx context.Context, tokenID string) TokenInfo {
	info := TokenInfo{
		ContractID: tokenID,
		Decimals:   7, // Standard Soroban / Stellar default
		Symbol:     "TOKEN",
	}

	// Read decimals
	simDec, err := p.Simulate(ctx, SimulateRequest{
		ContractID: tokenID,
		Function:   "decimals",
	})
	if err == nil && simDec.Success && simDec.Result != "" {
		clean := strings.TrimPrefix(simDec.Result, "u32:")
		clean = strings.TrimPrefix(clean, "i32:")
		if d, err := strconv.ParseUint(clean, 10, 32); err == nil {
			info.Decimals = uint32(d)
		}
	}

	// Read symbol
	simSym, err := p.Simulate(ctx, SimulateRequest{
		ContractID: tokenID,
		Function:   "symbol",
	})
	if err == nil && simSym.Success && simSym.Result != "" {
		clean := strings.TrimPrefix(simSym.Result, "sym:")
		clean = strings.TrimPrefix(clean, "string:")
		clean = strings.Trim(clean, `"'`)
		if clean != "" {
			info.Symbol = clean
		}
	}

	return info
}

// GetTokenBalances reads and formats token balances for each given holder address.
func (p *Prober) GetTokenBalances(ctx context.Context, tokenID string, holders []string) (*TokenBalancesReport, error) {
	info := p.ReadTokenInfo(ctx, tokenID)

	report := &TokenBalancesReport{
		Token:   info,
		Holders: make([]HolderBalance, 0, len(holders)),
	}

	for _, holder := range holders {
		holder = strings.TrimSpace(holder)
		if holder == "" {
			continue
		}

		arg := holder
		if !strings.HasPrefix(arg, "addr:") && !strings.HasPrefix(arg, "address:") {
			arg = "addr:" + arg
		}

		sim, err := p.Simulate(ctx, SimulateRequest{
			ContractID: tokenID,
			Function:   "balance",
			Args:       []string{arg},
		})

		hb := HolderBalance{
			Address: holder,
		}

		if err != nil {
			hb.Error = err.Error()
		} else if !sim.Success {
			hb.Error = sim.Error
		} else {
			raw := strings.TrimPrefix(sim.Result, "i128:")
			raw = strings.TrimPrefix(raw, "u128:")
			raw = strings.Trim(raw, `"'`)
			hb.RawBalance = raw
			hb.FormattedAmount = FormatTokenAmount(raw, info.Decimals, info.Symbol)
		}

		report.Holders = append(report.Holders, hb)
	}

	return report, nil
}

// FormatTokenAmount formats a raw integer amount into decimal representation with symbol.
func FormatTokenAmount(raw string, decimals uint32, symbol string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		if symbol != "" {
			return "0 " + symbol
		}
		return "0"
	}

	bi, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		if symbol != "" {
			return raw + " " + symbol
		}
		return raw
	}

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	rat := new(big.Rat).SetFrac(bi, divisor)
	formatted := rat.FloatString(int(decimals))

	if symbol != "" {
		formatted += " " + symbol
	}
	return formatted
}

// ReadHoldersFromFile reads a list of holder addresses from an io.Reader (one per line).
func ReadHoldersFromFile(r io.Reader) ([]string, error) {
	var holders []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		holders = append(holders, line)
	}
	return holders, scanner.Err()
}
