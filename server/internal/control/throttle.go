package control

// RemapIdle maps the phone's idle=0, reverse<0, forward>0 throttle onto a
// signed -1..1 scale whose idle sits at idlePoint: 0..1 stretches over
// idlePoint..1 and -1..0 over -1..idlePoint. idlePoint=0 is the identity.
func RemapIdle(value, idlePoint float64) float64 {
	value = max(-1, min(1, value))
	if value >= 0 {
		return idlePoint + value*(1-idlePoint)
	}
	return idlePoint + value*(idlePoint+1)
}
