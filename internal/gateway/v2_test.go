package gateway

import "testing"

func TestNewV2Engine(t *testing.T) {
	value, err := NewV2Engine()
	if err != nil || value == nil {
		t.Fatalf("new v2 engine = %v, %v", value, err)
	}
}
