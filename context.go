package gubernator

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

type DeadlineMap map[string]time.Time

// Key to context.Value containing deadline map.
var DEADLINE_MAP_KEY = struct{}{}

// // Key to context.Value containing logrus logger.
// var LOGGER_KEY = struct{}{}

// Do context.WithTimeout with added decoration for tracking 1 or more deadlines.
func DecoratedContextWithTimeout(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(duration)
	_, fn, line, _ := runtime.Caller(1)
	deadlineName := fmt.Sprintf("%s:%d", fn, line)

	var deadlineMap DeadlineMap
	var ok bool
	if deadlineMap, ok = ctx.Value(DEADLINE_MAP_KEY).(DeadlineMap); !ok {
		deadlineMap = DeadlineMap{}
	}
	deadlineMap[deadlineName] = deadline

	ctx2 := context.WithValue(ctx, DEADLINE_MAP_KEY, deadlineMap)
	return context.WithTimeout(ctx2, duration)
}

// // Get logger stored in context values.
// func ContextWithLogger(ctx context.Context, logger *logrus.Logger) context.Context {
// 	return context.WithValue(ctx, LOGGER_KEY, logger)
// }
//
// // Store logger in context.
// func LoggerFromContext(ctx context.Context) *logrus.Logger {
// 	logger, ok := ctx.Value(LOGGER_KEY).(*logrus.Logger)
// 	if !ok {
// 		return logrus.StandardLogger()
// 	}
//
// 	return logger
// }
