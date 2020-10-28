package gubernator_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster/v3/clock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

func spawnDaemon(t *testing.T, conf gubernator.DaemonConfig) *gubernator.Daemon {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), clock.Second*10)
	d, err := gubernator.SpawnDaemon(ctx, conf)
	cancel()
	require.NoError(t, err)
	d.SetPeers([]gubernator.PeerInfo{{GRPCAddress: conf.GRPCListenAddress, IsOwner: true}})
	return d
}

func makeRequest(t *testing.T, conf gubernator.DaemonConfig) error {
	t.Helper()

	client, err := gubernator.DialV1Server(conf.GRPCListenAddress, conf.TLS.ClientTLS)
	require.NoError(t, err)

	resp, err := client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
		Requests: []*gubernator.RateLimitReq{
			{
				Name:      "test_tls",
				UniqueKey: "account:995",
				Algorithm: gubernator.Algorithm_TOKEN_BUCKET,
				Duration:  gubernator.Second * 30,
				Limit:     100,
				Hits:      1,
			},
		},
	})

	if err != nil {
		return err
	}
	rl := resp.Responses[0]
	assert.Equal(t, "", rl.Error)
	return nil
}

func TestSetupTLS(t *testing.T) {
	tests := []struct {
		tls  *gubernator.TLSConfig
		name string
	}{
		{
			name: "user provided certificates",
			tls: &gubernator.TLSConfig{
				CaFile:   "certs/ca.pem",
				CertFile: "certs/gubernator.pem",
				KeyFile:  "certs/gubernator.key",
			},
		},
		{
			name: "auto tls",
			tls: &gubernator.TLSConfig{
				AutoTLS: true,
			},
		},
		{
			name: "generate server certs with user provided ca",
			tls: &gubernator.TLSConfig{
				CaFile:    "certs/ca.pem",
				CaKeyFile: "certs/ca.key",
				AutoTLS:   true,
			},
		},
		{
			name: "client auth enabled",
			tls: &gubernator.TLSConfig{
				CaFile:     "certs/ca.pem",
				CaKeyFile:  "certs/ca.key",
				AutoTLS:    true,
				ClientAuth: tls.RequireAndVerifyClientCert,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := gubernator.DaemonConfig{
				GRPCListenAddress: "127.0.0.1:9695",
				HTTPListenAddress: "127.0.0.1:9685",
				TLS:               tt.tls,
			}

			d := spawnDaemon(t, conf)

			client, err := gubernator.DialV1Server(conf.GRPCListenAddress, tt.tls.ServerTLS)
			require.NoError(t, err)

			resp, err := client.GetRateLimits(context.Background(), &gubernator.GetRateLimitsReq{
				Requests: []*gubernator.RateLimitReq{
					{
						Name:      "test_tls",
						UniqueKey: "account:995",
						Algorithm: gubernator.Algorithm_TOKEN_BUCKET,
						Duration:  gubernator.Second * 30,
						Limit:     100,
						Hits:      1,
					},
				},
			})
			require.NoError(t, err)

			rl := resp.Responses[0]
			assert.Equal(t, "", rl.Error)
			assert.Equal(t, gubernator.Status_UNDER_LIMIT, rl.Status)
			assert.Equal(t, int64(99), rl.Remaining)
			d.Close()
		})
	}
}

func TestSetupTLSSkipVerify(t *testing.T) {
	conf := gubernator.DaemonConfig{
		GRPCListenAddress: "127.0.0.1:9695",
		HTTPListenAddress: "127.0.0.1:9685",
		TLS: &gubernator.TLSConfig{
			CaFile:   "certs/ca.pem",
			CertFile: "certs/gubernator.pem",
			KeyFile:  "certs/gubernator.key",
		},
	}

	d := spawnDaemon(t, conf)
	defer d.Close()

	tls := &gubernator.TLSConfig{
		AutoTLS:            true,
		InsecureSkipVerify: true,
	}

	err := gubernator.SetupTLS(tls)
	require.NoError(t, err)
	conf.TLS = tls

	err = makeRequest(t, conf)
	require.NoError(t, err)
}

func TestSetupTLSClientAuth(t *testing.T) {
	serverTLS := gubernator.TLSConfig{
		CaFile:           "certs/ca.pem",
		CertFile:         "certs/gubernator.pem",
		KeyFile:          "certs/gubernator.key",
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientAuthCaFile: "certs/client-auth-ca.pem",
	}

	conf := gubernator.DaemonConfig{
		GRPCListenAddress: "127.0.0.1:9695",
		HTTPListenAddress: "127.0.0.1:9685",
		TLS:               &serverTLS,
	}

	d := spawnDaemon(t, conf)
	defer d.Close()

	// Given generated client certs
	tls := &gubernator.TLSConfig{
		AutoTLS:            true,
		InsecureSkipVerify: true,
	}

	err := gubernator.SetupTLS(tls)
	require.NoError(t, err)
	conf.TLS = tls

	// Should not be allowed without a cert signed by the client CA
	err = makeRequest(t, conf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "code = Unavailable desc")

	// Given the client auth certs
	tls = &gubernator.TLSConfig{
		CertFile:           "certs/client-auth.pem",
		KeyFile:            "certs/client-auth.key",
		InsecureSkipVerify: true,
	}

	err = gubernator.SetupTLS(tls)
	require.NoError(t, err)
	conf.TLS = tls

	// Should be allowed to connect and make requests
	err = makeRequest(t, conf)
	require.NoError(t, err)
}

func TestTLSClusterWithClientAuthentication(t *testing.T) {
	serverTLS := gubernator.TLSConfig{
		CaFile:     "certs/ca.pem",
		CertFile:   "certs/gubernator.pem",
		KeyFile:    "certs/gubernator.key",
		ClientAuth: tls.RequireAndVerifyClientCert,
	}

	d1 := spawnDaemon(t, gubernator.DaemonConfig{
		GRPCListenAddress: "127.0.0.1:9695",
		HTTPListenAddress: "127.0.0.1:9685",
		TLS:               &serverTLS,
	})
	defer d1.Close()

	d2 := spawnDaemon(t, gubernator.DaemonConfig{
		GRPCListenAddress: "127.0.0.1:9696",
		HTTPListenAddress: "127.0.0.1:9686",
		TLS:               &serverTLS,
	})
	defer d2.Close()

	peers := []gubernator.PeerInfo{
		{
			GRPCAddress: d1.GRPCListener.Addr().String(),
			HTTPAddress: d1.HTTPListener.Addr().String(),
		},
		{
			GRPCAddress: d2.GRPCListener.Addr().String(),
			HTTPAddress: d2.HTTPListener.Addr().String(),
		},
	}
	d1.SetPeers(peers)
	d2.SetPeers(peers)

	// Should result in a remote call to d2
	err := makeRequest(t, d1.Config())
	require.NoError(t, err)

	config := d2.Config()
	client := &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: config.ClientTLS(),
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://%s/metrics", config.HTTPListenAddress))
	require.NoError(t, err)
	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	// Should have called GetPeerRateLimits on d2
	assert.Contains(t, string(b), `{method="/pb.gubernator.PeersV1/GetPeerRateLimits"} 1`)
}
