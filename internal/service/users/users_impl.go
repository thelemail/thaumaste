package users

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/repository"
	"github.com/thelemail/thaumaste/internal/service"
)

type srv struct {
	users       repository.User
	credentials repository.Credential
	devices     repository.Device
	refresh     repository.RefreshToken
	sessions    repository.UIASession
	attempts    repository.AuthAttempt
	tokens      service.Tokens
	tenants     service.Tenants
	deviceLists service.DeviceLists
	tx          repository.Transactor
	cfg         config.Auth
	providers   []provider
	clock       func() time.Time
	rnd         io.Reader
}

func New(
	usersRepo repository.User,
	credentials repository.Credential,
	devices repository.Device,
	refresh repository.RefreshToken,
	sessions repository.UIASession,
	attempts repository.AuthAttempt,
	tokens service.Tokens,
	tenants service.Tenants,
	deviceLists service.DeviceLists,
	tx repository.Transactor,
	cfg config.Auth,
	clock func() time.Time,
	rnd io.Reader,
) service.Users {
	if clock == nil {
		clock = time.Now
	}
	if rnd == nil {
		rnd = rand.Reader
	}

	s := &srv{
		deviceLists: deviceLists,
		users:       usersRepo, credentials: credentials, devices: devices, refresh: refresh,
		sessions: sessions, attempts: attempts,
		tokens: tokens, tenants: tenants, tx: tx, cfg: cfg, clock: clock, rnd: rnd,
	}
	s.providers = append(s.providers, &localProvider{
		users: usersRepo, credentials: credentials, params: s.argon2Params(),
	})
	if key := assertionKey(cfg.AssertionKey); len(key) > 0 {
		s.providers = append(s.providers, &assertionProvider{key: key, ttl: cfg.AssertionTTL, clock: clock})
	}
	return s
}

func assertionKey(encoded string) ed25519.PublicKey {
	if encoded == "" {
		return nil
	}
	raw, err := entity.DecodeBase64(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}

func (s *srv) argon2Params() entity.Argon2Params {
	params := entity.DefaultArgon2Params()
	if s.cfg.Argon2Time > 0 {
		params.Time = s.cfg.Argon2Time
	}
	if s.cfg.Argon2MemoryK > 0 {
		params.Memory = s.cfg.Argon2MemoryK
	}
	if s.cfg.Argon2Threads > 0 {
		params.Threads = s.cfg.Argon2Threads
	}
	return params
}

func (s *srv) LoginFlows(context.Context) []string {
	out := make([]string, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, p.loginType())
	}
	return out
}

func (s *srv) CheckUsername(ctx context.Context, scope entity.TenantScope, username string) error {
	localpart := entity.NormaliseLocalpart(username)
	if _, err := entity.MintUserID(localpart, scope.ServerName()); err != nil {
		return err
	}
	taken, err := s.users.Exists(ctx, scope, localpart)
	if err != nil {
		return err
	}
	if taken {
		return entity.ErrUserInUse
	}
	return nil
}

func (s *srv) Register(ctx context.Context, scope entity.TenantScope, in service.RegisterInput) (entity.User, service.Session, error) {
	tenant, err := s.tenants.ByServerName(ctx, scope.ServerName())
	if err != nil {
		return entity.User{}, service.Session{}, err
	}
	if tenant.RegistrationMode != entity.RegistrationOpen {
		return entity.User{}, service.Session{}, entity.ErrRegistrationShut
	}
	if err := s.CheckUsername(ctx, scope, in.Username); err != nil {
		return entity.User{}, service.Session{}, err
	}
	if err := entity.CheckPasswordStrength(in.Password); err != nil {
		return entity.User{}, service.Session{}, err
	}

	localpart := entity.NormaliseLocalpart(in.Username)
	var (
		created entity.User
		session service.Session
	)
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		created, err = s.createUser(ctx, scope, localpart, "")
		if err != nil {
			return err
		}
		credential, err := entity.NewCredential(created.UserID, in.Password, s.argon2Params(), s.rnd)
		if err != nil {
			return err
		}
		if err := s.credentials.Upsert(ctx, scope, credential); err != nil {
			return err
		}
		if in.InhibitLogin {
			return nil
		}
		session, err = s.issue(ctx, scope, created.UserID, in.DeviceID, in.InitialDeviceDisplayName, in.WithRefreshToken)
		return err
	})
	if err != nil {
		return entity.User{}, service.Session{}, err
	}
	return created, session, nil
}

