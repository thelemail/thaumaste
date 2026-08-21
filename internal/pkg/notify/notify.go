package notify

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	resubscribeAfter = 5 * time.Second
	keySeparator     = "\x1e"
)

type Bus interface {
	Publish(ctx context.Context, channel, message string) error
	Subscribe(ctx context.Context, channel string, deliver func(string)) error
}

type Notifier struct {
	bus     Bus
	channel string

	mu      sync.Mutex
	waiters map[string]map[*waiter]struct{}
}

type waiter struct {
	woken chan struct{}
	keys  []string
}

func New(bus Bus, channel string) *Notifier {
	return &Notifier{bus: bus, channel: channel, waiters: make(map[string]map[*waiter]struct{})}
}

func (n *Notifier) Run(ctx context.Context) error {
	if n.bus == nil {
		<-ctx.Done()
		return nil
	}
	for ctx.Err() == nil {
		if err := n.bus.Subscribe(ctx, n.channel, n.deliver); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "sync wake-ups are not crossing instances", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(resubscribeAfter):
		}
	}
	return nil
}

func (n *Notifier) Name() string { return "notifier" }

func (n *Notifier) Notify(ctx context.Context, keys ...string) {
	if len(keys) == 0 {
		return
	}
	n.deliver(strings.Join(keys, keySeparator))
	if n.bus == nil {
		return
	}
	if err := n.bus.Publish(ctx, n.channel, strings.Join(keys, keySeparator)); err != nil {
		slog.WarnContext(ctx, "sync wake-up stayed on this instance", "error", err)
	}
}

func (n *Notifier) deliver(message string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, key := range strings.Split(message, keySeparator) {
		for w := range n.waiters[key] {
			select {
			case w.woken <- struct{}{}:
			default:
			}
		}
	}
}

func (n *Notifier) Wait(keys []string) (<-chan struct{}, func()) {
	w := &waiter{woken: make(chan struct{}, 1), keys: keys}

	n.mu.Lock()
	for _, key := range keys {
		if n.waiters[key] == nil {
			n.waiters[key] = make(map[*waiter]struct{})
		}
		n.waiters[key][w] = struct{}{}
	}
	n.mu.Unlock()

	return w.woken, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, key := range w.keys {
			delete(n.waiters[key], w)
			if len(n.waiters[key]) == 0 {
				delete(n.waiters, key)
			}
		}
	}
}

func (n *Notifier) Watching() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.waiters)
}
