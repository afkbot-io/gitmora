package domain

import (
	"encoding/json"
	"math"
)

// NormalizeVector rejects invalid/zero vectors and pins a deterministic unit vector.
// Distance is a relevance signal, never an authorship or factual confidence score.
func NormalizeVector(values []float32, dimensions int) ([]float32, error) {
	if dimensions < 1 || dimensions > 2048 || len(values) != dimensions {
		return nil, ErrInvalid
	}
	norm := float64(0)
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, ErrInvalid
		}
		norm += float64(value) * float64(value)
	}
	if norm <= 0 {
		return nil, ErrInvalid
	}
	norm = math.Sqrt(norm)
	result := make([]float32, len(values))
	for i, value := range values {
		result[i] = float32(float64(value) / norm)
	}
	return result, nil
}

// VectorHash prevents a continuation from silently using a different query embedding.
func VectorHash(values []float32) string { raw, _ := json.Marshal(values); return Hash(raw) }
