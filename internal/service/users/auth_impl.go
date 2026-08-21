package users

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
)

func (s *srv) BeginAuth(ctx context.Context, scope entity.TenantScope, kind entity.UIAKind, userID string) (entity.UIASession, error) {
	in := entity.NewUIASession{TenantID: scope.ID(), Kind: kind, UserID: userID, TTL: s.cfg.SessionTTL}
	if err := in.Validate(); err != nil {
		return entity.UIASession{}, err
	}
	return s.sessions.Create(ctx, in)
}

// SubmitAuth advances one stage of an interactive flow. A stage already recorded is not re-run:
// the spec forbids retrying a completed stage, and re-running the password check would let a
// caller brute force behind a session that has already succeeded once.
func (s *srv) SubmitAuth(ctx context.Context, scope entity.TenantScope, kind entity.UIAKind, sessionID uuid.UUID, stage, identifier, password string) (entity.UIASession, error) {
	session, err := s.sessions.Get(ctx, scope, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrUIASessionNotFound) {
			return entity.UIASession{}, entity.ErrUIASessionNotFound
		}
		return entity.UIASession{}, err
	}
	if session.Kind != kind || session.Expired(s.clock().UTC()) {
		return entity.UIASession{}, entity.ErrUIASessionNotFound
	}
	if session.Done() {
		return session, nil
	}

	wanted, _ := session.Next()
	if stage != wanted {
		return entity.UIASession{}, entity.ErrUIAStageUnknown
	}

	switch stage {
	case entity.LoginTypeDummy:
	case entity.LoginTypePassword:
		subject := identifier
		if subject == "" {
			subject = session.UserID
		}
		if _, err := s.verifyPassword(ctx, scope, subject, password); err != nil {
			return entity.UIASession{}, err
		}
	default:
		return entity.UIASession{}, entity.ErrUIAStageUnknown
	}
	return s.sessions.Complete(ctx, scope, sessionID, stage)
}

// verifyPassword checks a credential without issuing anything. A stage of an interactive flow
// proves who the caller is; it must not hand out a second session as a side effect.
func (s *srv) verifyPassword(ctx context.Context, scope entity.TenantScope, identifier, password string) (string, error) {
	for _, p := range s.providers {
		if p.loginType() != entity.LoginTypePassword {
			continue
		}
		result, err := p.authenticate(ctx, scope, providerInput{
			Identifier: identifier,
			Password:   password,
			ServerName: scope.ServerName(),
		})
		if err != nil {
			return "", err
		}
		return result.UserID, nil
	}
	return "", entity.ErrBadCredentials
}

func (s *srv) FinishAuth(ctx context.Context, scope entity.TenantScope, sessionID uuid.UUID) error {
	return s.sessions.Delete(ctx, scope, sessionID)
}

// GuardAttempt refuses before any work is done. Checking the lock first is what stops a locked
// account from still costing a password hash on every attempt.
func (s *srv) GuardAttempt(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error {
	attempt, err := s.attempts.Get(ctx, scope, subject, kind)
	if err != nil {
		return err
	}
	if attempt.Locked(s.clock().UTC()) {
		return entity.ErrLockedOut
	}
	return nil
}

func (s *srv) RecordFailure(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error {
	attempt, err := s.attempts.Get(ctx, scope, subject, kind)
	if err != nil {
		return err
	}
	next := attempt.Fail(s.clock().UTC(), s.lockoutPolicy())
	next.TenantID = scope.ID()
	next.Subject = subject
	next.Kind = kind
	return s.attempts.Save(ctx, next)
}

func (s *srv) ClearFailures(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error {
	return s.attempts.Clear(ctx, scope, subject, kind)
}

func (s *srv) RetryAfter(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) time.Duration {
	attempt, err := s.attempts.Get(ctx, scope, subject, kind)
	if err != nil {
		return s.cfg.LockFor
	}
	return attempt.RetryAfter(s.clock().UTC())
}

func (s *srv) lockoutPolicy() entity.LockoutPolicy {
	policy := entity.LockoutPolicy{
		MaxFailures: s.cfg.MaxFailures,
		Window:      s.cfg.FailureWindow,
		LockFor:     s.cfg.LockFor,
	}
	if policy.MaxFailures <= 0 {
		policy.MaxFailures = 10
	}
	if policy.Window <= 0 {
		policy.Window = 15 * time.Minute
	}
	if policy.LockFor <= 0 {
		policy.LockFor = 15 * time.Minute
	}
	return policy
}
