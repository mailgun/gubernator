package gubernator

import (
	"context"
	"runtime"
	"strconv"

	"github.com/opentracing/opentracing-go"
)

func StartSpan(ctx context.Context) (opentracing.Span, context.Context) {
	fileTag := "unknown"
	operationName := "unknown"
	pc, file, line, callerOk := runtime.Caller(1)

	if callerOk {
		operationName = runtime.FuncForPC(pc).Name()
		fileTag = file + ":" + strconv.Itoa(line)
	} else {
		fileTag = "unknown"
	}

	span, ctx2 := opentracing.StartSpanFromContext(ctx, operationName)
	span.SetTag("file", fileTag)

	return span, ctx2
}
