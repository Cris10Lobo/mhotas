// Package telemetry reads live aircraft state (flap/gear/throttle/parking
// brake/pause) and pushes it to a connected phone as TELEMETRY packets.
// This file has no build constraint so the Sink interface is checkable on
// any platform; the actual SimConnect-backed reader (source_windows.go) is
// Windows-only.
package telemetry

import "mhotas/pkg/protocol"

// Sink is implemented by whatever pushes telemetry to the connected phone.
// transport.Server.SendTelemetry satisfies this.
type Sink interface {
	SendTelemetry(t protocol.Telemetry) error
}
