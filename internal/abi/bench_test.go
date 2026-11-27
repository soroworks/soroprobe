package abi_test

import (
	"testing"
	"time"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

var (
	chainSourceSink *abi.ChainSource
	soroVaultSink   *abi.SoroVault
)

func BenchmarkNewChainSource(b *testing.B) {
	client := struct{ stellar.Client }{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		chainSourceSink = abi.NewChainSource(client)
	}
}

func BenchmarkNewSoroVault(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		src, err := abi.NewSoroVault("http://localhost:8080", nil, 30*time.Second)
		if err != nil {
			b.Fatal(err)
		}
		soroVaultSink = src
	}
}