func (s *srv) createUser(ctx context.Context, scope entity.TenantScope, localpart, displayName string) (entity.User, error) {
	created, err := s.users.Create(ctx, entity.NewUser{
		TenantID:    scope.ID(),
		Localpart:   localpart,
		ServerName:  scope.ServerName(),
		DisplayName: displayName,
	})
	if err != nil {
		if errors.Is(err, repository.ErrUserInUse) {
			return entity.User{}, entity.ErrUserInUse
		}
		return entity.User{}, err
	}
	return created, nil
}

func (s *srv) Login(ctx context.Context, scope entity.TenantScope, in service.LoginInput) (service.Session, error) {
	var chosen provider
	for _, p := range s.providers {
		if p.loginType() == in.Type {
			chosen = p
		}
	}
	if chosen == nil {
		return service.Session{}, entity.ErrBadCredentials
	}

	result, err := chosen.authenticate(ctx, scope, providerInput{
		Identifier: in.Identifier,
		Password:   in.Password,
		Token:      in.Token,
		ServerName: scope.ServerName(),
	})
	if err != nil {
		return service.Session{}, err
	}

	var session service.Session
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		user, err := s.users.Get(ctx, scope, result.UserID)
		switch {
		case err == nil:
		case errors.Is(err, repository.ErrUserNotFound) && result.Provision:
			user, err = s.provision(ctx, scope, result)
			if err != nil {
				return err
			}
		case errors.Is(err, repository.ErrUserNotFound):
			return entity.ErrBadCredentials
		default:
			return err
		}
		if !user.Active() {
			return entity.ErrUserDeactivated
		}

		if in.Type == entity.LoginTypePassword {
			s.rehashIfStale(ctx, scope, user.UserID, in.Password)
		}

		session, err = s.issue(ctx, scope, user.UserID, in.DeviceID, in.InitialDeviceDisplayName, in.WithRefreshToken)
		return err
	})
	if err != nil {
		return service.Session{}, err
	}
	return session, nil
}

func (s *srv) provision(ctx context.Context, scope entity.TenantScope, result providerResult) (entity.User, error) {
	tenant, err := s.tenants.ByServerName(ctx, scope.ServerName())
	if err != nil {
		return entity.User{}, err
	}
	switch tenant.RegistrationMode {
	case entity.RegistrationOpen, entity.RegistrationExternal:
	default:
		return entity.User{}, entity.ErrRegistrationShut
	}
	localpart, _, err := entity.ParseUserID(result.UserID)
	if err != nil {
		return entity.User{}, entity.ErrBadCredentials
	}
	return s.createUser(ctx, scope, localpart, result.DisplayName)
}

func (s *srv) rehashIfStale(ctx context.Context, scope entity.TenantScope, userID, password string) {
	credential, err := s.credentials.Get(ctx, scope, userID)
	if err != nil || !credential.NeedsRehash(s.argon2Params()) {
		return
	}
	replacement, err := entity.NewCredential(userID, password, s.argon2Params(), s.rnd)
	if err != nil {
		return
	}
	if err := s.credentials.Upsert(ctx, scope, replacement); err != nil {
		slog.WarnContext(ctx, "could not re-hash a password at the current cost", "error", err)
	}
}

func (s *srv) issue(ctx context.Context, scope entity.TenantScope, userID, deviceID, displayName string, withRefresh bool) (service.Session, error) {
	if deviceID == "" {
		generated, err := entity.GenerateDeviceID(s.rnd)
		if err != nil {
			return service.Session{}, err
		}
		deviceID = generated
	}
	if err := entity.ValidateDeviceID(deviceID); err != nil {
		return service.Session{}, err
	}

	in := entity.NewDevice{
		TenantID:    scope.ID(),
		UserID:      userID,
		DeviceID:    deviceID,
		DisplayName: displayName,
	}
	if err := in.Validate(); err != nil {
		return service.Session{}, err
	}
	if _, err := s.devices.Upsert(ctx, in); err != nil {
		return service.Session{}, err
	}
	s.announceDevices(ctx, scope, userID)

	if _, err := s.tokens.RevokeForDevice(ctx, scope, userID, deviceID); err != nil {
		return service.Session{}, err
	}
	if _, err := s.refresh.RevokeForDevice(ctx, scope, userID, deviceID, s.clock().UTC()); err != nil {
		return service.Session{}, err
	}

	session := service.Session{UserID: userID, DeviceID: deviceID}
	ttl := time.Duration(0)
	if withRefresh {
		ttl = s.cfg.AccessTokenTTL
		session.ExpiresIn = ttl
	}

	var refreshID *uuid.UUID
	if withRefresh {
		secret, stored, err := s.mintRefresh(ctx, scope, userID, deviceID)
		if err != nil {
			return service.Session{}, err
		}
		session.RefreshToken = secret
		refreshID = &stored
	}

	secret, _, err := s.tokens.MintForDevice(ctx, scope, userID, deviceID, ttl, refreshID)
	if err != nil {
		return service.Session{}, err
	}
	session.AccessToken = secret
	return session, nil
}

