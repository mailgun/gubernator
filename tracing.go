package gubernator

// General purpose Opentracing functions.

import (
	"context"
	"runtime"
	"strconv"

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
func LogSpan(span opentracing.Span, level string, message string, keyValues ...interface{}) {
	args := append(
		[]interface{}{
			"event", level,
			"event.message", message,
		},
		keyValues...,
	)
	span.LogKV(args...)
}
