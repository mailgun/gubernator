package etcdutil

import (
	"context"
	"os"
	"path"
	"sync/atomic"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/clientv3/concurrency"
	"github.com/mailgun/holster"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var log *logrus.Entry

type LeaderElector interface {
	IsLeader() bool
	LeaderChan() chan bool
	Concede() bool
	Start() error
	Stop()
}

type Election struct {
	conf       ElectionConfig
	session    *concurrency.Session
	election   *concurrency.Election
	client     *etcd.Client
	cancel     context.CancelFunc
	wg         holster.WaitGroup
	ctx        context.Context
	isLeader   int32
	leaderChan chan bool
}

type ElectionConfig struct {
	// The name of the election (IE: scout, blackbird, etc...)
	Election string
	// The name of this instance (IE: worker-n01, worker-n02, etc...)
	Candidate string
	// Seconds to wait before giving up the election if leader disconnected
	TTL int
	// Report not leader when etcd connection is interrupted
	LoseLeaderOnDisconnect bool
	// If we were leader before connection or service interruption attempt
	// to resume leadership without initiating a new election
	ResumeLeaderOnReconnect bool
	// How long to wait until attempting to establish a new session between failures
	ReconnectBackOff time.Duration
	// The size of the leader channel buffer as returned by LeaderChan(). Set this to
	// something other than zero to avoid losing leadership changes.
	LeaderChannelSize int
}

// Use leader election if you have several instances of a service running in production
// and you only want one of the service instances to preform a periodic task.
//
//  client, _ := etcdutil.NewClient(nil)
//
//  election := etcdutil.NewElection(client, etcdutil.ElectionConfig{
//      Election: "election-name",
//      Candidate: "",
//      TTL: 5,
//  })
//
//  // Start the leader election and attempt to become leader
//  if err := election.Start(); err != nil {
//      panic(err)
//  }
//
//	// Returns true if we are leader (thread safe)
//	if election.IsLeader() {
//		// Do periodic thing
//	}
//
//  select {
//  case isLeader := <-election.LeaderChan():
//  	fmt.Printf("Leader: %t\n", isLeader)
//  }
//
// NOTE: If this instance is elected leader and connection is interrupted to etcd,
// this library will continue to report it is leader until connection to etcd is resumed
// and a new leader is elected. If you wish to lose leadership on disconnect set
// `LoseLeaderOnDisconnect = true`
func NewElection(client *etcd.Client, conf ElectionConfig) LeaderElector {
	log = logrus.WithField("category", "election")
	ctx, cancelFunc := context.WithCancel(context.Background())
	e := &Election{
		conf:   conf,
		client: client,
		cancel: cancelFunc,
		ctx:    ctx,
	}

	// Default to short 5 second leadership TTL
	holster.SetDefault(&e.conf.TTL, 5)

	e.leaderChan = make(chan bool, e.conf.LeaderChannelSize)
	e.conf.Election = path.Join("/elections", e.conf.Election)

	// Use the hostname if no candidate name provided
	if host, err := os.Hostname(); err == nil {
		holster.SetDefault(&e.conf.Candidate, host)
	}
	return e
}

func (e *Election) newSession(id int64) error {
	var err error
	e.session, err = concurrency.NewSession(e.client, concurrency.WithTTL(e.conf.TTL),
		concurrency.WithContext(e.ctx), concurrency.WithLease(etcd.LeaseID(id)))
	if err != nil {
		return errors.Wrap(err, "while creating new session")
	}
	e.election = concurrency.NewElection(e.session, e.conf.Election)
	return nil
}

func (e *Election) Start() error {
	if e.conf.Election == "" {
		return errors.New("ElectionConfig.Election can not be empty")
	}

	err := e.newSession(0)
	if err != nil {
		return errors.Wrap(err, "while creating new initial etcd session")
	}

	e.wg.Until(func(done chan struct{}) bool {
		var node *etcd.GetResponse
		var observe <-chan etcd.GetResponse

		// Get the current leader if any
		if node, err = e.election.Leader(e.ctx); err != nil {
			if err != concurrency.ErrElectionNoLeader {
				log.Errorf("while determining election leader: %s", err)
				goto reconnect
			}
		} else {
			// If we are resuming an election from which we previously had leadership we
			// have 2 options
			// 1. Resume the leadership if the lease has not expired. This is a race as the
			//    lease could expire in between the `Leader()` call and when we resume
			//    observing changes to the election. If this happens we should detect the
			//    session has expired during the observation loop.
			// 2. Resign the leadership immediately to allow a new leader to be chosen.
			//    This option will almost always result in transfer of leadership.

			// If we were the leader previously
			if string(node.Kvs[0].Value) == e.conf.Candidate {
				log.Debug("etcd reports we are still leader")
				if e.conf.ResumeLeaderOnReconnect {
					log.Debug("attempting to resume leadership")
					// Recreate our session with the old lease id
					if err = e.newSession(node.Kvs[0].Lease); err != nil {
						log.Errorf("while re-establishing session with lease: %s", err)
						// abandon resuming leadership
						e.notifyLeaderChange(false)
						goto reconnect
					}
					e.election = concurrency.ResumeElection(e.session, e.conf.Election,
						string(node.Kvs[0].Key), node.Kvs[0].CreateRevision)

					// Because Campaign() only returns if the election entry doesn't exist
					// we must skip the campaign call and go directly to observe when resuming
					goto observe
				} else {
					log.Debug("resigning leadership")
					// If resign takes longer than our TTL then lease is expired and we are no longer leader anyway.
					ctx, cancel := context.WithTimeout(e.ctx, time.Duration(e.conf.TTL)*time.Second)
					election := concurrency.ResumeElection(e.session, e.conf.Election,
						string(node.Kvs[0].Key), node.Kvs[0].CreateRevision)
					err = election.Resign(ctx)
					cancel()
					e.notifyLeaderChange(false)
					if err != nil {
						log.Errorf("while resigning leadership after reconnect: %s", err)
						goto reconnect
					}
				}
			}
		}

		// Reset leadership if we had it previously
		e.setLeader(false)

		// Attempt to become leader
		if err := e.election.Campaign(e.ctx, e.conf.Candidate); err != nil {
			if errors.Cause(err) == context.Canceled {
				return false
			}
			// Campaign does not return an error if session expires
			log.Errorf("while campaigning for leader: %s", err)
			e.session.Close()
			goto reconnect
		}

	observe:
		// If Campaign() returned without error, we are leader
		e.setLeader(true)

		// Observe changes to leadership
		observe = e.election.Observe(e.ctx)
		for {
			select {
			case resp, ok := <-observe:
				if !ok {
					// Observe does not close if the session expires
					e.session.Close()
					goto reconnect
				}
				if string(resp.Kvs[0].Value) == e.conf.Candidate {
					log.Debug("is Leader")
					e.setLeader(true)
				} else {
					// We are not leader
					log.Debug("not Leader")
					e.setLeader(false)
					return true
				}
			case <-e.ctx.Done():
				return false
			case <-e.session.Done():
				log.Debug("session cancelled while observing election")
				goto reconnect
			}
		}

	reconnect:
		if e.conf.LoseLeaderOnDisconnect {
			e.setLeader(false)
		}

		for {
			log.Debug("disconnected, creating new session")
			if err = e.newSession(0); err != nil {
				if errors.Cause(err) == context.Canceled {
					return false
				}
				log.Errorf("while creating new etcd session: %s", err)
				tick := time.NewTicker(e.conf.ReconnectBackOff)
				select {
				case <-e.ctx.Done():
					tick.Stop()
					return false
				case <-tick.C:
					tick.Stop()
				}
				continue
			}
			break
		}
		log.Debug("New session created")
		return true
	})

	// Wait until we have a leader before returning
	for {
		resp, err := e.election.Leader(e.ctx)
		if err != nil {
			if err != concurrency.ErrElectionNoLeader {
				return err
			}
			time.Sleep(time.Millisecond * 300)
			continue
		}
		// If we are not leader, notify the channel
		if string(resp.Kvs[0].Value) != e.conf.Candidate {
			e.notifyLeaderChange(false)
		}
		break
	}
	return nil
}

func (e *Election) Stop() {
	e.Concede()
	e.cancel()
	e.wg.Wait()
	close(e.leaderChan)
}

func (e *Election) IsLeader() bool {
	return atomic.LoadInt32(&e.isLeader) == 1
}

func (e *Election) LeaderChan() chan bool {
	return e.leaderChan
}

// Release leadership and return true if we own it, else do nothing and return false
func (e *Election) Concede() bool {
	if atomic.LoadInt32(&e.isLeader) == 1 {
		// If resign takes longer than our TTL then lease is expired and we are no longer leader anyway.
		ctx, cancel := context.WithTimeout(e.ctx, time.Duration(e.conf.TTL)*time.Second)
		if err := e.election.Resign(ctx); err != nil {
			log.WithField("err", err).
				Error("while attempting to concede the election")
		}
		cancel()
		e.setLeader(false)
		return true
	}
	return false
}

func (e *Election) setLeader(set bool) {
	var requested int32
	if set {
		requested = 1
	}

	// Only notify if leadership changed
	if requested == atomic.LoadInt32(&e.isLeader) {
		return
	}

	atomic.StoreInt32(&e.isLeader, requested)
	e.notifyLeaderChange(set)
}

func (e *Election) notifyLeaderChange(set bool) {
	// If user expects to receive all leadership changes
	if e.conf.LeaderChannelSize != 0 {
		// Block until the leadership change is delivered
		e.leaderChan <- set
		return
	}

	// Else do not block if channel is not ready to received
	select {
	case e.leaderChan <- set:
	default:
	}
}

type LeaderElectionMock struct{}

func (s *LeaderElectionMock) IsLeader() bool        { return true }
func (s *LeaderElectionMock) LeaderChan() chan bool { return nil }
func (s *LeaderElectionMock) Concede() bool         { return true }
func (s *LeaderElectionMock) Start() error          { return nil }
func (s *LeaderElectionMock) Stop()                 {}