func (s *srv) Refresh(ctx context.Context, scope entity.TenantScope, presented string) (service.Session, error) {
	stored, err := s.refresh.GetByHash(ctx, hashSecret(presented))
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return service.Session{}, entity.ErrRefreshTokenNotFound
		}
		return service.Session{}, err
	}
	if stored.TenantID != scope.ID() {
		return service.Session{}, entity.ErrRefreshTokenNotFound
	}
	if err := stored.Usable(s.clock().UTC()); err != nil {
		return service.Session{}, err
	}

	var session service.Session
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.refresh.MarkUsed(ctx, stored.ID, s.clock().UTC()); err != nil {
			return err
		}
		if _, err := s.tokens.RevokeForDevice(ctx, scope, stored.UserID, stored.DeviceID); err != nil {
			return err
		}
		session, err = s.issueRefreshed(ctx, scope, stored)
		return err
	})
	if err != nil {
		return service.Session{}, err
	}
	return session, nil
}

func (s *srv) issueRefreshed(ctx context.Context, scope entity.TenantScope, old entity.RefreshToken) (service.Session, error) {
	secret, stored, err := s.mintRefresh(ctx, scope, old.UserID, old.DeviceID)
	if err != nil {
		return service.Session{}, err
	}
	access, _, err := s.tokens.MintForDevice(ctx, scope, old.UserID, old.DeviceID, s.cfg.AccessTokenTTL, &stored)
	if err != nil {
		return service.Session{}, err
	}
	return service.Session{
		UserID:       old.UserID,
		DeviceID:     old.DeviceID,
		AccessToken:  access,
		RefreshToken: secret,
		ExpiresIn:    s.cfg.AccessTokenTTL,
	}, nil
}

func (s *srv) announceDevices(ctx context.Context, scope entity.TenantScope, userID string) {
	if s.deviceLists == nil {
		return
	}
	if err := s.deviceLists.Record(ctx, scope, userID); err != nil {
		slog.WarnContext(ctx, "a device change was not announced", "user_id", userID, "error", err)
	}
}

func (s *srv) Logout(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.tokens.RevokeForDevice(ctx, scope, caller.UserID, caller.DeviceID); err != nil {
			return err
		}
		if _, err := s.refresh.RevokeForDevice(ctx, scope, caller.UserID, caller.DeviceID, s.clock().UTC()); err != nil {
			return err
		}
		s.announceDevices(ctx, scope, caller.UserID)
		if err := s.devices.Delete(ctx, scope, caller.UserID, caller.DeviceID); err != nil &&
			!errors.Is(err, repository.ErrDeviceNotFound) {
			return err
		}
		return nil
	})
}

func (s *srv) LogoutAll(ctx context.Context, scope entity.TenantScope, userID string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.tokens.RevokeForUser(ctx, scope, userID); err != nil {
			return err
		}
		if _, err := s.refresh.RevokeForUser(ctx, scope, userID, s.clock().UTC()); err != nil {
			return err
		}
		s.announceDevices(ctx, scope, userID)
		_, err := s.devices.DeleteAllForUser(ctx, scope, userID)
		return err
	})
}

func (s *srv) Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.User, error) {
	user, err := s.users.Get(ctx, scope, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return entity.User{}, entity.ErrUserNotFound
		}
		return entity.User{}, err
	}
	return user, nil
}

func (s *srv) UpdateProfile(ctx context.Context, scope entity.TenantScope, caller, target string, in entity.UpdateProfile) (entity.User, error) {
	if caller != target {
		return entity.User{}, entity.ErrProfileNotAllowed
	}
	if err := in.Validate(); err != nil {
		return entity.User{}, err
	}
	updated, err := s.users.UpdateProfile(ctx, scope, target, in)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return entity.User{}, entity.ErrUserNotFound
		}
		return entity.User{}, err
	}
	return updated, nil
}

