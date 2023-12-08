/*
Copyright 2018-2022 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"time"

	"github.com/duh-rpc/duh-go"
	v1 "github.com/duh-rpc/duh-go/proto/v1"
	"github.com/mailgun/holster/v4/clock"
	"github.com/mailgun/holster/v4/setter"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/proto"
)

const (
	Millisecond = 1
	Second      = 1000 * Millisecond
	Minute      = 60 * Second
)

type Client interface {
	CheckRateLimits(context.Context, *CheckRateLimitsRequest, *CheckRateLimitsResponse) error
	HealthCheck(context.Context, *HealthCheckResponse) error
}

func (m *RateLimitRequest) HashKey() string {
	return m.Name + "_" + m.UniqueKey
}

type ClientOptions struct {
	// Users can provide their own http client with TLS config if needed
	Client *http.Client
	// The address of endpoint in the format `<scheme>://<host>:<port>`
	Endpoint string
}

type client struct {
	*duh.Client
	prop propagation.TraceContext
	opts ClientOptions
}

// NewClient creates a new instance of the Gubernator user client
func NewClient(opts ClientOptions) (Client, error) {
	setter.SetDefault(&opts.Client, DefaultHTTPClient)

	if len(opts.Endpoint) == 0 {
		return nil, errors.New("opts.Endpoint is empty; must provide an address")
	}

	return &client{
		Client: &duh.Client{
			Client: opts.Client,
		},
		opts: opts,
	}, nil
}

func NewPeerClient(opts ClientOptions) PeerClient {
	return &client{
		Client: &duh.Client{
			Client: opts.Client,
		},
		opts: opts,
	}
}

func (c *client) CheckRateLimits(ctx context.Context, req *CheckRateLimitsRequest, resp *CheckRateLimitsResponse) error {
	payload, err := proto.Marshal(req)
	if err != nil {
		return duh.NewClientError(fmt.Errorf("while marshaling request payload: %w", err), nil)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s%s", c.opts.Endpoint, RPCRateLimitCheck), bytes.NewReader(payload))
	if err != nil {
		return duh.NewClientError(err, nil)
	}

	r.Header.Set("Content-Type", duh.ContentTypeProtoBuf)
	return c.Do(r, resp)
}

func (c *client) HealthCheck(ctx context.Context, resp *HealthCheckResponse) error {
	payload, err := proto.Marshal(&HealthCheckRequest{})
	if err != nil {
		return duh.NewClientError(fmt.Errorf("while marshaling request payload: %w", err), nil)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s%s", c.opts.Endpoint, RPCHealthCheck), bytes.NewReader(payload))
	if err != nil {
		return duh.NewClientError(err, nil)
	}

	r.Header.Set("Content-Type", duh.ContentTypeProtoBuf)
	return c.Do(r, resp)
}

func (c *client) Forward(ctx context.Context, req *ForwardRequest, resp *ForwardResponse) error {
	payload, err := proto.Marshal(req)
	if err != nil {
		return duh.NewClientError(fmt.Errorf("while marshaling request payload: %w", err), nil)
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s%s", c.opts.Endpoint, RPCPeerForward), bytes.NewReader(payload))
	if err != nil {
		return duh.NewClientError(err, nil)
	}

	c.prop.Inject(ctx, propagation.HeaderCarrier(r.Header))
	r.Header.Set("Content-Type", duh.ContentTypeProtoBuf)
	return c.Do(r, resp)
}

func (c *client) Update(ctx context.Context, req *UpdateRequest) error {
	payload, err := proto.Marshal(req)
	if err != nil {
		return duh.NewClientError(fmt.Errorf("while marshaling request payload: %w", err), nil)
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s%s", c.opts.Endpoint, RPCPeerUpdate), bytes.NewReader(payload))
	if err != nil {
		return duh.NewClientError(err, nil)
	}

	r.Header.Set("Content-Type", duh.ContentTypeProtoBuf)
	return c.Do(r, &v1.Reply{})
}

var (
	// DefaultHTTPClient enables H2C (HTTP/2 over Cleartext)
	DefaultHTTPClient = &http.Client{
		Transport: &http2.Transport{
			// So http2.Transport doesn't complain the URL scheme isn't 'https'
			AllowHTTP: true,
			// Pretend we are dialing a TLS endpoint. (Note, we ignore the passed tls.Config)
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
)

// WithNoTLS returns ClientOptions suitable for use with NON-TLS clients with H2C enabled.
func WithNoTLS(address string) ClientOptions {
	return ClientOptions{
		Endpoint: fmt.Sprintf("http://%s", address),
		Client:   DefaultHTTPClient,
	}
}

// WithTLS returns ClientOptions suitable for use with NON-TLS clients with H2C enabled.
func WithTLS(tls *tls.Config, address string) ClientOptions {
	return ClientOptions{
		Endpoint: fmt.Sprintf("https://%s", address),
		Client: &http.Client{
			Transport: &http2.Transport{
				TLSClientConfig: tls,
			},
		},
	}
}

// WithDaemonConfig returns ClientOptions suitable for use by the Daemon
func WithDaemonConfig(conf DaemonConfig, address string) ClientOptions {
	if conf.ClientTLS() == nil {
		return WithNoTLS(address)
	}
	return WithTLS(conf.ClientTLS(), address)
}

// ToTimeStamp is a convenience function to convert a time.Duration
// to a unix millisecond timestamp. Useful when working with gubernator
// request and response duration and reset_time fields.
func ToTimeStamp(duration time.Duration) int64 {
	return int64(duration / time.Millisecond)
}

// FromTimeStamp is a convenience function to convert a unix millisecond
// timestamp to a time.Duration. Useful when working with gubernator
// request and response duration and reset_time fields.
func FromTimeStamp(ts int64) time.Duration {
	return clock.Now().Sub(FromUnixMilliseconds(ts))
}

// FromUnixMilliseconds is a convenience function to convert a unix
// millisecond timestamp to a time.Time. Useful when working with gubernator
// request and response duration and reset_time fields.
func FromUnixMilliseconds(ts int64) time.Time {
	return clock.Unix(0, ts*int64(clock.Millisecond))
}

// RandomPeer returns a random peer from the list of peers provided
func RandomPeer(peers []PeerInfo) PeerInfo {
	rand.Shuffle(len(peers), func(i, j int) {
		peers[i], peers[j] = peers[j], peers[i]
	})
	return peers[0]
}

// RandomString returns a random alpha string of 'n' length
func RandomString(n int) string {
	const alphanumeric = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	_, _ = crand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanumeric[b%byte(len(alphanumeric))]
	}
	return string(bytes)
}
