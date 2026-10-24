//go:build windows

package telemetry

import (
	"context"
	"time"

	"mhotas/internal/control"
	"mhotas/internal/simlink"
	"mhotas/pkg/protocol"
)

// pollInterval caps the TELEMETRY send rate at ~20Hz; SimConnect reports every changed frame.
const pollInterval = 50 * time.Millisecond

// keepaliveInterval re-sends unchanged state so a freshly connected phone gets the full picture.
const keepaliveInterval = time.Second

// Source pushes live aircraft state to a Sink whenever it changes.
type Source struct {
	client *simlink.Client
	syncer control.ThrottleSyncer
}

// NewSource reads from an already-connected client. syncer, if non-nil, is
// fed every reading so a backend that holds its last value (vJoy) can follow
// the sim's throttle.
func NewSource(client *simlink.Client, syncer control.ThrottleSyncer) (*Source, error) {
	return &Source{client: client, syncer: syncer}, nil
}

// Run sends telemetry to sink until ctx is canceled.
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
				AirspeedHold:           as.AirspeedHold,
				MachHold:               as.MachHold,
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
