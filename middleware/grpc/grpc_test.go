package grpc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/luckylanggam/tools-go-featureflags/engine"
	ffgrpc "github.com/luckylanggam/tools-go-featureflags/middleware/grpc"
)

// fromMetadata reads site_id from the incoming metadata.
func fromMetadata(ctx context.Context, fullMethod string) engine.Attributes {
	md, _ := metadata.FromIncomingContext(ctx)
	return engine.Attributes{"site_id": md.Get("site-id")[0], "method": fullMethod}
}

func incoming() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("site-id", "7"))
}

func TestUnaryServerInterceptor(t *testing.T) {
	var got engine.Attributes
	resp, err := ffgrpc.UnaryServerInterceptor(fromMetadata)(incoming(), "req",
		&gogrpc.UnaryServerInfo{FullMethod: "/svc/Get"},
		func(ctx context.Context, req any) (any, error) {
			got = engine.AttributesFromContext(ctx)
			return req, nil
		})

	require.NoError(t, err)
	assert.Equal(t, "req", resp)
	assert.Equal(t, engine.Attributes{"site_id": "7", "method": "/svc/Get"}, got)
}

type fakeStream struct {
	gogrpc.ServerStream
	ctx context.Context
}

func (s fakeStream) Context() context.Context { return s.ctx }

func TestStreamServerInterceptor(t *testing.T) {
	var got engine.Attributes
	err := ffgrpc.StreamServerInterceptor(fromMetadata)(nil, fakeStream{ctx: incoming()},
		&gogrpc.StreamServerInfo{FullMethod: "/svc/Watch"},
		func(_ any, ss gogrpc.ServerStream) error {
			got = engine.AttributesFromContext(ss.Context())
			return nil
		})

	require.NoError(t, err)
	assert.Equal(t, engine.Attributes{"site_id": "7", "method": "/svc/Watch"}, got)
}
