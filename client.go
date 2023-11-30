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
	crand "crypto/rand"
	"crypto/tls"
	"math/rand"
	"time"

	"github.com/mailgun/holster/v4/clock"
	"github.com/pkg/errors"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	Millisecond = 1
	Second      = 1000 * Millisecond
	Minute      = 60 * Second
)

func (m *RateLimitReq) HashKey() string {
	return m.Name + "_" + m.UniqueKey
}

// DialV1Server is a convenience function for dialing gubernator instances
func DialV1Server(server string, tls *tls.Config) (V1Client, error) {
	if len(server) == 0 {
		return nil, errors.New("server is empty; must provide a server")
	}

	// Setup OpenTelemetry interceptor to propagate spans.
	opts := []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
	if tls != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tls)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(server, opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial server %s", server)
	}

	return NewV1Client(conn), nil
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
