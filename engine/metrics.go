package engine

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// MeterName is the OpenTelemetry meter the library records its metrics on.
const MeterName = "github.com/luckylanggam/tools-go-featureflags"

// Histograms of custom code latency in seconds. Operator samples carry the
// operator and outcome (ok, error) attributes; resolver samples carry the
// attribute and outcome.
const (
	MetricOperatorDuration = "featureflags.operator.duration"
	MetricResolverDuration = "featureflags.resolver.duration"
)

type metrics struct {
	operator metric.Float64Histogram
	resolver metric.Float64Histogram
}

func newMetrics(mp metric.MeterProvider) (*metrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(MeterName)
	operator, err := meter.Float64Histogram(MetricOperatorDuration, metric.WithUnit("s"),
		metric.WithDescription("Time spent in custom operators."))
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineMetrics, err)
	}
	resolver, err := meter.Float64Histogram(MetricResolverDuration, metric.WithUnit("s"),
		metric.WithDescription("Time spent in attribute resolvers."))
	if err != nil {
		return nil, fmt.Errorf(ffErrors.ErrEngineMetrics, err)
	}
	return &metrics{operator: operator, resolver: resolver}, nil
}

// observer records the latency of one custom operator or resolver, with its
// attribute sets built once at compile time.
type observer struct {
	hist   metric.Float64Histogram
	ok     metric.RecordOption
	failed metric.RecordOption
}

func newObserver(hist metric.Float64Histogram, key, name string) observer {
	set := func(outcome string) metric.RecordOption {
		return metric.WithAttributeSet(attribute.NewSet(attribute.String(key, name), attribute.String("outcome", outcome)))
	}
	return observer{hist: hist, ok: set("ok"), failed: set("error")}
}

func (o observer) record(ctx context.Context, start time.Time, err error) {
	opt := o.ok
	if err != nil {
		opt = o.failed
	}
	o.hist.Record(ctx, time.Since(start).Seconds(), opt)
}
