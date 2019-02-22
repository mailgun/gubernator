package testutils

import (
	"net/http/httptest"

	"github.com/gorilla/mux"
	"github.com/mailgun/service/httpapi"
	"github.com/pkg/errors"
)

type TestService struct {
	specs  []httpapi.Spec
	srv    *httptest.Server
	client *HTTPClient
}

func (ts *TestService) AddHandler(spec httpapi.Spec) error {
	ts.specs = append(ts.specs, spec)
	return nil
}

func (ts *TestService) Start() (*HTTPClient, error) {
	router := mux.NewRouter()
	for _, spec := range ts.specs {
		if err := httpapi.AddHandler(router, spec); err != nil {
			return nil, errors.Wrap(err, "while adding spec to router")
		}
	}
	ts.srv = httptest.NewServer(router)
	ts.client = NewHTTPClient(ts.srv.URL)
	return ts.client, nil
}

func (ts *TestService) Client() *HTTPClient {
	return ts.client
}

func (ts *TestService) URL() string {
	return ts.srv.URL
}

func (ts *TestService) Stop() {
	ts.srv.Close()
}
