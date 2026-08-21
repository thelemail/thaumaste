package serialiser

import (
	"context"
	"sync"
)

type Serialiser struct {
	mu    sync.Mutex
	gates map[string]*gate
}

type gate struct {
	ch   chan struct{}
	refs int
}

func New() *Serialiser {
	return &Serialiser{gates: make(map[string]*gate)}
}

func (s *Serialiser) Do(ctx context.Context, key string, fn func(context.Context) error) error {
	ch := s.acquire(key)
	defer s.forget(key)

	select {
	case ch <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-ch }()

	return fn(ctx)
}

func (s *Serialiser) acquire(key string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.gates[key]
	if !ok {
		g = &gate{ch: make(chan struct{}, 1)}
		s.gates[key] = g
	}
	g.refs++
	return g.ch
}

func (s *Serialiser) forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.gates[key]
	if !ok {
		return
	}
	g.refs--
	if g.refs == 0 {
		delete(s.gates, key)
	}
}

func (s *Serialiser) tracked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.gates)
}
