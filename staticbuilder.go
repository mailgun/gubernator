package gubernator

import (
	"strings"

	"google.golang.org/grpc/resolver"
)

type staticBuilder struct{}

var _ resolver.Builder = (*staticBuilder)(nil)

func (sb *staticBuilder) Scheme() string {
	return "static"
}

func (sb *staticBuilder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	var resolverAddrs []resolver.Address
	for _, address := range strings.Split(target.Endpoint(), ",") {
		resolverAddrs = append(resolverAddrs, resolver.Address{
			Addr:       address,
			ServerName: address,
		})
	}
	if err := cc.UpdateState(resolver.State{Addresses: resolverAddrs}); err != nil {
		return nil, err
	}
	return &staticResolver{cc: cc}, nil
}

// NewStaticBuilder returns a builder which returns a staticResolver that tells GRPC
// to connect a specific peer in the cluster.
func NewStaticBuilder() resolver.Builder {
	return &staticBuilder{}
}

type staticResolver struct {
	cc resolver.ClientConn
}

func (sr *staticResolver) ResolveNow(_ resolver.ResolveNowOptions) {}

func (sr *staticResolver) Close() {}

var _ resolver.Resolver = (*staticResolver)(nil)
