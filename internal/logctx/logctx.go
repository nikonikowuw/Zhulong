// Package logctx propagates logging correlation fields through context.Context.
// Its dependency whitelist is context and go.uber.org/zap only.
package logctx

import (
	"context"

	"go.uber.org/zap"
)

type contextKey uint8

const (
	requestIDKey contextKey = iota + 1
	traceIDKey
	streamIDKey
)

// WithRequestID adds the HTTP request identifier to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the HTTP request identifier stored in ctx.
func RequestID(ctx context.Context) string { return value(ctx, requestIDKey) }

// WithTraceID adds a trace identifier to ctx.
func WithTraceID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceID returns the trace identifier stored in ctx.
func TraceID(ctx context.Context) string { return value(ctx, traceIDKey) }

// WithStreamID adds a long-lived stream identifier to ctx.
func WithStreamID(ctx context.Context, id string) context.Context {
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, streamIDKey, id)
}

// StreamID returns the long-lived stream identifier stored in ctx.
func StreamID(ctx context.Context) string { return value(ctx, streamIDKey) }

func value(ctx context.Context, key contextKey) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(key).(string)
	return id
}

// Fields returns populated correlation fields, or nil when ctx carries none.
func Fields(ctx context.Context) []zap.Field {
	if ctx == nil {
		return nil
	}
	var fields []zap.Field
	if id := RequestID(ctx); id != "" {
		fields = append(fields, zap.String("requestId", id))
	}
	if id := TraceID(ctx); id != "" {
		fields = append(fields, zap.String("traceId", id))
	}
	if id := StreamID(ctx); id != "" {
		fields = append(fields, zap.String("streamId", id))
	}
	return fields
}

// Logger returns base enriched with context correlation fields. A nil base uses zap.L().
func Logger(ctx context.Context, base *zap.Logger) *zap.Logger {
	if base == nil {
		base = zap.L()
	}
	fields := Fields(ctx)
	if len(fields) == 0 {
		return base
	}
	return base.With(fields...)
}
