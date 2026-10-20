package vjoy

import (
	"math"
	"time"
)

const (
	// settleTime: the sim's lever counts as stopped once it hasn't moved
	// for this long. Many aircraft slew the lever toward a new input at a
	// limited rate rather than jumping, so a fixed holdoff after our write
	// isn't enough — it caught the lever mid-travel and "corrected" it,
	// fighting the phone.
	settleTime = 500 * time.Millisecond
	// settleEps: movement smaller than this between readings is noise.
	settleEps = 0.005
	// syncTolerance: how far (in -1..1 units) the stopped lever must be
	// from where it stopped after our last write to count as an outside
	// change.
	syncTolerance = 0.02
)

// throttleSync decides when the vJoy throttle axis should be moved to the
// sim's actual lever position — the sim changed it (keyboard, mouse,
// autothrottle letting go) and vJoy still holds the old value, which MSFS
// would re-apply if it ever re-reads the device.
//
// After any write the phone owns the throttle: sim readings are ignored
// until the lever stops moving, and where it stops becomes the baseline.
// Only a later move away from that baseline, once it has itself stopped,
// triggers a resync — so it never fights the lever slewing to our input,
// a keyboard/mouse change still in progress, or an MSFS binding with a
// non-linear sensitivity curve (which settles slightly off what we wrote).
// Not safe for concurrent use; Backend serializes it with its throttle
// lock.
type throttleSync struct {
	lastWrite time.Time
	following bool // sim still responding to our last write
	last      float64
	lastMove  time.Time
	baseline  float64
}

// wrote records any write to the throttle axis (phone or resync).
func (s *throttleSync) wrote(now time.Time) {
	s.lastWrite, s.following, s.lastMove = now, true, now
}

// shouldSync is fed every sim reading; it reports whether to write sim to
// the device now, and records that write if so. Never while autothrottle
// is active: to MSFS a vJoy write is the pilot moving a hardware lever,
// which can disconnect it.
func (s *throttleSync) shouldSync(sim float64, autothrottle bool, now time.Time) bool {
	if math.Abs(sim-s.last) > settleEps {
		s.lastMove = now
	}
	s.last = sim
	stopped := now.Sub(s.lastMove) >= settleTime

	switch {
	case autothrottle:
		return false
	case s.lastWrite.IsZero():
		// Nothing written since startup, so the device's value is unknown:
		// match the sim once.
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
