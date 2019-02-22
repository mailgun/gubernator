package vulcand

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/mailgun/service/internal/logging"
	"github.com/mailgun/service/vulcand"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	frontendFmt   = "%sfrontends/%s.%s/frontend"
	middlewareFmt = "%sfrontends/%s.%s/middlewares/%s"
	backendFmt    = "%sbackends/%s/backend"
	serverFmt     = "%sbackends/%s/servers/%s"

	etcdOpTimeout  = 30 * time.Second
	backOffTimeout = 5 * time.Second
)

var (
	InitFlagmanAPIBaseURL = vulcand.InitFlagmanAPIBaseURL
)

type Registry struct {
	namespace     string
	ttl           time.Duration
	backendSpec   *backendSpec
	frontendSpecs []*frontendSpec

	etcdCtl  *etcd.Client
	log      *logrus.Entry
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewRegistry(etcdClt *etcd.Client, namespace, appName, hostname, ip string, port int, ttl time.Duration) (*Registry, error) {
	backendSpec, err := newBackendSpec(appName, hostname, ip, port)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create backend")
	}

	c := Registry{
		namespace:   namespace,
		ttl:         ttl,
		backendSpec: backendSpec,
		etcdCtl:     etcdClt,
		log:         logging.Log(),
		stopCh:      make(chan struct{}),
	}
	return &c, nil
}

func (r *Registry) AddFrontend(host, path, method string, middlewares []vulcand.Middleware) {
	r.frontendSpecs = append(r.frontendSpecs, newFrontendSpec(r.backendSpec.AppName, host, path, method, middlewares))
}

func (r *Registry) Start(ctx context.Context) error {
	// Update static routing Vulcand data in Etcd.
	if err := r.upsertBackendType(ctx, r.backendSpec); err != nil {
		return errors.Wrapf(err, "failed to register backend, %s", r.backendSpec.ID)
	}
	for _, fes := range r.frontendSpecs {
		if err := r.upsertFrontend(ctx, fes); err != nil {
			return errors.Wrapf(err, "failed to register frontend, %s", fes.ID)
		}
	}

	r.wg.Add(1)
	go r.run()
	return nil
}

func (r *Registry) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}

func (r *Registry) run() {
	defer r.wg.Done()
	for {
		var ka keepAliver
		var err error
	register:
		for {
			if ka, err = r.upsertBackendServerWithLease(); err == nil {
				goto keepAlive
			}
			r.log.WithError(err).Error("Failed to create server keep aliver")
			select {
			case <-time.After(backOffTimeout):
				continue register
			case <-r.stopCh:
				return
			}
		}
	keepAlive:
		for {
			select {
			case _, ok := <-ka.responseCh:
				if !ok {
					r.log.Error("Keep alive channel closed")
					goto register
				}
			case <-r.stopCh:
				ka.cancel()
				r.deleteBackendServerWithLease(ka.leaseID)
				return
			}
		}
	}
}

type keepAliver struct {
	leaseID    etcd.LeaseID
	cancel     context.CancelFunc
	responseCh <-chan *etcd.LeaseKeepAliveResponse
}

func (r *Registry) upsertBackendServerWithLease() (keepAliver, error) {
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), etcdOpTimeout)
	defer cancel()

	leaseGrantRs, err := r.etcdCtl.Grant(ctx, int64(r.ttl.Seconds()))
	if err != nil {
		return keepAliver{}, errors.Wrapf(err, "while granting a new lease")
	}
	key := fmt.Sprintf(serverFmt, r.namespace, r.backendSpec.AppName, r.backendSpec.ID)
	_, err = r.etcdCtl.Put(ctx, key, r.backendSpec.serverSpec(), etcd.WithLease(leaseGrantRs.ID))
	if err != nil {
		return keepAliver{}, errors.Wrap(err, "while writing backend server to Etcd")
	}

	ka := keepAliver{leaseID: leaseGrantRs.ID}
	ctx, ka.cancel = context.WithCancel(context.Background())
	if ka.responseCh, err = r.etcdCtl.KeepAlive(ctx, leaseGrantRs.ID); err != nil {
		return keepAliver{}, errors.Wrapf(err, "failed to start keep alive")
	}
	return ka, nil
}

func (r *Registry) deleteBackendServerWithLease(leaseID etcd.LeaseID) error {
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), etcdOpTimeout)
	defer cancel()
	key := fmt.Sprintf(serverFmt, r.namespace, r.backendSpec.AppName, r.backendSpec.ID)
	_, err = r.etcdCtl.Delete(ctx, key)
	if err != nil {
		return errors.Wrap(err, "while deleting backend server from Etcd")
	}
	_, err = r.etcdCtl.Revoke(ctx, leaseID)
	if err != nil {
		return errors.Wrap(err, "while revoking lease from Etcd")
	}
	return nil
}

func (r *Registry) upsertBackendType(ctx context.Context, beSpec *backendSpec) error {
	beTypeKey := fmt.Sprintf(backendFmt, r.namespace, beSpec.AppName)
	beTypeVal := beSpec.typeSpec()
	if _, err := r.etcdCtl.Put(ctx, beTypeKey, beTypeVal); err != nil {
		return errors.Wrapf(err, "while writing backend type to Etcd, %s", beTypeKey)
	}
	return nil
}

func (r *Registry) upsertFrontend(ctx context.Context, fes *frontendSpec) error {
	fesKey := fmt.Sprintf(frontendFmt, r.namespace, fes.Host, fes.ID)
	fesVal := fes.spec()
	if _, err := r.etcdCtl.Put(ctx, fesKey, fesVal); err != nil {
		return errors.Wrapf(err, "while writing frontend to Etcd, %s", fesKey)
	}
	for i, mw := range fes.Middlewares {
		mw.Priority = i
		mwKey := fmt.Sprintf(middlewareFmt, r.namespace, fes.Host, fes.ID, mw.ID)
		mwVal, err := json.Marshal(mw)
		if err != nil {
			return errors.Wrapf(err, "while JSON marshalling middleware, %v", mw)
		}
		if _, err = r.etcdCtl.Put(ctx, mwKey, string(mwVal)); err != nil {
			return errors.Wrapf(err, "while writing middleware to Etcd, %s", mwKey)
		}
	}
	return nil
}
