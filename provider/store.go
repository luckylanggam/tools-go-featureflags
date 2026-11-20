package provider

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/luckylanggam/tools-go-featureflags/engine"
)

// state is one immutable snapshot: the compiled engine plus what describes it.
type state struct {
	engine *engine.Engine
	info   Snapshot
}

// store serves evaluations from the current state, which writers replace
// atomically. Readers never lock.
type store struct {
	current atomic.Pointer[state]
	metrics *metrics
	now     func() time.Time
}

// newStore starts serving initial and registers the metrics.
func newStore(mp metric.MeterProvider, initial *state, now func() time.Time) (*store, error) {
	s := &store{now: now}
	s.swap(initial)
	m, err := newMetrics(mp, s)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	s.metrics = m
	return s, nil
}

func (s *store) swap(st *state) {
	s.current.Store(st)
}

func (s *store) load() *state {
	return s.current.Load()
}

func (s *store) IsEnabled(ctx context.Context, flag string, attrs engine.Attributes) (bool, error) {
	d, err := s.Evaluate(ctx, flag, attrs)
	return d.Enabled, err
}

func (s *store) Evaluate(ctx context.Context, flag string, attrs engine.Attributes) (engine.Decision, error) {
	d, err := s.load().engine.Evaluate(ctx, flag, attrs)
	s.metrics.evaluation(ctx, d)
	return d, err
}

func (s *store) Snapshot() Snapshot {
	return s.load().info
}
