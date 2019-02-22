package common

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/mailgun/holster/stack"
	"github.com/sirupsen/logrus"
)

func ExpandNested(key string, value interface{}, dest map[string]interface{}) {
	if strings.ContainsRune(key, '.') {
		parts := strings.SplitN(key, ".", 2)
		// This nested value might already exist
		nested, isMap := dest[parts[0]].(map[string]interface{})
		if !isMap {
			// if not a map, overwrite current entry and make it a map
			nested = make(map[string]interface{})
			dest[parts[0]] = nested
		}
		ExpandNested(parts[1], value, nested)
		return
	}
	switch value.(type) {
	case *http.Request:
		dest[key] = RequestToMap(value.(*http.Request))
	default:
		dest[key] = value
	}
}

// Given a *http.Request return a map with detailed information about the request
func RequestToMap(req *http.Request) map[string]interface{} {
	var form []byte
	var err error

	// Scrub auth information
	headers := req.Header
	headers.Del("Authorization")
	headers.Del("Cookie")

	if len(req.Form) != 0 {
		form, err = json.MarshalIndent(req.Form, "", "  ")
		if err != nil {
			form = []byte(fmt.Sprintf("JSON Encode Error: %s", err))
		}
	}

	headersJSON, err := json.MarshalIndent(headers, "", "  ")
	if err != nil {
		headersJSON = []byte(fmt.Sprintf("JSON Encode Error: %s", err))
	}

	return map[string]interface{}{
		"headers-json": string(headersJSON),
		"ip":           req.RemoteAddr,
		"method":       req.Method,
		"params-json":  string(form),
		"size":         req.ContentLength,
		"url":          req.URL.String(),
		"useragent":    req.Header.Get("User-Agent"),
	}
}

// Returns the file, function and line number of the function that called logrus
func GetLogrusCaller() *stack.FrameInfo {
	var frames [32]uintptr

	// iterate until we find non logrus function
	length := runtime.Callers(5, frames[:])
	for idx := 0; idx < (length - 1); idx++ {
		pc := uintptr(frames[idx]) - 1
		fn := runtime.FuncForPC(pc)
		funcName := fn.Name()
		if strings.Contains(strings.ToLower(funcName), "sirupsen/logrus") {
			continue
		}
		filePath, lineNo := fn.FileLine(pc)
		return &stack.FrameInfo{
			Func:   stack.FuncName(fn),
			File:   filePath,
			LineNo: lineNo,
		}
	}
	return &stack.FrameInfo{}
}

// Returns true if the key exists in the map
func Exists(haystack map[string]interface{}, needle string) bool {
	_, exists := haystack[needle]
	return exists
}

// Convert a map to logrus fields
func ToFields(items map[string]string) logrus.Fields {
	result := logrus.Fields{}
	for key, value := range items {
		result[key] = value
	}
	return result
}
