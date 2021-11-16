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
	fileTag := "unknown"
	operationName := "unknown"
	pc, file, line, callerOk := runtime.Caller(1)

	if callerOk {
		operationName = runtime.FuncForPC(pc).Name()
		fileTag = file + ":" + strconv.Itoa(line)
	}

	span, ctx2 := opentracing.StartSpanFromContext(ctx, operationName)
	span.SetTag("file", fileTag)

	return span, ctx2
}

// Start a span using given operation name.
// When done, be sure to call span.Finish().
func StartNamedSpan(ctx context.Context, operationName string) (opentracing.Span, context.Context) {
	fileTag := "unknown"
	_, file, line, callerOk := runtime.Caller(1)

	if callerOk {
		fileTag = file + ":" + strconv.Itoa(line)
	}

	span, ctx2 := opentracing.StartSpanFromContext(ctx, operationName)
	span.SetTag("file", fileTag)

	return span, ctx2
}
