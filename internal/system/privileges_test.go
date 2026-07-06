package system

import "testing"

func TestNumericID(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int
		ok    bool
	}{
		{name: "nil", value: nil, ok: false},
		{name: "int", value: 12, want: 12, ok: true},
		{name: "float64", value: float64(34), want: 34, ok: true},
		{name: "numeric string", value: "56", want: 56, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := numericID(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want || ok != tt.ok {
				t.Fatalf("numericID(%#v) = %d, %v; want %d, %v", tt.value, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNumericIDRejectsFractionalFloat(t *testing.T) {
	if _, _, err := numericID(1.5); err == nil {
		t.Fatal("expected error for fractional id")
	}
}
