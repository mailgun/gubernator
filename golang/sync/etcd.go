package sync

import (
	"context"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/gubernator/golang"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	etcdTimeout    = time.Second * 10
	backOffTimeout = time.Second * 5
	leaseTTL       = 30
	rootKey        = "/gubernator/peers/"
)

type EtcdSync struct {
	callBack  gubernator.UpdateFunc
	peers     map[string]struct{}
	wg        holster.WaitGroup
	ctx       context.Context
	cancelCtx context.CancelFunc
	watchChan etcd.WatchChan
	log       *logrus.Entry
	client    *etcd.Client
	watcher   etcd.Watcher
}

func NewEtcdSync(client *etcd.Client) *EtcdSync {
	ctx, cancel := context.WithCancel(context.Background())
	return &EtcdSync{
		log:       logrus.WithField("category", "etcd-sync"),
		peers:     make(map[string]struct{}),
		cancelCtx: cancel,
		client:    client,
		ctx:       ctx,
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

func (e *EtcdSync) watchPeers() error {
	var revision int64

	// Update our list of peers
	if err := e.collectPeers(&revision); err != nil {
		return err
	}

	// Cancel any previous watches
	if e.watcher != nil {
		e.watcher.Close()
	}

	e.watcher = etcd.NewWatcher(e.client)

	ready := make(chan struct{})
	go func() {
		e.watchChan = e.watcher.Watch(etcd.WithRequireLeader(e.ctx), rootKey,
			etcd.WithRev(revision), etcd.WithPrefix(), etcd.WithPrevKV())
		close(ready)
	}()

	select {
	case <-ready:
		e.log.Infof("watching for peer changes @ ETCD revision %d", revision)
	case <-time.After(etcdTimeout):
		return errors.New("timed out while waiting for watcher.Watch() to start")
	}
	return nil
}

func (e *EtcdSync) collectPeers(revision *int64) error {
	ctx, cancel := context.WithTimeout(e.ctx, etcdTimeout)
	defer cancel()

	resp, err := e.client.Get(ctx, rootKey, etcd.WithPrefix())
	if err != nil {
		return errors.Wrapf(err, "while fetching peer listing from '%s'", rootKey)
	}

	// Collect all the peers
	for _, v := range resp.Kvs {
		e.peers[string(v.Value)] = struct{}{}
	}

	e.callOnUpdate()
	return nil
}

func (e *EtcdSync) watch() error {
	// Initialize watcher
	if err := e.watchPeers(); err != nil {
		return errors.Wrap(err, "while attempting to start watch")
	}

	e.wg.Until(func(done chan struct{}) bool {
		for response := range e.watchChan {
			if response.Canceled {
				e.log.Infof("graceful watch shutdown")
				return false
			}

			if err := response.Err(); err != nil {
				e.log.Errorf("watch error: %v", err)
				goto restart
			}

			for _, event := range response.Events {
				switch event.Type {
				case etcd.EventTypePut:
					if event.Kv != nil {
						e.log.Debugf("new peer [%s]", string(event.Kv.Value))
						e.peers[string(event.Kv.Value)] = struct{}{}
					}
				case etcd.EventTypeDelete:
					if event.PrevKv != nil {
						e.log.Debugf("removed peer [%s]", string(event.PrevKv.Value))
						delete(e.peers, string(event.PrevKv.Value))
					}
				}
				e.callOnUpdate()
			}
		}

	restart:
		// Are we in the middle of a shutdown?
		select {
		case <-done:
			return false
		case <-e.ctx.Done():
			return false
		default:
		}

		if err := e.watchPeers(); err != nil {
			e.log.WithError(err).
				Error("while attempting to restart watch")
			select {
			case <-time.After(backOffTimeout):
				return true
			case <-done:
				return false
			}
		}

		return true
	})
	return nil
}

func (e *EtcdSync) register(name string) error {
	instanceKey := rootKey + name
	e.log.Infof("Registering peer '%s' with etcd", name)

	var keepAlive <-chan *etcd.LeaseKeepAliveResponse
	var lease *etcd.LeaseGrantResponse

	register := func() error {
		ctx, cancel := context.WithTimeout(e.ctx, etcdTimeout)
		defer cancel()
		var err error

		lease, err = e.client.Grant(ctx, leaseTTL)
		if err != nil {
			return errors.Wrapf(err, "during grant lease")
		}

		_, err = e.client.Put(ctx, instanceKey, name, etcd.WithLease(lease.ID))
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
				e.log.WithError(err).
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
			if _, err := e.client.Delete(ctx, instanceKey); err != nil {
				e.log.WithError(err).
					Warn("during etcd delete")
			}

			if _, err := e.client.Revoke(ctx, lease.ID); err != nil {
				e.log.WithError(err).
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
	e.cancelCtx()
	e.wg.Stop()
}

func (e *EtcdSync) RegisterOnUpdate(fn gubernator.UpdateFunc) {
	e.callBack = fn
}

func (e *EtcdSync) callOnUpdate() {
	conf := gubernator.PeerConfig{}

	for k := range e.peers {
		conf.Peers = append(conf.Peers, k)
	}

	e.callBack(&conf)
}
