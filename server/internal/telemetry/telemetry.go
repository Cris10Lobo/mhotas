// Package telemetry streams live aircraft state to the phone as TELEMETRY packets.
package telemetry

import "mhotas/pkg/protocol"

type Sink interface {
	SendTelemetry(t protocol.Telemetry) error
}
