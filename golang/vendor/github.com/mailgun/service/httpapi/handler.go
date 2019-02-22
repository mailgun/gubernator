package httpapi

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/mailgun/service/internal/logging"
	"github.com/mailgun/service/internal/metrics"
	"github.com/mailgun/service/vulcand"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Response objects that apps' handlers are advised to return.
//
// Allows to easily return JSON-marshallable responses, e.g.:
//
//  Response{"message": "OK"}
type Response map[string]interface{}

// Represents handler's specification.
type Spec struct {
	// List of HTTP methods the handler should match.
	Method string

	// Canonical HTTP URL path.
	Path string

	// HTTP URL paths that should not be used any longer, but they have to be
	// kept around while users migrate off of them.
	DeprecatedPaths []string

	// Key/value pairs of specific HTTP headers the handler should match
	// (e.g. Content-Type).
	Headers []string

	// A handler function to use. Just one of these should be provided.
	RawHandler      http.HandlerFunc
	Handler         HandlerFunc
	HandlerWithBody HandlerWithBodyFunc

	// Unique identifier used when emitting performance metrics for the handler.
	MetricName string

	// Controls the handler's accessibility via vulcan (public or protected).
	// If not specified, public is assumed.
	Scope Scope

	// Vulcan middlewares to register with the handler. When registering,
	// middlewares are assigned priorities according to their positions in the
	// list: a middleware that appears in the list earlier is executed first.
	Middlewares []vulcand.Middleware
}

// Paths returns a list of paths to register that is comprised of the canonical
// and deprecated paths.
func (s *Spec) Paths() []string {
	paths := make([]string, len(s.DeprecatedPaths)+1)
	paths[0] = s.Path
	copy(paths[1:], s.DeprecatedPaths)
	return paths
}

func AddHandler(router *mux.Router, spec Spec) error {
	var httpHandlerFn http.HandlerFunc
	if spec.RawHandler != nil {
		httpHandlerFn = spec.RawHandler
	} else if spec.Handler != nil {
		httpHandlerFn = makeHandler(spec.Handler, spec)
	} else if spec.HandlerWithBody != nil {
		httpHandlerFn = makeHandlerWithBody(spec.HandlerWithBody, spec)
	} else {
		return errors.Errorf("spec does not provide handler: %v", spec)
	}
	for _, path := range spec.Paths() {
		route := router.HandleFunc(path, httpHandlerFn).Methods(spec.Method)
		if len(spec.Headers) != 0 {
			route.Headers(spec.Headers...)
		}
	}
	return nil
}

// DecodeParams given a map of parameters url decode each parameter.
func DecodeParams(src map[string]string) map[string]string {
	results := make(map[string]string, len(src))
	for key, param := range src {
		encoded, err := url.QueryUnescape(param)
		if err != nil {
			encoded = param
		}
		results[key] = encoded
	}
	return results
}

// HandlerFunc defines the signature of a handler function that can be
// registered by an app.
//
// The 3rd parameter is a map of variables extracted from the request path,
// e.g. if a request path was:
//   /resources/{resourceID}
// and the request was made to:
//   /resources/1
// then the map will contain the resource ID value:
//   {"resourceID": 1}
//
// A handler function should return a JSON marshallable object, e.g. Response.
type HandlerFunc func(http.ResponseWriter, *http.Request, map[string]string) (interface{}, error)

// makeHandler wraps the provided handler function encapsulating boilerplate
// code so handlers do not have to implement it themselves: parsing a request's
// form, formatting a proper JSON response, emitting the request stats, etc.
func makeHandler(fn HandlerFunc, spec Spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var response interface{}
		var status int
		var err error

		start := time.Now()
		if err = parseForm(r); err != nil {
			err = errors.Wrap(err, "Failed to parse request form")
			response = Response{"message": err.Error()}
			status = http.StatusInternalServerError
		} else {
			response, err = fn(w, r, DecodeParams(mux.Vars(r)))
			if err != nil {
				response, status = responseAndStatusFor(err)
			} else {
				status = http.StatusOK
			}
		}
		elapsedTime := time.Since(start)
		logRq(r, status, elapsedTime, err)
		metrics.Clt().TrackRequest(spec.MetricName, status, elapsedTime)

		Reply(w, response, status)
	}
}

// handlerWithBodyFunc defines a signature of a handler function, just like
// HandlerFunc.
//
// In addition to the HandlerFunc a request's body is passed into this function
// as a 4th parameter.
type HandlerWithBodyFunc func(http.ResponseWriter, *http.Request, map[string]string, []byte) (interface{}, error)

