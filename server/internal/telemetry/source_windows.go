//go:build windows

package telemetry

import (
	"context"
	"time"

	"mhotas/internal/simlink"
	"mhotas/pkg/protocol"
)

// pollInterval controls how often Run checks simlink's latest aircraft
// state for a change. SimConnect delivers updates at 1Hz (see
// simlink.setupTelemetryRead); polling faster just wastes cycles.
const pollInterval = 100 * time.Millisecond

// Source reads live aircraft state from SimConnect (via simlink) and
// pushes it to a Sink whenever it changes.
type Source struct {
	client *simlink.Client
}

// NewSource wraps an already-connected simlink.Client — the same
// connection control/simconnect.Backend writes through, not a second one.
func NewSource(client *simlink.Client) (*Source, error) {
	return &Source{client: client}, nil
}

// Run polls simlink's latest aircraft-state reading and calls
// sink.SendTelemetry whenever it changes, until ctx is canceled.
func (s *Source) Run(ctx context.Context, sink Sink) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var last protocol.Telemetry
	have := false

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			as, ok := s.client.LatestState()
			if !ok {
				continue
			}
			t := toTelemetry(rawState{
				FlapHandleIndex:        as.FlapHandleIndex,
				FlapNumHandlePositions: as.FlapNumHandlePositions,
				GearPositionPercent:    as.GearPositionPercent,
				ThrottleLeverPercent:   as.ThrottleLeverPercent,
				ParkingBrakeSet:        as.ParkingBrakeSet,
				Paused:                 s.client.Paused(),
				AirspeedKnots:          as.AirspeedKnots,
				AltitudeFeet:           as.AltitudeFeet,
				HeadingDegrees:         as.HeadingDegrees,
				VerticalSpeedFpm:       as.VerticalSpeedFpm,
			})
			if have && t == last {
				continue
			}
			last, have = t, true
			if err := sink.SendTelemetry(t); err != nil {
				return err
			}
		}
	}
}
