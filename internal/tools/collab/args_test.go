package collab

import (
	"strings"
	"testing"
)

func TestRequireFloat64(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		key     string
		want    float64
		wantErr string
	}{
		{"valid", map[string]any{"id": float64(42)}, "id", 42, ""},
		{"zero", map[string]any{"id": float64(0)}, "id", 0, ""},
		{"missing key", map[string]any{}, "id", 0, "id is required"},
		{"nil value", map[string]any{"id": nil}, "id", 0, "id is required"},
		{"wrong type string", map[string]any{"id": "abc"}, "id", 0, "must be a number"},
		{"wrong type bool", map[string]any{"id": true}, "id", 0, "must be a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requireFloat64(tt.args, tt.key)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequireString(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		key     string
		want    string
		wantErr string
	}{
		{"valid", map[string]any{"name": "cursor"}, "name", "cursor", ""},
		{"missing", map[string]any{}, "name", "", "name is required"},
		{"empty string", map[string]any{"name": ""}, "name", "", "name is required"},
		{"wrong type", map[string]any{"name": 42}, "name", "", "name is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requireString(tt.args, tt.key)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOptionalFloat64(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		key      string
		fallback float64
		want     float64
	}{
		{"present", map[string]any{"limit": float64(50)}, "limit", 10, 50},
		{"missing", map[string]any{}, "limit", 10, 10},
		{"nil", map[string]any{"limit": nil}, "limit", 10, 10},
		{"wrong type", map[string]any{"limit": "abc"}, "limit", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := optionalFloat64(tt.args, tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
