package vjoy

import (
	"testing"
	"time"
)

func TestThrottleSync(t *testing.T) {
	var s throttleSync
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

	if !s.shouldSync(0.2, false, at(0)) {
		t.Fatal("first reading after startup should sync the unknown device value")
	}
	if s.shouldSync(0.2, false, at(100)) {
		t.Fatal("within holdoff after a write: no sync")
	}
	// After holdoff the settled value becomes the baseline. A non-linear
	// MSFS curve settling a bit off what we wrote must not trigger another.
	if s.shouldSync(0.21, false, at(400)) || s.shouldSync(0.21, false, at(450)) {
		t.Fatal("settled value (our own write's effect) must not resync")
	}
	// Pilot pulls to idle in the sim.
	if !s.shouldSync(0, false, at(500)) {
		t.Fatal("outside change past tolerance should sync")
	}

	// Phone drag, then the sim moves under autothrottle: never sync.
	s.wrote(at(1000))
	s.shouldSync(0.5, false, at(1400)) // baseline
	if s.shouldSync(0.8, true, at(1500)) {
		t.Fatal("must not sync while autothrottle is active")
	}
	// Autothrottle lets go with the lever elsewhere: vJoy catches up.
	if !s.shouldSync(0.8, false, at(1600)) {
		t.Fatal("should sync once autothrottle is off")
	}
}
