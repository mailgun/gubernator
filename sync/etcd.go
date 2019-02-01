package sync

import (
	"context"
	"fmt"
	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/gubernator"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"time"
)

const (
	etcdTimeout    = time.Second * 30
	backOffTimeout = time.Second * 5
	leaseTTL       = 30
)

type EtcdSync struct {
	callBack gubernator.UpdateFunc
	wg       holster.WaitGroup
	ctx      context.Context
	log      *logrus.Entry
	client   *etcd.Client
}

func NewEtcdSync(client *etcd.Client) *EtcdSync {
	return &EtcdSync{
		log:    logrus.WithField("category", "etcd-sync"),
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
	var key = "/gubernator/peers"

	// TODO: Get the most recent list of peers
	afterIdx := 0

	watcher := etcd.NewWatcher(e.client)
	defer watcher.Close()

	var watchChan etcd.WatchChan
	ready := make(chan struct{})
	go func() {
		watchChan = watcher.Watch(etcd.WithRequireLeader(e.ctx), key,
			etcd.WithRev(int64(afterIdx)), etcd.WithPrefix())
		close(ready)
	}()

	select {
	case <-ready:
		e.log.Infof("begin watching: etcd revision %d", afterIdx)
	case <-time.After(time.Second * 10):
		return errors.New("timed out while waiting for watcher.Watch() to start")
	}

	for response := range watchChan {
		if response.Canceled {
			e.log.Infof("graceful watch shutdown")
			return nil
		}
		if err := response.Err(); err != nil {
			e.log.Errorf("watch error: %v", err)
			return err
		}

		// TODO: When peers change, get a full peer listing? (might be too much overhead)
		/*for _, event := range response.Events {
			change, err := n.parseChange(event)
			if err != nil {
				log.Warningf("ignore '%s', error: %s", eventToString(event), err)
				continue
			}
			if change != nil {
				log.Infof("%v", change)
				select {
				case changes <- change:
				case <-cancelC:
					return nil
				}
			}
		}*/
	}
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
			return errors.Wrapf(err, "during grant lease")
		}

		_, err = e.client.Put(ctx, key, "", etcd.WithLease(lease.ID))
		if err != nil {
			return errors.Wrap(err, "during put")
		}

		if keepAlive, err = e.client.KeepAlive(e.ctx, lease.ID); err != nil {
			return err
		}
		return nil
	}

	var err error
	var lastKeepAlive time.Time

	// Attempt to register our instance with etcd
	if err = register(); err != nil {
		return errors.Wrap(err, "during initial peer registration")
	}

	e.wg.Until(func(done chan struct{}) bool {
		// If we have lost our keep alive, register again
		if keepAlive == nil {
			if err = register(); err != nil {
				e.log.WithField("err", err).
					Error("while attempting to re-register peer")
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
				// Don't re-register if we are in the middle of a shutdown
				if e.ctx.Err() != nil {
					return true
				}

				e.log.Warn("keep alive lost, attempting to re-register peer")
				// re-register
				keepAlive = nil
				return true
			}

			// Ensure we are getting keep alive's regularly
			if lastKeepAlive.Sub(time.Now()) > time.Second*leaseTTL {
				e.log.Warn("to long between keep alive heartbeats, re-registering peer")
				keepAlive = nil
				return true
			}
			lastKeepAlive = time.Now()
		case <-done:
			ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
			if _, err := e.client.Delete(ctx, key); err != nil {
				e.log.WithField("err", err).
					Warn("during etcd delete")
			}

			if _, err := e.client.Revoke(ctx, lease.ID); err != nil {
				e.log.WithField("err", err).
					Warn("during lease revoke ")
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
	e.ctx.Done()
}

func (e *EtcdSync) RegisterOnUpdate(fn gubernator.UpdateFunc) {
	e.callBack = fn
}
