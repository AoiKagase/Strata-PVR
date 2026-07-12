package domain

import (
	"encoding/json"
	"testing"
)

func TestProgramUnmarshalNormalizesFractionalSecondsFromDuration(t *testing.T) {
	var program Program
	data := []byte(`{"id":"p","start":1000,"end":3001000,"seconds":0.001,"channel":{}}`)
	if err := json.Unmarshal(data, &program); err != nil {
		t.Fatal(err)
	}
	if program.Seconds != 3000 {
		t.Fatalf("seconds = %d, want 3000", program.Seconds)
	}
}

func TestProgramUnmarshalKeepsIntegerSeconds(t *testing.T) {
	var program Program
	data := []byte(`{"id":"p","start":1000,"end":3001000,"seconds":3000,"channel":{}}`)
	if err := json.Unmarshal(data, &program); err != nil {
		t.Fatal(err)
	}
	if program.Seconds != 3000 {
		t.Fatalf("seconds = %d, want 3000", program.Seconds)
	}
}
