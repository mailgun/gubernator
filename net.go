package gubernator

import (
	"net"
	"os"

	"github.com/mailgun/holster/v3/slice"
	"github.com/pkg/errors"
)

// If the passed address is "0.0.0.0" or "::" attempts to discover the actual ip address of the host
func ResolveHostIP(addr string) (string, error) {
	if slice.ContainsString(addr, []string{"0.0.0.0", "::", "0:0:0:0:0:0:0:0"}, nil) {
		// Use the hostname as the advertise address as it's most likely to be the external interface
		domainName, err := os.Hostname()
		if err != nil {
			addr, err = discoverIP()
			if err != nil {
				return "", errors.Wrapf(err, "while discovering ip for '%s'", addr)
			}
			return addr, nil
		}
		addrs, err := net.LookupHost(domainName)
		if err != nil {
			return "", errors.Wrapf(err, "while preforming host lookup for '%s'", domainName)
		}
		if len(addrs) == 0 {
			return "", errors.Wrapf(err, "net.LookupHost() returned no addresses for '%s'", domainName)
		}
		return addrs[0], nil
	}
	return addr, nil
}

type netInfo struct {
	IPAddresses []string
	DNSNames    []string
}

// Attempts to discover all the external ips and dns names associated with the current host.
func discoverNetwork() (netInfo, error) {
	var result netInfo

	var err error
	result.IPAddresses, err = discoverNetworkAddresses()
	if err != nil {
		return result, err
	}

	for _, ip := range result.IPAddresses {
		records, _ := net.LookupAddr(ip)
		result.DNSNames = append(result.DNSNames, records...)
	}
	return result, nil
}

// Returns the first external ip address it finds
func discoverIP() (string, error) {
	addrs, err := discoverNetworkAddresses()
	if err != nil {
		return "", errors.Wrap(err, "while detecting external ip address")
	}
	if len(addrs) == 0 {
		return "", errors.New("No external ip address found; please set `GUBER_ADVERTISE_ADDRESS`")
	}
	return addrs[0], err
}

// Returns a list of net addresses by inspecting the network interfaces on the current host.
func discoverNetworkAddresses() ([]string, error) {
	var results []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue // not an ipv4 address
			}
			results = append(results, ip.String())
		}
	}
	return results, nil
}
