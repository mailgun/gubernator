package vulcand

import (
	"errors"
	"fmt"

	"github.com/mailgun/iptools"
)

type backendSpec struct {
	AppName string
	ID      string
	URL     string
}

func newBackendSpec(appName, hostname, ip string, port int) (*backendSpec, error) {
	id := fmt.Sprintf("%v_%v", hostname, port)
	url, err := makeEndpointURL(ip, port)
	if err != nil {
		return nil, fmt.Errorf("failed to make endpoint URL: %v", err)
	}
	return &backendSpec{
		AppName: appName,
		ID:      id,
		URL:     url,
	}, nil
}

func (bes *backendSpec) typeSpec() string {
	return `{"Type":"http"}`
}

func (bes *backendSpec) serverSpec() string {
	return fmt.Sprintf(`{"URL":"%s"}`, bes.URL)
}

// makeEndpointURL constructs a URL by determining the private IP address of
// the host.
func makeEndpointURL(listenIP string, listenPort int) (string, error) {
	if listenIP != "0.0.0.0" {
		return fmt.Sprintf("http://%v:%v", listenIP, listenPort), nil
	}
	privateIPs, err := iptools.GetPrivateHostIPs()
	if err != nil {
		return "", fmt.Errorf("failed to obtain host's private IPs: %v", err)
	}
	if len(privateIPs) == 0 {
		return "", errors.New("no host's private IPs are found")
	}
	return fmt.Sprintf("http://%v:%v", privateIPs[0], listenPort), nil
}
