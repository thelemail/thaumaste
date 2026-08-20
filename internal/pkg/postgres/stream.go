package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

const positionPersistTimeout = 5 * time.Second

var ErrNonPositiveCount = errors.New("postgres: stream position count must be positive")

type StreamConfig struct {
	Name     string
	Instance string
	Sequence string
	Negative bool
}

type Stream struct {
	db  *Client
	cfg StreamConfig

	mu       sync.Mutex
	inFlight []int64
	fetching []int64
	finished []int64
	maxSeen  int64
	upTo     int64
}

func NewStream(ctx context.Context, db *Client, cfg StreamConfig) (*Stream, error) {
	if cfg.Name == "" || cfg.Instance == "" || cfg.Sequence == "" {
		return nil, fmt.Errorf("postgres: stream needs a name, instance and sequence")
	}

	s := &Stream{db: db, cfg: cfg}

	var stored int64
	err := db.Querier(ctx).QueryRowContext(ctx,
		`SELECT stream_id FROM stream_positions WHERE stream_name = $1 AND instance_name = $2`,
		cfg.Name, cfg.Instance,
	).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("postgres: load stream position: %w", err)
	default:
		s.upTo = abs(stored)
		s.maxSeen = s.upTo
	}
	return s, nil
}

func (s *Stream) Current() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sign() * s.upTo
}

func (s *Stream) Next(ctx context.Context, n int) (*Positions, error) {
	if n <= 0 {
		return nil, ErrNonPositiveCount
	}

	s.mu.Lock()
	marker := s.maxSeen
	s.fetching = insert(s.fetching, marker)
	s.mu.Unlock()

	ids, err := s.allocate(ctx, n)

	s.mu.Lock()
	if err == nil {
		for _, id := range ids {
			s.inFlight = insert(s.inFlight, id)
		}
		s.maxSeen = max(s.maxSeen, ids[len(ids)-1])
	}
	s.fetching = remove(s.fetching, marker)
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}

	signed := make([]int64, len(ids))
	for i, id := range ids {
		signed[i] = s.sign() * id
	}
	return &Positions{stream: s, raw: ids, IDs: signed}, nil
}

func (s *Stream) allocate(ctx context.Context, n int) ([]int64, error) {
	rows, err := s.db.Querier(ctx).QueryContext(ctx,
		fmt.Sprintf(`SELECT nextval('%s') FROM generate_series(1, $1)`, s.cfg.Sequence), n)
	if err != nil {
		return nil, fmt.Errorf("postgres: allocate stream position: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scan stream position: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: allocate stream position: %w", err)
	}
	if len(ids) != n {
		return nil, fmt.Errorf("postgres: allocated %d stream positions, wanted %d", len(ids), n)
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *Stream) release(ids []int64) {
	s.mu.Lock()
	for _, id := range ids {
		s.inFlight = remove(s.inFlight, id)
		s.finished = insert(s.finished, id)
	}
	advanced := s.advance()
	upTo := s.upTo
	s.mu.Unlock()

	if advanced {
		s.persist(upTo)
	}
}

func (s *Stream) advance() bool {
	limit, bounded := s.minUnsafe()

	before := s.upTo
	if !bounded {
		if len(s.finished) > 0 {
			s.upTo = max(s.upTo, s.finished[len(s.finished)-1])
			s.finished = s.finished[:0]
		}
		return s.upTo != before
	}

	cut := 0
	for cut < len(s.finished) && s.finished[cut] < limit {
		cut++
	}
	if cut > 0 {
		s.upTo = max(s.upTo, s.finished[cut-1])
		s.finished = slices.Delete(s.finished, 0, cut)
	}
	return s.upTo != before
}

func (s *Stream) minUnsafe() (int64, bool) {
	switch {
	case len(s.inFlight) > 0 && len(s.fetching) > 0:
		return min(s.inFlight[0], s.fetching[0]+1), true
	case len(s.fetching) > 0:
		return s.fetching[0] + 1, true
	case len(s.inFlight) > 0:
		return s.inFlight[0], true
	default:
		return 0, false
	}
}

func (s *Stream) persist(upTo int64) {
	ctx, cancel := context.WithTimeout(context.Background(), positionPersistTimeout)
	defer cancel()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO stream_positions (stream_name, instance_name, stream_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (stream_name, instance_name) DO UPDATE SET stream_id = EXCLUDED.stream_id
		 WHERE stream_positions.stream_id < EXCLUDED.stream_id`,
		s.cfg.Name, s.cfg.Instance, s.sign()*upTo)
	if err != nil {
		slog.ErrorContext(ctx, "could not record stream position",
			"stream", s.cfg.Name, "position", s.sign()*upTo, "error", err)
	}
}

func (s *Stream) sign() int64 {
	if s.cfg.Negative {
		return -1
	}
	return 1
}

type Positions struct {
	IDs []int64

	stream *Stream
	raw    []int64
	once   sync.Once
}

func (p *Positions) Release() {
	p.once.Do(func() { p.stream.release(p.raw) })
}

func insert(xs []int64, v int64) []int64 {
	i, _ := slices.BinarySearch(xs, v)
	return slices.Insert(xs, i, v)
}

func remove(xs []int64, v int64) []int64 {
	if i, ok := slices.BinarySearch(xs, v); ok {
		return slices.Delete(xs, i, i+1)
	}
	return xs
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
