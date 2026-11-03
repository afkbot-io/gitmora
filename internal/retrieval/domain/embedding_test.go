package domain_test

import (
	"math"
	"testing"

	"workgraph.local/platform/internal/retrieval/domain"
)

func TestEmbeddingVectorsRejectInvalidCoordinatesAndPreserveCaller(t *testing.T) {
	for _, v := range [][]float32{{0, 0}, {1}, {float32(math.Inf(1)), 1}, {float32(math.NaN()), 1}} {
		if _, err := domain.NormalizeVector(v, 2); err == nil {
			t.Fatal("invalid embedding accepted")
		}
	}
	input := []float32{3, 4}
	normalized, err := domain.NormalizeVector(input, 2)
	if err != nil || normalized[0] != 0.6 || normalized[1] != 0.8 || input[0] != 3 {
		t.Fatal(normalized, err)
	}
}
