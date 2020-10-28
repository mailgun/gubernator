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

func discoverIP() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
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
			return "", err
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
			return ip.String(), nil
		}
	}
	return "", errors.New("Unable to detect external ip address; please set `GUBER_ADVERTISE_ADDRESS`?")
}
