package entity

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrRateLimited = errors.New("entity: too many requests")
	ErrLockedOut   = errors.New("entity: too many failed attempts")
)

type AttemptKind string

const (
	AttemptLogin    AttemptKind = "login"
	AttemptRegister AttemptKind = "register"
)

func (k AttemptKind) Valid() bool {
	return k == AttemptLogin || k == AttemptRegister
}

// LockoutPolicy is deliberately two numbers rather than a curve. Anything cleverer is harder to
// reason about under attack, and the failure mode of a curve nobody understands is a user locked
// out for a duration nobody predicted.
type LockoutPolicy struct {
	MaxFailures int
	Window      time.Duration
	LockFor     time.Duration
}

type AuthAttempt struct {
	TenantID    uuid.UUID
	Subject     string
	Kind        AttemptKind
	Failures    int
	WindowStart time.Time
	LockedUntil *time.Time
}

func (AuthAttempt) Validate() error { return nil }

func (a AuthAttempt) Locked(now time.Time) bool {
	return a.LockedUntil != nil && now.Before(*a.LockedUntil)
}

func (a AuthAttempt) RetryAfter(now time.Time) time.Duration {
	if !a.Locked(now) {
		return 0
	}
	return a.LockedUntil.Sub(now)
}

// Fail records one more failure and reports the attempt as it now stands. A window that has already
// elapsed starts again rather than accumulating, so a user who fails once a day is never locked.
func (a AuthAttempt) Fail(now time.Time, policy LockoutPolicy) AuthAttempt {
	next := a
	if next.WindowStart.IsZero() || now.Sub(next.WindowStart) > policy.Window {
		next.WindowStart = now
		next.Failures = 0
	}
	next.Failures++
	if next.Failures >= policy.MaxFailures {
		until := now.Add(policy.LockFor)
		next.LockedUntil = &until
	}
	return next
}
