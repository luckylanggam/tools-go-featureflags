package engine

import "go.opentelemetry.io/otel/metric"

// Option configures New.
type Option func(*options)

type options struct {
	registry      *Registry
	undecided     bool
	meterProvider metric.MeterProvider
}

// WithRegistry gives the engine the service's custom operators and
// resolvers.
func WithRegistry(r *Registry) Option {
	return func(o *options) { o.registry = r }
}

// WithUnregisteredOperatorsUndecided is for tools that evaluate a service's
// flags without its code, such as the Flag Console. A custom operator the
// catalog declares but the registry lacks no longer fails New; a rule that
// needs it is reported with ReasonDecidedByService. Services must not use
// it: they rely on New rejecting a document they cannot evaluate.
func WithUnregisteredOperatorsUndecided() Option {
	return func(o *options) { o.undecided = true }
}

// WithMeterProvider sets where custom operator and resolver latency is
// recorded. The default is the global OpenTelemetry meter provider.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *options) { o.meterProvider = mp }
}
