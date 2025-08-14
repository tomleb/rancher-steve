package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const name = "github.com/rancher/steve/pkg/otel"

var (
	Tracer = otel.Tracer(name)
)

// Creates a new root span
func RootStart(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer.Start(ctx, spanName, opts...)
}

// Start creates a span only if a span already exists
func Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer(name).Start(ctx, spanName, opts...)
}
