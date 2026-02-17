package collab

import "fmt"

// requireFloat64 extracts a float64 from args by key. Returns a clear error distinguishing
// "missing" from "wrong type" — safe against nil values (no panic).
func requireFloat64(args map[string]any, key string) (float64, error) {
	v, exists := args[key]
	if !exists || v == nil {
		return 0, fmt.Errorf("%s is required", key)
	}
	f, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("%s must be a number, got %T", key, v)
	}
	return f, nil
}

// requireString extracts a non-empty string from args by key.
func requireString(args map[string]any, key string) (string, error) {
	v, _ := args[key].(string)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// optionalFloat64 extracts a float64 from args by key, returning the fallback if not present.
func optionalFloat64(args map[string]any, key string, fallback float64) float64 {
	if v, ok := args[key].(float64); ok {
		return v
	}
	return fallback
}
