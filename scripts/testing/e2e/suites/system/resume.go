// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"fmt"
	"log"
)

// resolveStopIntent observes the role's live data plane. A healthy endpoint
// proves Stop was not applied, so exactly one Stop is issued. An unavailable
// endpoint proves the intended outage and only the missing durable post-state
// is appended.
func (s *systemCheck) resolveStopIntent(ctx context.Context, service string) error {
	status, err := s.k.Status(ctx, service)
	if err != nil {
		return fmt.Errorf("resolve recorded stop-intent for %s: service state is unknown: %w", service, err)
	}
	if status == ServiceRunning {
		log.Printf("systemcheck: %s is still running after recorded stop-intent; applying Stop once", service)
		if err := s.k.Stop(ctx, service); err != nil {
			return fmt.Errorf("apply unresolved Stop for %s: %w", service, err)
		}
	} else if status == ServiceStopped {
		log.Printf("systemcheck: %s is stopped after recorded stop-intent; preserving the completed Stop", service)
	} else {
		return fmt.Errorf("resolve recorded stop-intent for %s: unsupported service status %q", service, status)
	}
	return s.recordRestart(ctx, service, RestartStopped)
}

// resolveStartIntent mirrors resolveStopIntent. A healthy endpoint proves the
// service already started; an unavailable endpoint requires exactly one Start.
func (s *systemCheck) resolveStartIntent(ctx context.Context, service string) error {
	status, err := s.k.Status(ctx, service)
	if err != nil {
		return fmt.Errorf("resolve recorded start-intent for %s: service state is unknown: %w", service, err)
	}
	if status == ServiceStopped {
		log.Printf("systemcheck: %s is still stopped after recorded start-intent; applying Start once", service)
		if err := s.k.Start(ctx, service); err != nil {
			return fmt.Errorf("apply unresolved Start for %s: %w", service, err)
		}
	} else if status == ServiceRunning {
		log.Printf("systemcheck: %s is running after recorded start-intent; preserving the completed Start", service)
	} else {
		return fmt.Errorf("resolve recorded start-intent for %s: unsupported service status %q", service, status)
	}
	return s.recordRestart(ctx, service, RestartStarted)
}

// resolveEmergencyStartIntent reconciles only the safety recovery mutation.
// Its distinct evidence must never be mistaken for the planned recovery that
// proves the fault-cycle assertions.
func (s *systemCheck) resolveEmergencyStartIntent(ctx context.Context, service string) error {
	status, err := s.k.Status(ctx, service)
	if err != nil {
		return fmt.Errorf("resolve recorded emergency-start-intent for %s: service state is unknown: %w", service, err)
	}
	if status == ServiceStopped {
		log.Printf("systemcheck: %s is still stopped after recorded emergency recovery intent; applying Start once", service)
		if err := s.k.Start(ctx, service); err != nil {
			return fmt.Errorf("apply unresolved emergency Start for %s: %w", service, err)
		}
	} else if status == ServiceRunning {
		log.Printf("systemcheck: %s is running after recorded emergency recovery intent; preserving the completed Start", service)
	} else {
		return fmt.Errorf("resolve recorded emergency-start-intent for %s: unsupported service status %q", service, status)
	}
	return s.recordRestart(ctx, service, RestartEmergencyStarted)
}

// reenterFaultAfterEmergency begins a new append-only fault-cycle generation
// after a prior attempt restored the service for safety before the required
// outage milestone was durable. The old generation is never rewritten.
func (s *systemCheck) reenterFaultAfterEmergency(ctx context.Context, service string) (bool, error) {
	status, err := s.k.Status(ctx, service)
	if err != nil {
		return false, fmt.Errorf("inspect %s before re-entering its fault cycle: %w", service, err)
	}
	if status != ServiceRunning {
		return false, fmt.Errorf("%s is %q after durable emergency recovery, want running before a new fault-cycle generation", service, status)
	}
	if err := s.recordRestart(ctx, service, RestartStopIntent); err != nil {
		return false, err
	}
	if err := s.k.Stop(ctx, service); err != nil {
		// Once the durable intent exists, a failed Stop response is ambiguous:
		// the service may already be down. Tell the caller's safety defer to
		// reconcile and recover it before returning the original error.
		return true, err
	}
	if err := s.recordRestart(ctx, service, RestartStopped); err != nil {
		return true, err
	}
	return true, nil
}
