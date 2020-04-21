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

package cluster

import (
	"testing"

	"github.com/mailgun/gubernator"
	"github.com/stretchr/testify/assert"
)

func Test_instance_Peers(t *testing.T) {
	tests := []struct {
		name     string
		instance *instance
		peers    []gubernator.PeerInfo
		want     []gubernator.PeerInfo
	}{
		{
			name:     "Happy path",
			instance: &instance{Address: "mailgun.com"},
			peers:    []gubernator.PeerInfo{gubernator.PeerInfo{Address: "mailgun.com"}},
			want: []gubernator.PeerInfo{
				{Address: "mailgun.com", IsOwner: true},
			},
		},
		{
			name:     "Get multy peers",
			instance: &instance{Address: "mailgun.com"},
			peers:    []gubernator.PeerInfo{gubernator.PeerInfo{Address: "localhost:11111"}, gubernator.PeerInfo{Address: "mailgun.com"}},
			want: []gubernator.PeerInfo{
				{Address: "localhost:11111"},
				{Address: "mailgun.com", IsOwner: true},
			},
		},
		{
			name:     "No Peers",
			instance: &instance{Address: "www.mailgun.com:11111"},
			peers:    []gubernator.PeerInfo{},
			want:     []gubernator.PeerInfo(nil),
		},
		{
			name:     "Peers are nil",
			instance: &instance{Address: "www.mailgun.com:11111"},
			peers:    nil,
			want:     []gubernator.PeerInfo(nil),
		},
		{
			name:     "Owner does not exist",
			instance: &instance{Address: "mailgun.com"},
			peers:    []gubernator.PeerInfo{gubernator.PeerInfo{Address: "localhost:11111"}},
			want: []gubernator.PeerInfo{
				{Address: "localhost:11111"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peers = tt.peers

			got := tt.instance.Peers()

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPeer(t *testing.T) {
	tests := []struct {
		name  string
		peers []gubernator.PeerInfo
		oneOf map[string]bool
	}{
		{
			name:  "Happy path",
			peers: []gubernator.PeerInfo{gubernator.PeerInfo{Address: "mailgun.com"}},
		},
		{
			name:  "Get one peer from multiple peers",
			peers: []gubernator.PeerInfo{gubernator.PeerInfo{Address: "mailgun.com"}, gubernator.PeerInfo{Address: "localhost"}, gubernator.PeerInfo{Address: "test.com"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peers = tt.peers
			got := GetRandomPeer()

			assert.Contains(t, peers, got)
		})
	}
}

func TestPeerAt(t *testing.T) {
	peers = []gubernator.PeerInfo{gubernator.PeerInfo{Address: "mailgun.com"}}

	got := PeerAt(0)
	want := gubernator.PeerInfo{Address: "mailgun.com"}

	assert.Equal(t, want, got)
}

func TestInstanceAt(t *testing.T) {
	tests := []struct {
		name      string
		instances []*instance
		index     int
		want      *instance
	}{
		{
			name: "Get first instance",
			instances: []*instance{
				{Address: "test.com"},
				{Address: "localhost"},
			},
			index: 0,
			want:  &instance{Address: "test.com"},
		},
		{
			name: "Get second instance",
			instances: []*instance{
				{Address: "mailgun.com"},
				{Address: "google.com"},
			},
			index: 1,
			want:  &instance{Address: "google.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instances = tt.instances

			got := InstanceAt(tt.index)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStartMultipleInstances(t *testing.T) {
	// to be tests independent we need to reset the global variables
	instances = nil
	peers = nil

	err := Start(2)
	assert.Nil(t, err)

	assert.Equal(t, 2, len(instances))
	assert.Equal(t, 2, len(peers))
}

func TestStartZeroInstances(t *testing.T) {
	// to be tests independent we need to reset the global variables
	instances = nil
	peers = nil

	err := Start(0)
	assert.Nil(t, err)

	assert.Equal(t, 0, len(instances))
	assert.Equal(t, 0, len(peers))
}

func TestStartMultipleInstancesWithAddresses(t *testing.T) {
	// to be tests independent we need to reset the global variables
	instances = nil
	peers = nil

	addresses := []string{"localhost:11111", "localhost:22222"}
	err := StartWith(addresses)
	assert.Nil(t, err)

	wantPeers := []gubernator.PeerInfo{gubernator.PeerInfo{Address: "127.0.0.1:11111"}, gubernator.PeerInfo{Address: "127.0.0.1:22222"}}
	wantInstances := []*instance{
		{Address: "127.0.0.1:11111"},
		{Address: "127.0.0.1:22222"},
	}

	assert.Equal(t, wantPeers, peers)
	assert.Equal(t, 2, len(instances))
	assert.Equal(t, wantInstances[0].Address, instances[0].Address)
	assert.Equal(t, wantInstances[1].Address, instances[1].Address)
}

func TestStartWithAddressesFail(t *testing.T) {
	// to be tests independent we need to reset the global variables
	instances = nil
	peers = nil

	addresses := []string{"11111"}
	err := StartWith(addresses)
	assert.NotNil(t, err)
	assert.Nil(t, peers)
	assert.Nil(t, instances)
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
