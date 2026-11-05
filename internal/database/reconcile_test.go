package database

import "testing"

func TestProvenanceReconcileMatrix(t *testing.T) {
	tests := []struct {
		phase                          string
		present, known, resource, live bool
		want                           string
	}{
		{"deleting", false, true, false, false, "remove"}, {"deleting", true, true, false, false, "drop"},
		{"created", true, true, false, false, "drop"}, {"created", true, true, true, false, "leave"},
		{"created", true, true, false, true, "leave"}, {"reserved", false, true, false, false, "remove"},
		{"reserved", true, true, false, false, "conflict"}, {"reserved", true, true, false, true, "leave"},
		{"deleting", true, false, false, false, "conflict"}, {"created", true, false, false, false, "conflict"},
		{"reserved", true, false, false, false, "conflict"},
	}
	for _, tt := range tests {
		if got := reconcileDecision(tt.phase, tt.present, tt.known, tt.resource, tt.live); got != tt.want {
			t.Errorf("%+v: got %s", tt, got)
		}
	}
}
