package valkey

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/valkeylimiter"
	"github.com/valkey-io/valkey-go/valkeylock"

	"github.com/thelemail/thaumaste/internal/config"
)

const (
	reconnectEvery    = 5 * time.Second
	defaultLockWait   = 20 * time.Second
	defaultRateWindow = time.Second
)

var (
	ErrUnavailable = errors.New("valkey: unavailable")
	ErrNoAddress   = errors.New("valkey: no address configured")
	ErrHeld        = errors.New("valkey: lock is held elsewhere")
)

type Verdict struct {
	Allowed bool
	ResetAt time.Time
}

type session struct {
	conn    valkey.Client
	locker  valkeylock.Locker
	limiter valkeylimiter.RateLimiterClient
}

func (s *session) close() {
	s.limiter.Close()
	s.locker.Close()
	s.conn.Close()
}

type Client struct {
	cfg    config.Valkey
	limits config.Limits

	live atomic.Pointer[session]

	once   sync.Once
	closed chan struct{}
}

func New(ctx context.Context, cfg config.Valkey, limits config.Limits) (*Client, error) {
	if len(cfg.Addrs) == 0 {
		return nil, ErrNoAddress
	}

	c := &Client{cfg: cfg, limits: limits, closed: make(chan struct{})}

	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	go c.reconnect()
	return c, nil
}

func (c *Client) connect(ctx context.Context) error {
	options := valkey.ClientOption{
		InitAddress: c.cfg.Addrs,
		Username:    c.cfg.Username,
		Password:    c.cfg.Password,
		SelectDB:    c.cfg.SelectDB,
		Dialer:      net.Dialer{Timeout: c.cfg.DialTimeout},
	}

	conn, err := valkey.NewClient(options)
	if err != nil {
		return fmt.Errorf("valkey: connect: %w", err)
	}
	if err := conn.Do(ctx, conn.B().Ping().Build()).Error(); err != nil {
		conn.Close()
		return fmt.Errorf("valkey: ping: %w", err)
	}

	locker, err := valkeylock.NewLocker(valkeylock.LockerOption{
		ClientOption:   options,
		KeyPrefix:      c.cfg.KeyPrefix + ":lock",
		KeyMajority:    1,
		KeyValidity:    c.cfg.LockValidity,
		NoLoopTracking: true,
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("valkey: locker: %w", err)
	}

	limiter, err := valkeylimiter.NewRateLimiter(valkeylimiter.RateLimiterOption{
		ClientOption: options,
		KeyPrefix:    c.cfg.KeyPrefix + ":rate",
		Limit:        max(c.limits.SendPerUser, 1),
		Window:       max(c.limits.SendWindow, defaultRateWindow),
	})
	if err != nil {
		locker.Close()
		conn.Close()
		return fmt.Errorf("valkey: limiter: %w", err)
	}

	next := &session{conn: conn, locker: locker, limiter: limiter}
	if previous := c.live.Swap(next); previous != nil {
		previous.close()
	}
	return nil
}

func (c *Client) reconnect() {
	ticker := time.NewTicker(reconnectEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
		}
		if c.live.Load() != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.DialTimeout)
		if err := c.connect(ctx); err == nil {
			slog.InfoContext(ctx, "valkey is back")
		}
		cancel()
	}
}

