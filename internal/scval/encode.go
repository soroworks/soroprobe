package scval

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// maxSymbolLen is Soroban's limit on ScSymbol length.
const maxSymbolLen = 32

func registerBuiltins(r *Registry) {
	r.Register("bool", encodeBool)
	r.Register("void", func(string) (xdr.ScVal, error) { return xdr.ScVal{Type: xdr.ScValTypeScvVoid}, nil })
	r.Register("null", func(string) (xdr.ScVal, error) { return xdr.ScVal{Type: xdr.ScValTypeScvVoid}, nil })

	r.Register("u32", encodeU32)
	r.Register("i32", encodeI32)
	r.Register("u64", encodeU64)
	r.Register("i64", encodeI64)
	r.Register("u128", encodeU128)
	r.Register("i128", encodeI128)
	r.Register("u256", encodeU256)
	r.Register("i256", encodeI256)

	r.Register("timepoint", encodeTimepoint)
	r.Register("duration", encodeDuration)

	r.Register("sym", encodeSymbol)
	r.Register("symbol", encodeSymbol)
	r.Register("str", encodeString)
	r.Register("string", encodeString)
	r.Register("bytes", encodeBytes)
	r.Register("addr", encodeAddress)
	r.Register("address", encodeAddress)
}

// infer applies the documented inference rules for a bare argument.
func (r *Registry) infer(spec string) (xdr.ScVal, error) {
	switch strings.ToLower(spec) {
	case "true", "false":
		return encodeBool(spec)
	case "void", "null":
		return xdr.ScVal{Type: xdr.ScValTypeScvVoid}, nil
	}

	if strkey.IsValidEd25519PublicKey(spec) || strkey.IsValidContractAddress(spec) {
		return encodeAddress(spec)
	}

	if isIntegerLiteral(spec) {
		return encodeI128(spec)
	}

	if isSymbolLiteral(spec) {
		return encodeSymbol(spec)
	}
	return encodeString(spec)
}

func isIntegerLiteral(s string) bool {
	if s == "" {
		return false
	}
	body := strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if body == "" {
		return false
	}
	for _, c := range body {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isSymbolLiteral(s string) bool {
	if s == "" || len(s) > maxSymbolLen {
		return false
	}
	for _, c := range s {
		isAlnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !isAlnum {
			return false
		}
	}
	return true
}

// unquote strips one layer of matching single or double quotes, which lets a
// shell-quoted string keep leading or trailing whitespace.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func encodeBool(literal string) (xdr.ScVal, error) {
	b, err := strconv.ParseBool(strings.TrimSpace(literal))
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected a boolean, got %q", literal)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}, nil
}

func encodeU32(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(literal), 10, 32)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected a u32, got %q", literal)
	}
	v := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}, nil
}

func encodeI32(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(literal), 10, 32)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected an i32, got %q", literal)
	}
	v := xdr.Int32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: &v}, nil
}

func encodeU64(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(literal), 10, 64)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected a u64, got %q", literal)
	}
	v := xdr.Uint64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v}, nil
}

func encodeI64(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(literal), 10, 64)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected an i64, got %q", literal)
	}
	v := xdr.Int64(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &v}, nil
}

func encodeTimepoint(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(literal), 10, 64)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected a unix timestamp, got %q", literal)
	}
	v := xdr.TimePoint(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvTimepoint, Timepoint: &v}, nil
}

func encodeDuration(literal string) (xdr.ScVal, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(literal), 10, 64)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected a duration in seconds, got %q", literal)
	}
	v := xdr.Duration(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvDuration, Duration: &v}, nil
}

func encodeSymbol(literal string) (xdr.ScVal, error) {
	s := unquote(literal)
	if len(s) > maxSymbolLen {
		return xdr.ScVal{}, fmt.Errorf("symbol %q is %d characters, limit is %d", s, len(s), maxSymbolLen)
	}
	if !isSymbolLiteral(s) && s != "" {
		return xdr.ScVal{}, fmt.Errorf("symbol %q may only contain a-z, A-Z, 0-9 and underscore", s)
	}
	v := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &v}, nil
}

func encodeString(literal string) (xdr.ScVal, error) {
	v := xdr.ScString(unquote(literal))
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &v}, nil
}

func encodeBytes(literal string) (xdr.ScVal, error) {
	s := strings.TrimPrefix(strings.TrimSpace(unquote(literal)), "0x")
	raw, err := hex.DecodeString(s)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("expected hex-encoded bytes, got %q: %w", literal, err)
	}
	v := xdr.ScBytes(raw)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &v}, nil
}

