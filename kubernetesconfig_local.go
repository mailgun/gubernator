//go:build local
// +build local

package gubernator

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func config() (*rest.Config, error) {
	home := homeDir()
	if home == "" {
		return nil, errors.New("could not find kube config file. cannot load config")

	}
	cfg := filepath.Join(home, ".kube", "config")
	r, err := clientcmd.BuildConfigFromFlags("", cfg)
	if nil != err {
		return nil, err
	}

	return r, nil
}

func RestConfig() (*rest.Config, error) {
	return config()
}
