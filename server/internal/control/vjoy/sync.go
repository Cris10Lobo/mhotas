package vjoy

import (
	"math"
	"time"
)

const (
	// syncHoldoff: no resync this soon after any write, so it can't fight a
	// drag in progress or the sim still catching up to the last write.
	syncHoldoff = 300 * time.Millisecond
	// syncTolerance: how far (in -1..1 units) the sim must move from where
	// it settled after our last write before that counts as an outside
	// change.
	syncTolerance = 0.02
)

// throttleSync decides when the vJoy throttle axis should be moved to the
// sim's actual lever position — the sim changed it (keyboard, mouse,
// autothrottle letting go) and vJoy still holds the old value, which MSFS
// would re-apply if it ever re-reads the device.
//
// The sim's value after our own write becomes the baseline, and only a move
// away from it triggers a resync. Writing the sim's value straight back on
// every difference would loop: an MSFS binding with a non-linear
// sensitivity curve settles slightly off what we wrote, so each write would
// nudge the throttle again, forever. Not safe for concurrent use; Backend
// serializes it with its own throttle lock.
type throttleSync struct {
	lastWrite time.Time
	baseline  float64
	settled   bool
}

// wrote records any write to the throttle axis (phone or resync).
func (s *throttleSync) wrote(now time.Time) {
	s.lastWrite, s.settled = now, false
}

// shouldSync reports whether to write sim to the device now, and records
// that write if so. Never while autothrottle is active: to MSFS a vJoy
// write is the pilot moving a hardware lever, which can disconnect it.
func (s *throttleSync) shouldSync(sim float64, autothrottle bool, now time.Time) bool {
	switch {
	case autothrottle:
		return false
	case s.lastWrite.IsZero():
		// Nothing written since startup, so the device's value is unknown:
		// match the sim once.
	case now.Sub(s.lastWrite) < syncHoldoff:
		return false
	case !s.settled:
		s.baseline, s.settled = sim, true
		return false
	case math.Abs(sim-s.baseline) < syncTolerance:
		return false
	}
	s.wrote(now)
	return true
}
