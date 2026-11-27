package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/soroworks/soroprobe/internal/config"
)

var configSink config.Config

func BenchmarkDefault(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		configSink = config.Default()
	}
}

func BenchmarkLoad(b *testing.B) {
	path := filepath.Join(b.TempDir(), "soroprobe.json")
	body := []byte(`{"rpc_url":"https://rpc.example.com","timeout":"5s","warn_ledgers":100,"critical_ledgers":10}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg, err := config.Load(path)
		if err != nil {
			b.Fatal(err)
		}
		configSink = cfg
	}
}
