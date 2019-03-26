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

func (m *Request) HashKey() string {
	return m.Name + "_" + m.UniqueKey
}

// Create a new connection to the server
func NewV1Client(server string) (RateLimitServiceV1Client, error) {
	if len(server) == 0 {
		return nil, errors.New("server is empty; must provide a server")
	}

	conn, err := grpc.Dial(server, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial peer %s", server)
	}

	return NewRateLimitServiceV1Client(conn), nil
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
	rand.Shuffle(len(peers), func(i, j int) {
		peers[i], peers[j] = peers[j], peers[i]
	})
	return peers[0]
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
