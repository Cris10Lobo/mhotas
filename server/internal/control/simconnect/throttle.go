package simconnect

import "mhotas/internal/control"

// stockIdlePoint is where THROTTLE_AXIS_SET_EX1 puts idle under the
// arm-toggle convention: the range's negative end, not raw 0. The SDK's
// "(0-16383)" doc text is wrong; raw 0 gave ~50-58% power live on a Cessna
// 172 and a jet.
const stockIdlePoint = -1

// toSignedThrottleRaw applies control.RemapIdle and scales to the raw range.
func toSignedThrottleRaw(value, idlePoint float64, axisRange int32) int32 {
	return int32(control.RemapIdle(value, idlePoint) * float64(axisRange))
}
