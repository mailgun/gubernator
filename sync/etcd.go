package sync

import (
	"context"
	"fmt"
	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"time"
)

const (
	etcdTimeout    = time.Second * 30
	backOffTimeout = time.Second * 5
	leaseTTL       = 30
)

type EtcdSync struct {
	client *etcd.Client
	wg     holster.WaitGroup
}

func NewEtcdSync(client *etcd.Client) *EtcdSync {
	return &EtcdSync{
		client: client,
	}

}
func (e *EtcdSync) Start(name string) error {

	// Register our instance with etcd
	if err := e.register(name); err != nil {
		return err
	}

	// Get our peer list and watch for changes
	if err := e.watch(); err != nil {
		return err
	}
	return nil
}

func (e *EtcdSync) watch() error {
	// TODO: Copy watch code from vulcand, it's the most reliable implementation
	return nil
}

func (e *EtcdSync) register(name string) error {
	key := fmt.Sprintf("/gubernator/peers/%s", name)
	var keepAlive <-chan *etcd.LeaseKeepAliveResponse
	var lease *etcd.LeaseGrantResponse

	register := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
		defer cancel()
		var err error

		lease, err = e.client.Grant(ctx, leaseTTL)
		if err != nil {
			return errors.Wrapf(err, "while granting a new lease")
		}

		_, err = e.client.Put(ctx, key, "", etcd.WithLease(lease.ID))
		if err != nil {
			return errors.Wrap(err, "while registering server to ETCD")
		}

		if keepAlive, err = e.client.KeepAlive(ctx, lease.ID); err != nil {
			return err
		}
		return nil
	}

	var err error
	var lastKeepAlive time.Time

	// Attempt to register our instance with etcd
	if err = register(); err != nil {
		return err
	}

	e.wg.Until(func(done chan struct{}) bool {
		// If we have lost our keep alive, register again
		if keepAlive == nil {
			if err = register(); err != nil {
				// TODO: Log
				select {
				case <-time.After(backOffTimeout):
					return true
				case <-done:
					return false
				}
			}
		}

		select {
		case _, ok := <-keepAlive:
			if !ok {
				// TODO: Log
				// re-register
				keepAlive = nil
				return true
			}

			// Ensure we are getting keep alive's regularly
			if lastKeepAlive.Sub(time.Now()) > time.Second*leaseTTL {
				// TODO: Log
				// Took too long between keep alive's, the lease has expired
				keepAlive = nil
				return true
			}
			lastKeepAlive = time.Now()
		case <-done:
			ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
			if _, err := e.client.Delete(ctx, key); err != nil {
				// TODO: Log
			}

			if _, err := e.client.Revoke(ctx, lease.ID); err != nil {
				// TODO: Log
			}
			cancel()
			return false
		}
		return true
	})

	return nil
}

func (e *EtcdSync) Stop() {
	e.wg.Stop()
}

func (e *EtcdSync) RegisterOnUpdate(gubernator.UpdateFunc) {

}
