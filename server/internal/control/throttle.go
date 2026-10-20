package control

// RemapIdle moves this project's own idle=0/reverse<0/forward>0 throttle
// convention onto idlePoint on a backend's signed -1..1 scale: idle..full
// power (0..1) stretches across idlePoint..1, and full reverse..idle (-1..0)
// across -1..idlePoint. idlePoint=0 is the identity; -1 puts idle at the
// bottom of the range. Where idle falls depends on the aircraft (and, for
// vJoy, on the MSFS axis binding), so it's always the operator's tuned
// signed_throttle_idle, never a guess. Shared by every backend that writes
// a throttle axis, so the same setting means the same thing on each.
func RemapIdle(value, idlePoint float64) float64 {
	value = max(-1, min(1, value))
	if value >= 0 {
		return idlePoint + value*(1-idlePoint)
	}
	return idlePoint + value*(idlePoint+1)
}
