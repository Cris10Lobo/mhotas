package vjoy

import (
	"testing"
	"time"
)

func TestThrottleSync(t *testing.T) {
	var s throttleSync
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	// feed reports whether any reading in a 50ms-tick ramp from..to over
	// [startMs, endMs] triggered a sync.
	feed := func(from, to float64, startMs, endMs int, autothrottle bool) bool {
		synced := false
		for ms := startMs; ms <= endMs; ms += 50 {
			v := from + (to-from)*float64(ms-startMs)/float64(max(endMs-startMs, 1))
			if s.shouldSync(v, autothrottle, at(ms)) {
				synced = true
			}
		}
		return synced
	}

	if !s.shouldSync(0.5, false, at(0)) {
		t.Fatal("first reading after startup should sync the unknown device value")
	}

	// Phone commands 0.8; the sim's lever slews there over 1.5s, then a
	// non-linear MSFS curve leaves it at 0.78. Neither may resync.
	s.wrote(at(1000))
	if feed(0.5, 0.78, 1000, 2500, false) || feed(0.78, 0.78, 2550, 4000, false) {
		t.Fatal("resynced while the sim was responding to our own input")
	}

	// Pilot pulls to idle in the sim (keyboard held for 1s): no sync while
	// it's still moving, one sync once it stops.
	if feed(0.78, 0, 4050, 5050, false) {
		t.Fatal("resynced while an outside change was still in progress")
	}
	if !feed(0, 0, 5100, 6000, false) {
		t.Fatal("should sync once the outside change has stopped")
	}
	if feed(0, 0, 6050, 8000, false) {
		t.Fatal("should not resync again after its own write settled")
	}

	// Autothrottle moves the lever: never sync, then catch up once it's off.
	if feed(0, 0.6, 8050, 9000, true) || feed(0.6, 0.6, 9050, 10000, true) {
		t.Fatal("must not sync while autothrottle is active")
	}
	if !s.shouldSync(0.6, false, at(10050)) {
		t.Fatal("should sync once autothrottle is off")
	}
}

func TestThrottleSyncSkipsReverse(t *testing.T) {
	var s throttleSync
	t0 := time.Unix(1000, 0)
	s.wrote(t0)
	s.shouldSync(0.3, false, t0.Add(600*time.Millisecond)) // settles: baseline
	for ms := 650; ms <= 3000; ms += 50 {
		if s.shouldSync(-0.2, false, t0.Add(time.Duration(ms)*time.Millisecond)) {
			t.Fatal("resynced a reverse position")
		}
	}
}
