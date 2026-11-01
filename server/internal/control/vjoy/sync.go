package vjoy

import (
	"math"
	"time"
)

const (
	// Many aircraft slew the lever toward a new input, so a fixed holdoff
	// after our write caught it mid-travel and fought the phone; wait for
	// it to stop instead.
	settleTime    = 500 * time.Millisecond
	settleEps     = 0.005
	syncTolerance = 0.02
)

// throttleSync decides when to move the vJoy throttle axis to the sim's
// lever, since the axis holds its last value after an in-sim change.
//
// After any write the phone owns the throttle: readings are ignored until
// the lever stops, and where it stops becomes the baseline (a non-linear
// MSFS sensitivity curve settles off what we wrote). Only a later, finished
// move away from the baseline resyncs. Not safe for concurrent use.
type throttleSync struct {
	lastWrite time.Time
	following bool
	last      float64
	lastMove  time.Time
	baseline  float64
}

func (s *throttleSync) wrote(now time.Time) {
	s.lastWrite, s.following, s.lastMove = now, true, now
}

// shouldSync reports whether to write sim to the device, recording the
// write if so. Never during autothrottle (a vJoy write looks like the pilot
// moving a hardware lever and can disconnect it), and never in reverse
// (aircraft disagree on reverse travel, e.g. -20% at full reverse, so
// writing it back would pull the lever out of reverse).
func (s *throttleSync) shouldSync(sim float64, autothrottle bool, now time.Time) bool {
	// Against the last position that counted as a move, not the previous
	// reading: a slow slew (the 737 MAX's, ~0.03/s) changes less than
	// settleEps per reading and would look stopped mid-travel.
	if math.Abs(sim-s.last) > settleEps {
		s.lastMove, s.last = now, sim
	}
	stopped := now.Sub(s.lastMove) >= settleTime

	switch {
	case autothrottle, sim < 0:
		return false
	case s.lastWrite.IsZero():
		// Device value unknown since startup: match the sim once.
	case s.following:
		if stopped {
			s.following, s.baseline = false, sim
		}
		return false
	case math.Abs(sim-s.baseline) < syncTolerance, !stopped:
		return false
	}
	s.wrote(now)
	return true
}