func encodeAddress(literal string) (xdr.ScVal, error) {
	s := strings.TrimSpace(unquote(literal))

	switch {
	case strkey.IsValidEd25519PublicKey(s):
		raw, err := strkey.Decode(strkey.VersionByteAccountID, s)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode account address %q: %w", s, err)
		}
		var key xdr.Uint256
		copy(key[:], raw)
		addr := xdr.ScAddress{
			Type:      xdr.ScAddressTypeScAddressTypeAccount,
			AccountId: &xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &key},
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}, nil

	case strkey.IsValidContractAddress(s):
		raw, err := strkey.Decode(strkey.VersionByteContract, s)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("decode contract address %q: %w", s, err)
		}
		var id xdr.ContractId
		copy(id[:], raw)
		addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &id}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}, nil

	default:
		return xdr.ScVal{}, fmt.Errorf("expected an account (G...) or contract (C...) address, got %q", s)
	}
}

// --- 128 and 256 bit integers ---------------------------------------------

func parseBigInt(literal string) (*big.Int, error) {
	s := strings.TrimSpace(literal)
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("expected a base-10 integer, got %q", literal)
	}
	return n, nil
}

// checkRange rejects values that do not fit the target width.
func checkRange(n *big.Int, bits uint, signed bool, name string) error {
	var lo, hi *big.Int
	if signed {
		hi = new(big.Int).Lsh(big.NewInt(1), bits-1)
		lo = new(big.Int).Neg(hi)
		hi.Sub(hi, big.NewInt(1))
	} else {
		lo = big.NewInt(0)
		hi = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1))
	}
	if n.Cmp(lo) < 0 || n.Cmp(hi) > 0 {
		return fmt.Errorf("value %s is out of range for %s", n.String(), name)
	}
	return nil
}

// twosComplementWords renders n as `count` 64-bit words, most significant
// first, using two's complement for negative values.
func twosComplementWords(n *big.Int, count int) []uint64 {
	bits := uint(count * 64)
	v := new(big.Int).Set(n)
	if v.Sign() < 0 {
		v.Add(v, new(big.Int).Lsh(big.NewInt(1), bits))
	}

	words := make([]uint64, count)
	mask := new(big.Int).SetUint64(^uint64(0))
	tmp := new(big.Int)
	for i := count - 1; i >= 0; i-- {
		words[i] = tmp.And(v, mask).Uint64()
		v.Rsh(v, 64)
	}
	return words
}

func encodeU128(literal string) (xdr.ScVal, error) {
	n, err := parseBigInt(literal)
	if err != nil {
		return xdr.ScVal{}, err
	}
	if err := checkRange(n, 128, false, "u128"); err != nil {
		return xdr.ScVal{}, err
	}
	w := twosComplementWords(n, 2)
	parts := xdr.UInt128Parts{Hi: xdr.Uint64(w[0]), Lo: xdr.Uint64(w[1])}
	return xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &parts}, nil
}

func encodeI128(literal string) (xdr.ScVal, error) {
	n, err := parseBigInt(literal)
	if err != nil {
		return xdr.ScVal{}, err
	}
	if err := checkRange(n, 128, true, "i128"); err != nil {
		return xdr.ScVal{}, err
	}
	w := twosComplementWords(n, 2)
	parts := xdr.Int128Parts{Hi: xdr.Int64(w[0]), Lo: xdr.Uint64(w[1])}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}, nil
}

func encodeU256(literal string) (xdr.ScVal, error) {
	n, err := parseBigInt(literal)
	if err != nil {
		return xdr.ScVal{}, err
	}
	if err := checkRange(n, 256, false, "u256"); err != nil {
		return xdr.ScVal{}, err
	}
	w := twosComplementWords(n, 4)
	parts := xdr.UInt256Parts{
		HiHi: xdr.Uint64(w[0]), HiLo: xdr.Uint64(w[1]),
		LoHi: xdr.Uint64(w[2]), LoLo: xdr.Uint64(w[3]),
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvU256, U256: &parts}, nil
}

func encodeI256(literal string) (xdr.ScVal, error) {
	n, err := parseBigInt(literal)
	if err != nil {
		return xdr.ScVal{}, err
	}
	if err := checkRange(n, 256, true, "i256"); err != nil {
		return xdr.ScVal{}, err
	}
	w := twosComplementWords(n, 4)
	parts := xdr.Int256Parts{
		HiHi: xdr.Int64(w[0]), HiLo: xdr.Uint64(w[1]),
		LoHi: xdr.Uint64(w[2]), LoLo: xdr.Uint64(w[3]),
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvI256, I256: &parts}, nil
}
