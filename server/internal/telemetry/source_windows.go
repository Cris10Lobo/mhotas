//go:build windows

package telemetry

import (
	"context"
	"time"

	"mhotas/internal/control"
	"mhotas/internal/simlink"
	"mhotas/pkg/protocol"
)

// pollInterval caps the TELEMETRY send rate at ~20Hz. SimConnect itself
// reports every changed sim frame (see simlink.setupTelemetryRead), which
// would be far more packets than the phone can use.
const pollInterval = 50 * time.Millisecond

// keepaliveInterval re-sends unchanged state so a phone that just
// (re)connected gets the full picture without waiting for something in the
// sim to change.
const keepaliveInterval = time.Second

// Source reads live aircraft state from SimConnect (via simlink) and
// pushes it to a Sink whenever it changes.
type Source struct {
	client *simlink.Client
	syncer control.ThrottleSyncer
}

// NewSource wraps an already-connected simlink.Client — the same
// connection control/simconnect.Backend writes through, not a second one.
//
// syncer, if non-nil, is fed every reading so an input backend that holds
// its last value (vJoy) can follow the sim's throttle.
func NewSource(client *simlink.Client, syncer control.ThrottleSyncer) (*Source, error) {
	return &Source{client: client, syncer: syncer}, nil
}

// Run polls simlink's latest aircraft-state reading and calls
// sink.SendTelemetry whenever it changes, until ctx is canceled.
func (s *Source) Run(ctx context.Context, sink Sink) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var last protocol.Telemetry
	var lastSent time.Time
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
				StallWarning:           as.StallWarning,
				OnGround:               as.OnGround,
				GroundVelocityKnots:    as.GroundVelocityKnots,
				SpoilersHandlePercent:  as.SpoilersHandlePercent,
				SpoilersArmed:          as.SpoilersArmed,
				ElevatorTrim:           as.ElevatorTrim,
				AutopilotMaster:        as.AutopilotMaster,
				AutothrottleArmed:      as.AutothrottleArmed,
				ManagedThrottleActive:  as.ManagedThrottleActive,
				Paused:                 s.client.Paused(),
			})
			if s.syncer != nil {
				s.syncer.SyncThrottle(float64(t.ThrottlePercent), t.AutothrottleActive)
			}
			if have && t == last && time.Since(lastSent) < keepaliveInterval {
				continue
			}
			last, have, lastSent = t, true, time.Now()
			if err := sink.SendTelemetry(t); err != nil {
				return err
			}
		}
	}
}
