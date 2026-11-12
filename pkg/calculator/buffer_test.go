package calculator

import (
	"testing"
	"time"
)

func TestCalculate(t *testing.T) {
	official := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	// travel: 25 min -> ceil(25 * 1.15) = ceil(28.75) = 29 min
	// prep: 35 min -> ceil(35 * 1.15) = ceil(40.25) = 41 min
	// Leave: 15:00 - 29min = 14:31
	// Prep: 14:31 - 41min = 13:50
	// Margin: (29 - 25) + (41 - 35) = 4 + 6 = 10 min

	res := Calculate(official, 25, 35, 0.15)

	expectedLeave := time.Date(2026, 11, 10, 14, 31, 0, 0, time.UTC)
	expectedPrep := time.Date(2026, 11, 10, 13, 50, 0, 0, time.UTC)

	if !res.BufferLeave.Equal(expectedLeave) {
		t.Errorf("BufferLeave = %v, expected %v", res.BufferLeave, expectedLeave)
	}
	if !res.BufferPrep.Equal(expectedPrep) {
		t.Errorf("BufferPrep = %v, expected %v", res.BufferPrep, expectedPrep)
	}
	if res.MarginMinutes != 10 {
		t.Errorf("MarginMinutes = %d, expected 10", res.MarginMinutes)
	}
}
