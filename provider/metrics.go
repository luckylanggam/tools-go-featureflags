package provider

import (
	"context"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// PollFailure says why a poll did not produce a new snapshot.
type PollFailure string

const (
	// PollFailureFetch: AppConfig could not be reached or returned an error.
	PollFailureFetch PollFailure = "fetch"
	// PollFailureInvalid: AppConfig returned a version that does not parse,
	// validate or compile; the previous snapshot is kept.
	PollFailureInvalid PollFailure = "invalid"
)

// metrics holds the provider's instruments:
//   - featureflags.evaluations {flag, enabled, reason}: one per flag check;
//   - featureflags.poll.failures {profile, failure}: polls that produced no
//     new snapshot;
//   - featureflags.snapshot.age {source, flags_version, catalog_version}:
//     seconds since the source last confirmed the snapshot, observed at
//     collection time.
type metrics struct {
	evaluations  metric.Int64Counter
	pollFailures metric.Int64Counter
	registration metric.Registration
}

func newMetrics(mp metric.MeterProvider, s *store) (*metrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(engine.MeterName)

	evaluations, err := meter.Int64Counter("featureflags.evaluations",
		metric.WithDescription("Flag evaluations by flag, decision and reason."))
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrMetricsInstrument, "featureflags.evaluations", err)
	}
	pollFailures, err := meter.Int64Counter("featureflags.poll.failures",
		metric.WithDescription("AppConfig polls that produced no new snapshot."))
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrMetricsInstrument, "featureflags.poll.failures", err)
	}
	age, err := meter.Float64ObservableGauge("featureflags.snapshot.age",
		metric.WithUnit("s"),
		metric.WithDescription("Seconds since the snapshot being served was last confirmed by its source."))
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrMetricsInstrument, "featureflags.snapshot.age", err)
	}
	registration, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		info := s.Snapshot()
		o.ObserveFloat64(age, s.now().Sub(info.SyncedAt).Seconds(), metric.WithAttributes(
			attribute.String("source", string(info.Source)),
			attribute.String("flags_version", info.FlagsVersion),
			attribute.String("catalog_version", info.CatalogVersion),
		))
		return nil
	}, age)
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrMetricsInstrument, "featureflags.snapshot.age", err)
	}

	return &metrics{evaluations: evaluations, pollFailures: pollFailures, registration: registration}, nil
}

func (m *metrics) evaluation(ctx context.Context, d engine.Decision) {
	m.evaluations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("flag", d.Flag),
		attribute.String("enabled", strconv.FormatBool(d.Enabled)),
		attribute.String("reason", string(d.Reason)),
	))
}

func (m *metrics) pollFailed(ctx context.Context, profile string, failure PollFailure) {
	m.pollFailures.Add(ctx, 1, metric.WithAttributes(
		attribute.String("profile", profile),
		attribute.String("failure", string(failure)),
	))
}

func (m *metrics) close() error {
	return m.registration.Unregister()
}
