package gubernator

type ClusterConfiger interface {
	OnUpdate(func(*ClusterConfig))
	Start() error
	Stop()
}

type ClusterConfig struct {
	Peers []string
}

type ServerConfig struct {
	ListenAddress string
	ClusterConfig ClusterConfiger
	Picker        PeerPicker
}

type StaticClusterConfig struct {
	Conf ClusterConfig
}

func (sc *StaticClusterConfig) OnUpdate(cb func(config *ClusterConfig)) {
	// Calls the call back immediately with our current config
	cb(&sc.Conf)
}