// makeHandlerWithBody Make a handler out of HandlerWithBodyFunc, just like
// regular makeHandler function.
func makeHandlerWithBody(fn HandlerWithBodyFunc, spec Spec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var response interface{}
		var body []byte
		var status int
		var err error

		start := time.Now()
		if err = parseForm(r); err != nil {
			err = errors.Wrap(err, "Failed to parse request form")
			response = Response{"message": err.Error()}
			status = http.StatusInternalServerError
			goto end
		}

		body, err = ioutil.ReadAll(r.Body)
		if err != nil {
			err = errors.Wrap(err, "Failed to read request body")
			response = Response{"message": err.Error()}
			status = http.StatusInternalServerError
			goto end
		}

		response, err = fn(w, r, mux.Vars(r), body)
		if err != nil {
			response, status = responseAndStatusFor(err)
		} else {
			status = http.StatusOK
		}

	end:
		elapsedTime := time.Since(start)
		logRq(r, status, elapsedTime, err)
		metrics.Clt().TrackRequest(spec.MetricName, status, elapsedTime)

		Reply(w, response, status)
	}
}

// Reply with the provided HTTP response and status code.
//
// response body must be JSON-marshallable, otherwise the response
// will be "Internal Server Error".
func Reply(w http.ResponseWriter, response interface{}, status int) {
	// marshal the body of the response
	marshalledResponse, err := json.Marshal(response)
	if err != nil {
		marshalledResponse = []byte(fmt.Sprintf(`{"message": "Failed to marshal response: %v %v"}`, response, err))
		status = http.StatusInternalServerError
		logRq(nil, status, time.Nanosecond, err)
	}

	// write JSON response
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(marshalledResponse)
}

// ReplyError converts registered error into HTTP response code and writes it back.
func ReplyError(w http.ResponseWriter, err error) {
	response, status := responseAndStatusFor(err)
	Reply(w, response, status)
}

// ReplyInternalError logs the error message and replies with a 500 status code.
func ReplyInternalError(w http.ResponseWriter, message string) {
	logRq(nil, http.StatusInternalServerError, time.Nanosecond, errors.New(message))
	Reply(w, Response{"message": message}, http.StatusInternalServerError)
}

// GetVarSafe is a helper function that returns the requested variable from URI
// with allowSet providing input sanitization. If an error occurs, returns
// either a `MissingFieldError` or an `UnsafeFieldError`.
func GetVarSafe(r *http.Request, variableName string, allowSet AllowSet) (string, error) {
	vars := mux.Vars(r)
	variableValue, ok := vars[variableName]

	if !ok {
		return "", MissingFieldError{variableName}
	}

	err := allowSet.IsSafe(variableValue)
	if err != nil {
		return "", UnsafeFieldError{variableName, err.Error()}
	}

	return variableValue, nil
}

// Parse the request data based on its content type.
func parseForm(r *http.Request) error {
	if isMultipart(r) == true {
		return r.ParseMultipartForm(0)
	} else {
		return r.ParseForm()
	}
}

// Determine whether the request is multipart/form-data or not.
func isMultipart(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return strings.HasPrefix(contentType, "multipart/form-data")
}

func logRq(r *http.Request, status int, elapsedTime time.Duration, err error) {
	var paramsJSON string
	if paramsBytes, err := json.MarshalIndent(r.Form, "", "  "); err == nil {
		paramsJSON = string(paramsBytes)
	}
	var headersJSON string
	if headersBytes, err := json.MarshalIndent(r.Header, "", "  "); err == nil {
		headersJSON = string(headersBytes)
	}
	log := logging.Log().WithFields(logrus.Fields{
		"http.request.method":       r.Method,
		"http.request.url":          r.URL.String(),
		"http.request.params-json":  paramsJSON,
		"http.request.headers-json": headersJSON,
		"http.request.useragent":    r.Header.Get("user-agent"),
		"http.response.status":      status,
		"category":                  "http",
	})

	clientIP := r.Header.Get("x-real-ip")
	if clientIP == "" {
		parts := strings.Split(r.RemoteAddr, ":")
		if len(parts) > 0 {
			clientIP = parts[0]
		}
	}
	if clientIP != "" {
		log = log.WithField("http.request.ip", clientIP)
	}

	if err != nil {
		log = log.WithError(err)
	}
	log.Infof("%s %s %d", r.Method, r.URL, status)
}
