package errors

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mailgun/holster/stack"
	pkg "github.com/pkg/errors"
)

// Implements the `error` `causer` and `Contexter` interfaces
type withContext struct {
	context WithContext
	msg     string
	cause   error
	stack   *stack.Stack
}

func (c *withContext) Cause() error {
	return c.cause
}

func (c *withContext) Error() string {
	if len(c.msg) == 0 {
		return c.cause.Error()
	}
	return c.msg + ": " + c.cause.Error()
}

func (c *withContext) StackTrace() pkg.StackTrace {
	if child, ok := c.cause.(stack.HasStackTrace); ok {
		return child.StackTrace()
	}
	return c.stack.StackTrace()
}

func (c *withContext) Context() map[string]interface{} {
	result := make(map[string]interface{}, len(c.context))
	for key, value := range c.context {
		result[key] = value
	}

	// downstream context values have precedence as they are closer to the cause
	if child, ok := c.cause.(HasContext); ok {
		downstream := child.Context()
		if downstream == nil {
			return result
		}

		for key, value := range downstream {
			result[key] = value
		}
	}
	return result
}

func (c *withContext) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		fmt.Fprintf(s, "%s: %+v (%s)", c.msg, c.Cause(), c.FormatFields())
	case 's', 'q':
		io.WriteString(s, c.Error())
	}
}

func (c *withContext) FormatFields() string {
	var buf bytes.Buffer
	var count int

	for key, value := range c.context {
		if count > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(fmt.Sprintf("%+v=%+v", key, value))
		count++
	}
	return buf.String()
}
