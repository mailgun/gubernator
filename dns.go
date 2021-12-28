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
	"context"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/mailgun/holster/v4/setter"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Adapted from TimothyYe/godns
// DNSResolver represents a dns resolver
type DNSResolver struct {
	Servers []string
	random  *rand.Rand
}

// NewFromResolvConf initializes DnsResolver from resolv.conf like file.
func NewFromResolvConf(path string) (*DNSResolver, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &DNSResolver{}, errors.New("no such file or directory: " + path)
	}

	config, err := dns.ClientConfigFromFile(path)
	if err != nil {
		return &DNSResolver{}, err
	}

	var servers []string
	if len(config.Servers) == 0 {
		return &DNSResolver{}, errors.New("no servers in config")
	}
	for _, ipAddress := range config.Servers {
		servers = append(servers, net.JoinHostPort(ipAddress, "53"))
	}
	return &DNSResolver{servers, rand.New(rand.NewSource(time.Now().UnixNano()))}, nil
}

func (r *DNSResolver) lookupHost(host string, dnsType uint16, delay uint32) ([]net.IP, uint32, error) {
	m1 := new(dns.Msg)
	m1.Id = dns.Id()
	m1.RecursionDesired = true
	m1.Question = make([]dns.Question, 1)

	switch dnsType {
	case dns.TypeA:
		m1.Question[0] = dns.Question{Name: dns.Fqdn(host), Qtype: dns.TypeA, Qclass: dns.ClassINET}
	case dns.TypeAAAA:
		m1.Question[0] = dns.Question{Name: dns.Fqdn(host), Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}
	}

	in, err := dns.Exchange(m1, r.Servers[r.random.Intn(len(r.Servers))])

	var result []net.IP

	if err != nil {
		return result, 0, err
	}

	if in.Rcode != dns.RcodeSuccess {
		return result, 0, errors.New(dns.RcodeToString[in.Rcode])
	}

	if dnsType == dns.TypeA {
		if len(in.Answer) > 0 {
			for _, record := range in.Answer {
				if t, ok := record.(*dns.A); ok {
					result = append(result, t.A)
					delay = min(delay, record.Header().Ttl)
				}
			}
		} else {
			return result, 0, errors.New("not useful")
		}
	}

	if dnsType == dns.TypeAAAA {
		if len(in.Answer) > 0 {
			for _, record := range in.Answer {
				if t, ok := record.(*dns.AAAA); ok {
					result = append(result, t.AAAA)
					delay = min(delay, record.Header().Ttl)
				}
			}
		} else {
			return result, 0, errors.New("not useful")
		}
	}

	return result, delay, nil
}

type DNSPoolConfig struct {
	// (Required) The FQDN that should resolve to gubernator instance ip addresses
	FQDN string

	// (Required) Filesystem path to "/etc/resolv.conf", override for testing
	ResolvConf string

	// (Required) Own GRPC address
	OwnAddress string

	// (Required) Called when the list of gubernators in the pool updates
	OnUpdate UpdateFunc

	Logger FieldLogger
}

type DNSPool struct {
	log    FieldLogger
	conf   DNSPoolConfig
	ctx    context.Context
	cancel context.CancelFunc
}

func NewDNSPool(conf DNSPoolConfig) (*DNSPool, error) {
	setter.SetDefault(&conf.Logger, logrus.WithField("category", "gubernator"))

	if conf.OwnAddress == "" {
		return nil, errors.New("Advertise.GRPCAddress is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool := &DNSPool{
		log:    conf.Logger,
		conf:   conf,
		ctx:    ctx,
		cancel: cancel,
	}
	go pool.task()
	return pool, nil
}

func peer(ip string, self string, ipv6 bool) PeerInfo {

	if ipv6 {
		ip = "[" + ip + "]"
	}
	grpc := ip + ":81"
	return PeerInfo{
		ClusterName: "",
		HTTPAddress: ip + ":80",
		GRPCAddress: grpc,
		IsOwner:     grpc == self,
	}

}

func min(a uint32, b uint32) uint32 {
	if a < b {
		return a
	} else {
		return b
	}
}

func (p *DNSPool) task() {
	for {
		var delay uint32 = 300
		resolver, err := NewFromResolvConf(p.conf.ResolvConf)
		if err != nil {
			p.log.Warn("No resolver: ", err)

		} else {
			ipv4, delay4, err4 := resolver.lookupHost(p.conf.FQDN, dns.TypeA, delay)
			ipv6, delay6, err6 := resolver.lookupHost(p.conf.FQDN, dns.TypeAAAA, delay)
			var update []PeerInfo
			if err4 == nil {
				delay = min(delay, delay4)
				for _, ip := range ipv4 {
					update = append(update, peer(ip.String(), p.conf.OwnAddress, false))
				}
			}
			if err6 == nil {
				delay = min(delay, delay6)
				for _, ip := range ipv6 {
					update = append(update, peer(ip.String(), p.conf.OwnAddress, false))
				}
			}
			if len(update) > 0 {
				p.conf.OnUpdate(update)
			} else {
				p.log.Error("Looking up peers: ", err4, err6)
			}
		}
		p.log.Debug("DNS poll delay: ", delay)
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(time.Duration(delay) * time.Second):
		}
	}
}

func (p *DNSPool) Close() {
	p.cancel()
}
