// Package grpc has gRPC server interceptors that put each call's common
// flag attributes in its context, the gRPC counterpart of
// middleware.Attributes.
package grpc

import (
	"context"

	gogrpc "google.golang.org/grpc"

	"github.com/luckylanggam/tools-go-featureflags/engine"
)

// AttributesFunc returns the attributes for a call, typically read from its
// incoming metadata. It may return nil.
type AttributesFunc func(ctx context.Context, fullMethod string) engine.Attributes

// UnaryServerInterceptor sets the attributes fn returns once per unary call
// (see engine.WithAttributes).
func UnaryServerInterceptor(fn AttributesFunc) gogrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
		return handler(engine.WithAttributes(ctx, fn(ctx, info.FullMethod)), req)
	}
}

// StreamServerInterceptor sets the attributes fn returns once per stream,
// so every message on it shares them and their resolver cache.
func StreamServerInterceptor(fn AttributesFunc) gogrpc.StreamServerInterceptor {
	return func(srv any, ss gogrpc.ServerStream, info *gogrpc.StreamServerInfo, handler gogrpc.StreamHandler) error {
		ctx := engine.WithAttributes(ss.Context(), fn(ss.Context(), info.FullMethod))
		return handler(srv, &serverStream{ServerStream: ss, ctx: ctx})
	}
}

// serverStream replaces the stream's context.
type serverStream struct {
	gogrpc.ServerStream
	ctx context.Context
}

func (s *serverStream) Context() context.Context { return s.ctx }
