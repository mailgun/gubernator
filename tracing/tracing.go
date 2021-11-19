package tracing

// General purpose Opentracing functions.

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/opentracing/opentracing-go"
)

// Start a span using the full function name as the operation name.
// When done, be sure to call span.Finish().
func StartSpan(ctx context.Context) (opentracing.Span, context.Context) {
	operationName, fileTag := getCallerInfoForTracing(2)

	span, ctx2 := opentracing.StartSpanFromContext(ctx, operationName)
	span.SetTag("file", fileTag)

	return span, ctx2
}

// Start a span using given operation name.
// When done, be sure to call span.Finish().
func StartNamedSpan(ctx context.Context, operationName string) (opentracing.Span, context.Context) {
	_, fileTag := getCallerInfoForTracing(2)

	span, ctx2 := opentracing.StartSpanFromContext(ctx, operationName)
	span.SetTag("file", fileTag)

	return span, ctx2
}

func getCallerInfoForTracing(stackIndex int) (string, string) {
	fileTag := "unknown"
	operationName := "unknown"
	pc, file, line, callerOk := runtime.Caller(stackIndex)

	if callerOk {
		operationName = runtime.FuncForPC(pc).Name()
		fileTag = file + ":" + strconv.Itoa(line)
	}

	return operationName, fileTag
}

// Log a message to span.
// Optionally pass additional key/value pairs.
func LogInfo(span opentracing.Span, message string, keyValues ...interface{}) {
	args := append(
		[]interface{}{
			"event", "info",
			"event.message", message,
		},
		keyValues...,
	)
	span.LogKV(args...)
}

// Do context.WithTimeout and log details of the deadline origin.
func ContextWithTimeout(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(duration)
	_, fn, line, _ := runtime.Caller(1)

	if span := opentracing.SpanFromContext(ctx); span != nil {
		LogInfo(span, "Set context deadline",
			"deadline", deadline.Format(time.RFC3339),
			"source", fmt.Sprintf("%s:%d", fn, line),
		)
	}

	return context.WithTimeout(ctx, duration)
}
