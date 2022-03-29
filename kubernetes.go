/*
Copyright 2018-2022 Mailgun Technologies Inc

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

	"github.com/mailgun/holster/v4/setter"
	"github.com/mailgun/holster/v4/syncutil"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	api_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type K8sPool struct {
	informer    cache.SharedIndexInformer
	client      *kubernetes.Clientset
	wg          syncutil.WaitGroup
	log         logrus.FieldLogger
	conf        K8sPoolConfig
	watchCtx    context.Context
	watchCancel func()
	done        chan struct{}
}

type WatchMechanism string

const (
	WatchEndpoints WatchMechanism = "endpoints"
	WatchPods      WatchMechanism = "pods"
)

func WatchMechanismFromString(mechanism string) (WatchMechanism, error) {
	switch WatchMechanism(mechanism) {
	case "": // keep default behavior
		return WatchEndpoints, nil
	case WatchEndpoints:
		return WatchEndpoints, nil
	case WatchPods:
		return WatchPods, nil
	default:
		return "", fmt.Errorf("unknown watch mechanism specified: %s", mechanism)
	}
}

type K8sPoolConfig struct {
	Logger    logrus.FieldLogger
	Mechanism WatchMechanism
	OnUpdate  UpdateFunc
	Namespace string
	Selector  string
	PodIP     string
	PodPort   string
}

func NewK8sPool(conf K8sPoolConfig) (*K8sPool, error) {
	config, err := RestConfig()
	if err != nil {
		return nil, errors.Wrap(err, "during InClusterConfig()")
	}
	// creates the client
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrap(err, "during NewForConfig()")
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &K8sPool{
		done:        make(chan struct{}),
		log:         conf.Logger,
		client:      client,
		conf:        conf,
		watchCtx:    ctx,
		watchCancel: cancel,
	}
	setter.SetDefault(&pool.log, logrus.WithField("category", "gubernator"))

	return pool, pool.start()
}

func (e *K8sPool) start() error {
	switch e.conf.Mechanism {
	case WatchEndpoints:
		return e.startEndpointWatch()
	case WatchPods:
		return e.startPodWatch()
	default:
		return fmt.Errorf("unknown value for watch mechanism: %s", e.conf.Mechanism)
	}
}

func (e *K8sPool) startGenericWatch(objType runtime.Object, listWatch *cache.ListWatch, updateFunc func()) error {
	e.informer = cache.NewSharedIndexInformer(
		listWatch,
		objType,
		0, //Skip resync
		cache.Indexers{},
	)

	e.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Add) '%s' - %v", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
			updateFunc()
		},
		UpdateFunc: func(obj, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Update) '%s' - %v", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
			updateFunc()
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			e.log.Debugf("Queue (Delete) '%s' - %v", key, err)
			if err != nil {
				e.log.Errorf("while calling MetaNamespaceKeyFunc(): %s", err)
				return
			}
			updateFunc()
		},
	})

	go e.informer.Run(e.done)

	if !cache.WaitForCacheSync(e.done, e.informer.HasSynced) {
		close(e.done)
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	return nil
}

func (e *K8sPool) startPodWatch() error {
	listWatch := &cache.ListWatch{
		ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = e.conf.Selector
			return e.client.CoreV1().Pods(e.conf.Namespace).List(context.Background(), options)
		},
		WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = e.conf.Selector
			return e.client.CoreV1().Pods(e.conf.Namespace).Watch(e.watchCtx, options)
		},
	}
	return e.startGenericWatch(&api_v1.Pod{}, listWatch, e.updatePeersFromPods)
}

func (e *K8sPool) startEndpointWatch() error {
	listWatch := &cache.ListWatch{
		ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = e.conf.Selector
			return e.client.CoreV1().Endpoints(e.conf.Namespace).List(context.Background(), options)
		},
		WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = e.conf.Selector
			return e.client.CoreV1().Endpoints(e.conf.Namespace).Watch(e.watchCtx, options)
		},
	}
	return e.startGenericWatch(&api_v1.Endpoints{}, listWatch, e.updatePeersFromEndpoints)
}

func (e *K8sPool) updatePeersFromPods() {
	e.log.Debug("Fetching peer list from pods API")
	var peers []PeerInfo
main:
	for _, obj := range e.informer.GetStore().List() {
		pod, ok := obj.(*api_v1.Pod)
		if !ok {
			e.log.Errorf("expected type v1.Endpoints got '%s' instead", reflect.TypeOf(obj).String())
		}

		peer := PeerInfo{GRPCAddress: fmt.Sprintf("%s:%s", pod.Status.PodIP, e.conf.PodPort)}

		// if containers are not ready or not running then skip this peer
		for _, status := range pod.Status.ContainerStatuses {
			if !status.Ready || status.State.Running == nil {
				e.log.Debugf("Skipping peer because it's not ready or not running: %+v\n", peer)
				continue main
			}
		}

		if pod.Status.PodIP == e.conf.PodIP {
			peer.IsOwner = true
		}
		e.log.Debugf("Peer: %+v\n", peer)
		peers = append(peers, peer)
	}
	e.conf.OnUpdate(peers)
}

func (e *K8sPool) updatePeersFromEndpoints() {
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
	e.watchCancel()
	close(e.done)
}
