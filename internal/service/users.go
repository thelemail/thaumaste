package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/entity"
)

type Session struct {
	UserID       string
	DeviceID     string
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

type RegisterInput struct {
	Username                 string
	Password                 string
	DeviceID                 string
	InitialDeviceDisplayName string
	InhibitLogin             bool
	WithRefreshToken         bool
}

type LoginInput struct {
	Type                     string
	Identifier               string
	Password                 string
	Token                    string
	DeviceID                 string
	InitialDeviceDisplayName string
	WithRefreshToken         bool
}

type Users interface {
	CheckUsername(ctx context.Context, scope entity.TenantScope, username string) error
	Register(ctx context.Context, scope entity.TenantScope, in RegisterInput) (entity.User, Session, error)
	Login(ctx context.Context, scope entity.TenantScope, in LoginInput) (Session, error)
	Refresh(ctx context.Context, scope entity.TenantScope, refreshToken string) (Session, error)
	Logout(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken) error
	LogoutAll(ctx context.Context, scope entity.TenantScope, userID string) error

	Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.User, error)
	UpdateProfile(ctx context.Context, scope entity.TenantScope, caller, target string, in entity.UpdateProfile) (entity.User, error)

	Devices(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.Device, error)
	Device(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (entity.Device, error)
	RenameDevice(ctx context.Context, scope entity.TenantScope, userID, deviceID, displayName string) error
	DeleteDevices(ctx context.Context, scope entity.TenantScope, userID string, deviceIDs []string) error

	ChangePassword(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken, newPassword string, logoutDevices bool) error
	Deactivate(ctx context.Context, scope entity.TenantScope, userID string) error

	BeginAuth(ctx context.Context, scope entity.TenantScope, kind entity.UIAKind, userID string) (entity.UIASession, error)
	SubmitAuth(ctx context.Context, scope entity.TenantScope, kind entity.UIAKind, sessionID uuid.UUID, stage, identifier, password string) (entity.UIASession, error)

	GuardAttempt(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error
	RetryAfter(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) time.Duration
	RecordFailure(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error
	ClearFailures(ctx context.Context, scope entity.TenantScope, subject string, kind entity.AttemptKind) error

	LoginFlows(ctx context.Context) []string
	Touch(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken, ip string)
}