func (s *srv) Devices(ctx context.Context, scope entity.TenantScope, userID string) ([]entity.Device, error) {
	return s.devices.ListForUser(ctx, scope, userID)
}

func (s *srv) Device(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (entity.Device, error) {
	found, err := s.devices.Get(ctx, scope, userID, deviceID)
	if err != nil {
		if errors.Is(err, repository.ErrDeviceNotFound) {
			return entity.Device{}, entity.ErrDeviceNotFound
		}
		return entity.Device{}, err
	}
	return found, nil
}

func (s *srv) RenameDevice(ctx context.Context, scope entity.TenantScope, userID, deviceID, displayName string) error {
	if err := s.devices.Rename(ctx, scope, userID, deviceID, displayName); err != nil {
		if errors.Is(err, repository.ErrDeviceNotFound) {
			return entity.ErrDeviceNotFound
		}
		return err
	}
	return nil
}

func (s *srv) DeleteDevices(ctx context.Context, scope entity.TenantScope, userID string, deviceIDs []string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		for _, deviceID := range slices.Compact(slices.Clone(deviceIDs)) {
			if _, err := s.tokens.RevokeForDevice(ctx, scope, userID, deviceID); err != nil {
				return err
			}
			if _, err := s.refresh.RevokeForDevice(ctx, scope, userID, deviceID, s.clock().UTC()); err != nil {
				return err
			}
			s.announceDevices(ctx, scope, userID)
			err := s.devices.Delete(ctx, scope, userID, deviceID)
			if err != nil && !errors.Is(err, repository.ErrDeviceNotFound) {
				return err
			}
		}
		return nil
	})
}

func (s *srv) ChangePassword(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken, newPassword string, logoutDevices bool) error {
	if err := entity.CheckPasswordStrength(newPassword); err != nil {
		return err
	}
	credential, err := entity.NewCredential(caller.UserID, newPassword, s.argon2Params(), s.rnd)
	if err != nil {
		return err
	}

	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.credentials.Upsert(ctx, scope, credential); err != nil {
			return err
		}
		if !logoutDevices {
			return nil
		}
		devices, err := s.devices.ListForUser(ctx, scope, caller.UserID)
		if err != nil {
			return err
		}
		for _, device := range devices {
			if device.DeviceID == caller.DeviceID {
				continue
			}
			if err := s.DeleteDevices(ctx, scope, caller.UserID, []string{device.DeviceID}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *srv) Deactivate(ctx context.Context, scope entity.TenantScope, userID string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.users.Deactivate(ctx, scope, userID, s.clock().UTC()); err != nil {
			if errors.Is(err, repository.ErrUserNotFound) {
				return entity.ErrUserNotFound
			}
			return err
		}
		if err := s.credentials.Delete(ctx, scope, userID); err != nil {
			return err
		}
		return s.LogoutAll(ctx, scope, userID)
	})
}

func (s *srv) Touch(ctx context.Context, scope entity.TenantScope, caller entity.AccessToken, ip string) {
	if caller.DeviceID == "" {
		return
	}
	if err := s.devices.Touch(ctx, scope, caller.UserID, caller.DeviceID, ip, s.clock().UTC()); err != nil {
		slog.WarnContext(ctx, "could not record device activity", "error", err)
	}
}

func (s *srv) mintRefresh(ctx context.Context, scope entity.TenantScope, userID, deviceID string) (string, uuid.UUID, error) {
	secret, err := randomSecret(s.rnd)
	if err != nil {
		return "", uuid.UUID{}, err
	}
	in := entity.NewRefreshToken{
		TenantID:  scope.ID(),
		UserID:    userID,
		DeviceID:  deviceID,
		TokenHash: hashSecret(secret),
	}
	if s.cfg.RefreshTokenTTL > 0 {
		at := s.clock().UTC().Add(s.cfg.RefreshTokenTTL)
		in.ExpiresAt = &at
	}
	if err := in.Validate(); err != nil {
		return "", uuid.UUID{}, err
	}
	stored, err := s.refresh.Insert(ctx, in)
	if err != nil {
		return "", uuid.UUID{}, fmt.Errorf("users: mint refresh token: %w", err)
	}
	return secret, stored.ID, nil
}

func randomSecret(rnd io.Reader) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rnd, raw); err != nil {
		return "", fmt.Errorf("users: generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
