package vulcand

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mailgun/service/vulcand"
	"github.com/pkg/errors"
)

const (
	defaultFailoverPredicate = "(IsNetworkError() || ResponseCode() == 503) && Attempts() <= 2"
	defaultPassHostHeader    = true
)

type frontendSpec struct {
	ID          string
	Host        string
	Path        string
	URLPath     string
	Method      string
	AppName     string
	Options     frontendOptions
	Middlewares []vulcand.Middleware
}

type frontendOptions struct {
	FailoverPredicate string `json:"FailoverPredicate"`
	PassHostHeader    bool   `json:"PassHostHeader,omitempty"`
}

func (fo frontendOptions) spec() string {
	return fmt.Sprintf(`{"FailoverPredicate":"%s","PassHostHeader":%t}`, fo.FailoverPredicate, fo.PassHostHeader)
}

func newFrontendSpec(appName, host, path, method string, middlewares []vulcand.Middleware) *frontendSpec {
	path = normalizePath(path)
	method = strings.ToUpper(method)
	return &frontendSpec{
		ID:      makeLocationID(method, path),
		Host:    strings.ToLower(host),
		Method:  method,
		URLPath: path,
		Path:    makeLocationPath(method, path),
		AppName: appName,
		Options: frontendOptions{
			FailoverPredicate: defaultFailoverPredicate,
			PassHostHeader:    defaultPassHostHeader,
		},
		Middlewares: middlewares,
	}
}

func (fes *frontendSpec) spec() string {
	return fmt.Sprintf(`{"Type":"http","BackendId":"%s","Route":%s,"Settings":%s}`,
		fes.AppName, strconv.Quote(fes.route()), fes.Options.spec())
}

func (fes *frontendSpec) hash() (string, error) {
	d := sha1.New()
	fesJSON, err := json.Marshal(fes)
	if err != nil {
		return "", errors.Wrapf(err, "failed to JSON, %v", fes)
	}
	d.Write(fesJSON)
	return fmt.Sprintf("%x", d.Sum(nil)), nil
}

func (fes *frontendSpec) route() string {
	return fmt.Sprintf(`Host("%s") && Method("%s") && Path("%s")`, fes.Host, fes.Method, fes.URLPath)
}

func makeLocationID(method string, path string) string {
	return strings.ToLower(strings.Replace(fmt.Sprintf("%v%v", method, path), "/", ".", -1))
}

func makeLocationPath(method string, path string) string {
	return fmt.Sprintf(`TrieRoute("%v", "%v")`, method, path)
}

// normalizePath converts a router path to the format understood by Vulcand.
//
// It does two things:
//  - Strips regular expression parts of path variables, i.e. turns "/v2/{id:[0-9]+}" into "/v2/{id}".
//  - Replaces curly brackets with angle brackets, i.e. turns "/v2/{id}" into "/v2/<id>".
func normalizePath(path string) string {
	path = regexp.MustCompile("(:[^}]+)").ReplaceAllString(path, "")
	return strings.Replace(strings.Replace(path, "{", "<", -1), "}", ">", -1)
}
