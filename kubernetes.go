/*
Copyright 2018-2019 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

import (
	"context"
	"fmt"
	"reflect"

	"github.com/mailgun/holster/v3/setter"

	"github.com/mailgun/holster/v3/syncutil"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	api_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type K8sPool struct {
	client    *kubernetes.Clientset
	peers     map[string]struct{}
	cancelCtx context.CancelFunc
	wg        syncutil.WaitGroup
	ctx       context.Context
	log       logrus.FieldLogger
	conf      K8sPoolConfig
	informer  cache.SharedIndexInformer
	done      chan struct{}
}

type K8sPoolConfig struct {
	OnUpdate  UpdateFunc
	Namespace string
	Selector  string
	PodIP     string
	PodPort   string
	Logger    logrus.FieldLogger
}

func NewK8sPool(conf K8sPoolConfig) (*K8sPool, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, errors.Wrap(err, "during InClusterConfig()")
	}
	// creates the client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "during NewForConfig()")
	}

	pool := &K8sPool{
		log:    conf.Logger,
		peers:  make(map[string]struct{}),
		done:   make(chan struct{}),
		client: client,
		conf:   conf,
	}
	setter.SetDefault(&pool.log, logrus.WithField("category", "gubernator"))

	return pool, pool.start()
}

func (e *K8sPool) start() error {

	e.informer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = e.conf.Selector
				return e.client.CoreV1().Endpoints(e.conf.Namespace).List(options)
			},
			WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = e.conf.Selector
				return e.client.CoreV1().Endpoints(e.conf.Namespace).Watch(options)
			},
		},
		&api_v1.Endpoints{},
		0, //Skip resync
		cache.Indexers{},
	)

	e.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Add) '%s' - %s", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
		},
		UpdateFunc: func(obj, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Update) '%s' - %s", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
			e.updatePeers()
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Delete) '%s' - %s", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
			e.updatePeers()
		},
	})

	go e.informer.Run(e.done)

	if !cache.WaitForCacheSync(e.done, e.informer.HasSynced) {
		close(e.done)
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	return nil
}

func (e *K8sPool) updatePeers() {
	e.log.Debug("Fetching peer list from endpoints API")
	var peers []PeerInfo
	for _, obj := range e.informer.GetStore().List() {
		endpoint, ok := obj.(*api_v1.Endpoints)
		if !ok {
			e.log.Errorf("expected type v1.Endpoints got '%s' instead", reflect.TypeOf(obj).String())
		}

		for _, s := range endpoint.Subsets {
			for _, addr := range s.Addresses {
				// TODO(thrawn01): Might consider using the `namespace` as the `DataCenter`. We should
				//  do what ever k8s convention is for identifying a k8s cluster within a federated multi-data
				//  center setup.
				peer := PeerInfo{GRPCAddress: fmt.Sprintf("%s:%s", addr.IP, e.conf.PodPort)}

				if addr.IP == e.conf.PodIP {
					peer.IsOwner = true
				}
				peers = append(peers, peer)
				e.log.Debugf("Peer: %+v\n", peer)
			}
		}
	}
	e.conf.OnUpdate(peers)
}

func (e *K8sPool) Close() {
	close(e.done)
}
