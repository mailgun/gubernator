package gubernator

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestParsesGrpcAddress(t *testing.T) {
	os.Clearenv()
	s := `
# a comment
GUBER_GRPC_ADDRESS=10.10.10.10:9000`
	daemonConfig, err := SetupDaemonConfig(logrus.StandardLogger(), strings.NewReader(s))
	require.NoError(t, err)
	require.Equal(t, "10.10.10.10:9000", daemonConfig.GRPCListenAddress)
	require.NotEmpty(t, daemonConfig.InstanceID)
}

func TestDefaultGrpcAddress(t *testing.T) {
	os.Clearenv()
	s := `
# a comment`
	daemonConfig, err := SetupDaemonConfig(logrus.StandardLogger(), strings.NewReader(s))
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%s:81", LocalHost()), daemonConfig.GRPCListenAddress)
	require.NotEmpty(t, daemonConfig.InstanceID)
}

func TestDefaultInstanceId(t *testing.T) {
	os.Clearenv()
	s := ``
	daemonConfig, err := SetupDaemonConfig(logrus.StandardLogger(), strings.NewReader(s))
	require.NoError(t, err)
	require.NotEmpty(t, daemonConfig.InstanceID)
}

func TestNoConfigFile(t *testing.T) {
	os.Clearenv()
	daemonConfig, err := SetupDaemonConfig(logrus.StandardLogger(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, daemonConfig.InstanceID)
}
