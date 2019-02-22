package common

import (
	"fmt"

	"github.com/mailgun/holster/errors"
	"github.com/mailgun/holster/stack"
	"github.com/sirupsen/logrus"
)

type Number float64

func (n Number) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%f", n)), nil
}

//easyjson:json
type LogRecord struct {
	Context   map[string]interface{} `json:"context,omitempty"`
	Category  string                 `json:"category,omitempty"`
	AppName   string                 `json:"appname"`
	HostName  string                 `json:"hostname"`
	LogLevel  string                 `json:"logLevel"`
	FileName  string                 `json:"filename"`
	FuncName  string                 `json:"funcName"`
	LineNo    int                    `json:"lineno"`
	Message   string                 `json:"message"`
	Timestamp Number                 `json:"timestamp"`
	CID       string                 `json:"cid,omitempty"`
	PID       int                    `json:"pid,omitempty"`
	TID       string                 `json:"tid,omitempty"`
	ExcType   string                 `json:"excType,omitempty"`
	ExcText   string                 `json:"excText,omitempty"`
	ExcValue  string                 `json:"excValue,omitempty"`
}

func (r *LogRecord) FromFields(fields logrus.Fields) {
	if len(fields) == 0 {
		return
	}
	r.Context = make(map[string]interface{})
	for k, v := range fields {
		switch k {
		// logrus.WithError adds a field with name error.
		case "error":
			fallthrough
		case "err":
			// Record details of the error
			if v, ok := v.(error); ok {
				r.ExcValue = v.Error()
				r.ExcType = fmt.Sprintf("%T", errors.Cause(v))
				r.ExcText = fmt.Sprintf("%+v", v)

				// Extract the stack info if provided
				if v, ok := v.(stack.HasStackTrace); ok {
					trace := v.StackTrace()
					caller := stack.GetLastFrame(trace)
					r.FuncName = caller.Func
					r.LineNo = caller.LineNo
					r.FileName = caller.File
				}

				// Extract context if provided
				if ctx, ok := v.(errors.HasContext); ok {
					for ck, cv := range ctx.Context() {
						r.Context[ck] = cv
					}
				}
				continue
			}
		case "tid":
			if v, ok := v.(string); ok {
				r.TID = v
				continue
			}
		case "excValue":
			if v, ok := v.(string); ok {
				r.ExcValue = v
				continue
			}
		case "excType":
			if v, ok := v.(string); ok {
				r.ExcType = v
				continue
			}
		case "excText":
			if v, ok := v.(string); ok {
				r.ExcText = v
				continue
			}
		case "excFuncName":
			if v, ok := v.(string); ok {
				r.FuncName = v
				continue
			}
		case "excLineno":
			if v, ok := v.(int); ok {
				r.LineNo = v
				continue
			}
		case "excFileName":
			if v, ok := v.(string); ok {
				r.FileName = v
				continue
			}
		case "category":
			if v, ok := v.(string); ok {
				r.Category = v
				continue
			}
		}
		ExpandNested(k, v, r.Context)
	}
}
