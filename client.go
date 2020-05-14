/*
Copyright 2018-2019 Mailgun Technologies Inc

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
	"math/rand"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

const (
	Millisecond = 1
	Second      = 1000 * Millisecond
	Minute      = 60 * Second
)

func (m *RateLimitReq) HashKey() string {
	return m.Name + "_" + m.UniqueKey
}

// Create a new connection to the server
func DialV1Server(server string) (V1Client, error) {
	if len(server) == 0 {
		return nil, errors.New("server is empty; must provide a server")
	}

	conn, err := grpc.Dial(server, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial peer %s", server)
	}

	return NewV1Client(conn), nil
}

// Convert a time.Duration to a unix millisecond timestamp
func ToTimeStamp(duration time.Duration) int64 {
	return int64(duration / time.Millisecond)
}

// Convert a unix millisecond timestamp to a time.Duration
func FromTimeStamp(ts int64) time.Duration {
	return time.Now().Sub(FromUnixMilliseconds(ts))
}

func FromUnixMilliseconds(ts int64) time.Time {
	return time.Unix(0, ts*int64(time.Millisecond))
}

// Given a list of peers, return a random peer
func RandomPeer(peers []string) string {
	return peers[rand.Intn(len(peers))]
}

// Return a random alpha string of 'n' length
func RandomString(n int) string {
	const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = alphanum[b%byte(len(alphanum))]
	}
	return string(bytes)
}
