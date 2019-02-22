package testutils

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type HTTPClient struct {
	baseURL string
	client  http.Client
}

func (c *HTTPClient) URL() string {
	return c.baseURL
}

type requestResponse struct {
	rq     *http.Request
	rs     *http.Response
	cancel context.CancelFunc
}

func (rr *requestResponse) close() {
	if rr.cancel != nil {
		rr.cancel()
	}
}

type option func(rr *requestResponse) error

func WithFormURLEncoded(vals url.Values) option {
	return func(rr *requestResponse) error {
		if rr.rs != nil {
			return nil
		}
		rr.rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr.rq.Body = ioutil.NopCloser(strings.NewReader(vals.Encode()))
		return nil
	}
}

func WithJSON(data interface{}) option {
	return func(rr *requestResponse) error {
		if rr.rs != nil {
			return nil
		}
		rr.rq.Header.Set("Content-Type", "application/json")
		switch data := data.(type) {
		case string:
			rr.rq.Body = ioutil.NopCloser(strings.NewReader(data))
		default:
			encoded, err := json.Marshal(data)
			if err != nil {
				return errors.Wrap(err, "while marshalling JSON request")
			}
			rr.rq.Body = ioutil.NopCloser(bytes.NewReader(encoded))
		}
		return nil
	}
}

func WithHeader(key, value string) option {
	return func(rr *requestResponse) error {
		if rr.rs != nil {
			return nil
		}
		rr.rq.Header.Set(key, value)
		return nil
	}
}

func WithTimeout(timeout time.Duration) option {
	return func(rr *requestResponse) error {
		if rr.rs != nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		rr.rq = rr.rq.WithContext(ctx)
		rr.cancel = cancel
		return nil
	}
}

func WithContext(ctx context.Context) option {
	return func(rr *requestResponse) error {
		if rr.rs != nil {
			return nil
		}
		rr.rq = rr.rq.WithContext(ctx)
		return nil
	}
}

func ParseJSONResponseTo(body interface{}) option {
	return func(rr *requestResponse) error {
		if rr.rs == nil {
			return nil
		}
		defer rr.rs.Body.Close()
		err := json.NewDecoder(rr.rs.Body).Decode(&body)
		if err != nil {
			return errors.Wrap(err, "while unmarshalling JSON response")
		}
		return nil
	}
}

func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{baseURL: baseURL}
}

func (c *HTTPClient) Get(path string, opts ...option) (*http.Response, error) {
	return c.Do("GET", path, opts...)
}

func (c *HTTPClient) Put(path string, opts ...option) (*http.Response, error) {
	return c.Do("PUT", path, opts...)
}

func (c *HTTPClient) Delete(path string, opts ...option) (*http.Response, error) {
	return c.Do("DELETE", path, opts...)
}

func (c *HTTPClient) Post(path string, opts ...option) (*http.Response, error) {
	return c.Do("POST", path, opts...)
}

func (c *HTTPClient) Do(method, path string, opts ...option) (*http.Response, error) {
	var rr requestResponse
	defer rr.close()
	var err error

	if rr.rq, err = http.NewRequest(method, c.url(path), nil); err != nil {
		return nil, errors.Wrap(err, "while preparing HTTP request")
	}
	if err := applyOptions(&rr, opts...); err != nil {
		return nil, errors.Wrap(err, "while applying request options")
	}

	if rr.rs, err = c.client.Do(rr.rq); err != nil {
		return rr.rs, errors.Wrap(err, "while executing HTTP request")
	}
	if err := applyOptions(&rr, opts...); err != nil {
		return nil, errors.Wrap(err, "while applying response options")
	}
	return rr.rs, nil
}

func (c *HTTPClient) url(path string) string {
	return c.baseURL + path
}

func applyOptions(rr *requestResponse, opts ...option) error {
	for _, opt := range opts {
		if err := opt(rr); err != nil {
			return errors.Wrap(err, "while applying opt")
		}
	}
	return nil
}
