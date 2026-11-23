package health_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/soroworks/soroprobe/internal/health"
)

func TestThresholdsMarshalsAndUnmarshals(t *testing.T) {
	t.Parallel()

	original := health.Thresholds{
		Warn:     120_960,
		Critical: 17_280,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded health.Thresholds
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip mismatch:\noriginal: %#v\ndecoded:  %#v", original, decoded)
	}
}