func (c *Client) drop(current *session, err error) error {
	if c.live.CompareAndSwap(current, nil) {
		slog.Warn("lost valkey", "error", err)
		go current.close()
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

type lease struct {
	held    context.Context
	release context.CancelFunc
	err     error
}

func (c *Client) Lock(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
	live := c.live.Load()
	if live == nil {
		return nil, nil, ErrUnavailable
	}

	acquiring, abandon := context.WithCancel(ctx)
	taken := make(chan lease, 1)
	go func() {
		held, release, err := live.locker.WithContext(acquiring, name)
		taken <- lease{held: held, release: release, err: err}
	}()

	timer := time.NewTimer(c.waitFor())
	defer timer.Stop()

	select {
	case result := <-taken:
		if result.err != nil {
			abandon()
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, c.drop(live, result.err)
		}
		return result.held, func() { result.release(); abandon() }, nil
	case <-timer.C:
		abandon()
		go releaseLate(taken)
		return nil, nil, fmt.Errorf("%w: %q", ErrHeld, name)
	}
}

func releaseLate(taken <-chan lease) {
	if result := <-taken; result.err == nil {
		result.release()
	}
}

func (c *Client) waitFor() time.Duration {
	if c.cfg.LockWait > 0 {
		return c.cfg.LockWait
	}
	return defaultLockWait
}

func (c *Client) Allow(ctx context.Context, key string, limit int, window time.Duration) (Verdict, error) {
	live := c.live.Load()
	if live == nil {
		return Verdict{}, ErrUnavailable
	}
	result, err := live.limiter.Allow(ctx, key, valkeylimiter.WithCustomRateLimit(limit, window))
	if err != nil {
		if ctx.Err() != nil {
			return Verdict{}, ctx.Err()
		}
		return Verdict{}, c.drop(live, err)
	}
	return Verdict{Allowed: result.Allowed, ResetAt: time.UnixMilli(result.ResetAtMs).UTC()}, nil
}

func (c *Client) Publish(ctx context.Context, channel, message string) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	if err := live.conn.Do(ctx, live.conn.B().Publish().Channel(channel).Message(message).Build()).Error(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.drop(live, err)
	}
	return nil
}

func (c *Client) Subscribe(ctx context.Context, channel string, deliver func(string)) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	dedicated, cancel := live.conn.Dedicate()
	defer cancel()

	go func() {
		<-ctx.Done()
		cancel()
	}()

	err := dedicated.Receive(ctx, dedicated.B().Subscribe().Channel(channel).Build(),
		func(message valkey.PubSubMessage) { deliver(message.Message) })
	if err != nil && ctx.Err() == nil {
		return c.drop(live, err)
	}
	return ctx.Err()
}

func (c *Client) SortedAdd(ctx context.Context, key string, score int64, member string,
	ttl time.Duration,
) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	commands := valkey.Commands{
		live.conn.B().Zadd().Key(key).ScoreMember().ScoreMember(float64(score), member).Build(),
		live.conn.B().Pexpire().Key(key).Milliseconds(ttl.Milliseconds()).Build(),
	}
	for _, result := range live.conn.DoMulti(ctx, commands...) {
		if err := result.Error(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return c.drop(live, err)
		}
	}
	return nil
}

func (c *Client) SortedRange(ctx context.Context, key string, from int64) ([]string, error) {
	live := c.live.Load()
	if live == nil {
		return nil, ErrUnavailable
	}
	members, err := live.conn.Do(ctx, live.conn.B().Zrangebyscore().Key(key).
		Min(strconv.FormatInt(from, 10)).Max("+inf").Build()).AsStrSlice()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.drop(live, err)
	}
	return members, nil
}

func (c *Client) SortedRemove(ctx context.Context, key, member string) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	if err := live.conn.Do(ctx, live.conn.B().Zrem().Key(key).Member(member).Build()).Error(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.drop(live, err)
	}
	return nil
}

func (c *Client) SortedTrim(ctx context.Context, key string, upTo int64) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	err := live.conn.Do(ctx, live.conn.B().Zremrangebyscore().Key(key).
		Min("-inf").Max(strconv.FormatInt(upTo, 10)).Build()).Error()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return c.drop(live, err)
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	live := c.live.Load()
	if live == nil {
		return ErrUnavailable
	}
	return live.conn.Do(ctx, live.conn.B().Ping().Build()).Error()
}

func (c *Client) Close() {
	c.once.Do(func() {
		close(c.closed)
		if live := c.live.Swap(nil); live != nil {
			live.close()
		}
	})
}
