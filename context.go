package gubernator

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/opentracing/opentracing-go"
)

type DeadlineMap map[string]time.Time

// Key to context.Value containing deadline map.
var DEADLINE_MAP_KEY = struct{}{}

// Do context.WithTimeout with added decoration for tracking 1 or more deadlines.
func DecoratedContextWithTimeout(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(duration)
	deadlineStr := deadline.Format(time.RFC3339)
	_, fn, line, _ := runtime.Caller(1)
	deadlineName := fmt.Sprintf("%s:%d", fn, line)

	var deadlineMap DeadlineMap
	var ok bool
	if deadlineMap, ok = ctx.Value(DEADLINE_MAP_KEY).(DeadlineMap); !ok {
		deadlineMap = DeadlineMap{}
	}
	deadlineMap[deadlineName] = deadline

	if span := opentracing.SpanFromContext(ctx); span != nil {
		LogSpan(span, "info", "Set context deadline",
			"deadline", deadlineStr,
			"source", deadlineName,
		)
	}

	ctx2 := context.WithValue(ctx, DEADLINE_MAP_KEY, deadlineMap)
	return context.WithTimeout(ctx2, duration)
}
