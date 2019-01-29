package gubernator

type ClusterConfiger interface {
	OnUpdate(func(*ClusterConfig) error)
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
