// +build !local

package gubernator

import (
	"k8s.io/client-go/rest"
)

func RestConfig() (*rest.Config, error) {
	return rest.InClusterConfig()
}
