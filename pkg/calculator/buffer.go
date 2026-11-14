package calculator

import (
	"math"
	"time"
)

const DefaultDelayMargin = 0.15 // 15% deterministic delay safety margin

type BufferResult struct {
	OfficialTime  time.Time
	BufferPrep    time.Time // Buffer 1: time to start preparing/shower
	BufferLeave   time.Time // Buffer 2: time to leave the house
	MarginMinutes int       // Extra minutes added by the 15% margin
}

// Calculate computes deterministic anti-delay buffers for an event.
func Calculate(officialTime time.Time, travelMin, prepMin int, delayFactor float64) BufferResult {
	if travelMin < 0 {
		travelMin = 25
	}
	if prepMin < 0 {
		prepMin = 35
	}
	if delayFactor <= 0 {
		delayFactor = DefaultDelayMargin
	}

	travelWithMargin := int(math.Ceil(float64(travelMin) * (1.0 + delayFactor)))
	prepWithMargin := int(math.Ceil(float64(prepMin) * (1.0 + delayFactor)))

	minutesUntilLeave := travelWithMargin
	minutesUntilPrep := travelWithMargin + prepWithMargin

	bufferLeave := officialTime.Add(-time.Duration(minutesUntilLeave) * time.Minute)
	bufferPrep := officialTime.Add(-time.Duration(minutesUntilPrep) * time.Minute)

	marginMinutes := (travelWithMargin - travelMin) + (prepWithMargin - prepMin)

	return BufferResult{
		OfficialTime:  officialTime,
		BufferPrep:    bufferPrep,
		BufferLeave:   bufferLeave,
		MarginMinutes: marginMinutes,
	}
}
