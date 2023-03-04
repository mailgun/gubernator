# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## As of 2.0.0-rc.42 this CHANGELOG is deprecated in favor of using github's change release feature.

## [2.0.0-rc.42] - 2023-03-03
## What's Changed
* Allow binding the daemon to a random port by @pzduniak in #167

## [2.0.0-rc.40] - 2023-03-01
## What's Changed
* Adjust tracing detail by @Baliedge in #166.
   * Adjust certain functions to debug level tracing. Mainly private methods are debug tracing.
   * Give background async goroutine traces descriptive names.
   * Update holster for additional functionality.
   * Adjust dependency versions to ensure compatibility with holster.

## [2.0.0-rc.39] - 2023-02-28
## What's Changed
* Simplify OTel traces by @Baliedge in #165.

## [2.0.0-rc.38] - 2023-02-20
## What's Changed
* Update dependencies to patch for CVE-2022-27664 by @Baliedge in #164.

## [2.0.0-rc.37] - 2023-01-12
## What's Changed
* Token bucket fix for Gregorian duration by @akshay-livespace in #162.
  * Token bucket algorithm would reset "remaining" for a request within milliseconds when using behavior DURATION_IS_GREGORIAN.
  * This is happening because expiry calculation for behavior DURATION_IS_GREGORIAN, was calculated but never updated.

## [2.0.0-rc.36] - 2022-12-07
## What's Changed
* Add environment variable to set minimum TLS version on server tls config by
@denkyl08 in #160.

## [2.0.0-rc.35] - 2022-10-28
## What's Changed
* No functional change.
* GitHub Actions provide version consistency check by @Baliedge in #158.

## [2.0.0-rc.34] - 2022-10-21
## What's Changed
* Update Go to 1.19.2 by @pzduniak in #155

## [2.0.0-rc.33] - 2022-10-21
## Changes
* Fix negative hits when remaining 0 by @Loosetooth in #157.

## [2.0.0-rc.32] - 2022-10-11
## Changes
* Bump etcd to v3.5.5 by @thrawn01 in #154

## [2.0.0-rc.31] - 2022-10-11
## Changes
* Now using the logger passed in on initialization by @thrawn01 in #153

## [2.0.0-rc.30] - 2022-09-26
## Changes
* Bumps protobuf from 3.15.0 to 3.18.3 by @dependabot in #152.

## [2.0.0-rc.29] - 2022-08-16
## Changes
* Promote asyncRequests() span to info level so it's not filtered out by
default by @Baliedge in #150.

## [2.0.0-rc.28] - 2022-08-15
## Changes
* Tag some spans as debug by @Baliedge in #149.

## [2.0.0-rc.27] - 2022-08-12
## Changes
* Update holster to get new `tracing` functionality.
* Tag spans with debug log level to improve signal:noise ratio.

by @Baliedge in #148.

## [2.0.0-rc.26] - 2022-08-02
## Changes
* Update Go to 1.17.13 by @pzduniak in #147.

## [2.0.0-rc.25] - 2022-08-01
## Changes
* Refactor tracing using holster OpenTelemetry tooling in #125.
   * [OpenTelemetry](https://opentelemetry.io) is newer standard that supercedes OpenTracing.
   * This change migrates to OpenTelemetry. However, the traces created in Jaeger remain the same structure and level of detail.
   * **Breaking change**: Configuration for OpenTelemetry changes from OpenTracing. See `jaegertracing.md` for details.

## [2.0.0-rc.24] - 2022-06-07
## Changes
* Update Go to 1.17.11 by @pzduniak in #145.

## [2.0.0-rc.23] - 2022-05-23
## Changes
* Add support for pre-1.23 Kubernetes versions in #144.

## [2.0.0-rc.22] - 2022-05-18
## Changes
* Update to Go 1.17.10 by @pzduniak in #143.

## [2.0.0-rc.21] - 2022-05-17
## Changes
* Publish a Helm repository by @pzduniak in #139.

## [2.0.0-rc.20] - 2022-05-17
## Changes
* Publish multi-arch images #142
* Helm chart add image pull secrets #141
* Pin Go to 1.17.9, always pull the base images #140

## [2.0.0-rc.19] - 2022-04-21
## Changes
* Log level and format config.

## [2.0.0-rc.18] - 2022-04-19
## Changes
* Various Helm chart fixes.

## [2.0.0-rc.17] - 2022-04-08
## Changes
* Update prometheus client_golang to resolve CVE-2022-21698.

## [2.0.0-rc.16] - 2022-02-22
## Changes
* Added metric to track number of cache evictions which involve unexpired entries
* Allow configuration of ServerName used by peer clients to avoid necessity
 of IP SANs in Cert #133

## [2.0.0-rc.15] - 2022-02-22
## Changes
* Apply security updates to Golang libraries to fix CVE-2021-38561,
CVE-2021-33194, and CVE-2020-29652.

## [2.0.0-rc.14] - 2022-02-17
## Changes
* Added performance optimizations to ensure batching behavior does not cause
additional performance bottlenecks at scale.

## [2.0.0-rc.13] - 2022-01-19
## Changes
* Added Opentracing support in gRPC service and various critical functions.
* Added many more useful Prometheus metrics.
* Refactored GetRateCheck calls to use a hash ring for parallel processing,
instead of locking a shared mutex to process requests sequentially.
* Rate checks now respect client's context deadline and will abort processing
immediately if canceled.
* Fixed stack overflow panic in token bucket ratelimit checking.
* Fixed leaky bucket ratelimits expiring prematurely.

## [2.0.0-rc.12] - 2021-12-28
## Changes
* Include s.conf.Behaviors in Config for NewV1Instance

## [2.0.0-rc.11] - 2021-11-01
## Changes
* Moved official gubernator container to ghcr.io

## [2.0.0-rc.10] - 2021-10-25
## Changes
* Fixed async send when sending multiple rate limits to other nodes
* Fix leaky bucket reset time #110

## [2.0.0-rc.9] - 2021-10-12
## Changes
* Fixed infinite loop in async send

## [2.0.0-rc.7] - 2021-08-20
## Added
* Added optional os and golang internal metrics collectors

## [2.0.0-rc.6] - 2021-08-20
## Changes
* JSON responses are now back to their original camel_case form
* Fixed reporting of number of peers in health check

## [2.0.0-rc.5] - 2021-06-03
## Changes
* Implemented performance recommendations reported in Issue #74

## [2.0.0-rc.4] - 2021-06-03
## Changes
* Add support for burst in leaky bucket #103
* Add working example of aws ecs service discovery deployment #102

## [2.0.0-rc.2] - 2021-07-11
## Changes
* Deprecated github.com/golang/protobuf was replaced with google.golang.org/protobuf
* github.com/grpc-ecosystem/grpc-gateway was upgraded to v2

## [2.0.0-rc.1] - 2021-07-02
## Changes
* github.com/coreos/etcd was replaced with go.etcd.io/etcd/client/v3. This is
 an API breaking change. It entailed updated of github.com/mailgun/holster to
 the next major version v4
* Deprecated ConsistentHash was removed
* HashBytes64 is replaced with HashString64 to avoid unsafe conversions that is
 reported by go vet since v1.16 https://github.com/golang/go/issues/40701#issue-677219527

## [1.0.0-rc.8] - 2021-03-16
## Added
* Add GUBER_GRPC_MAX_CONN_AGE_SEC to limit GRPC keep alive

## [1.0.0-rc.7] - 2021-02-10
## Changes
* Fix leaky bucket algorithm returning remaining more than limit

## [1.0.0-rc.6] - 2020-12-21
## Changes
* Update the k8s example to reflect the latest changes from the release
candidate.

## [1.0.0-rc.5] - 2020-12-21
## Changes
* Respect SIGTERM from docker during shutdown
* Peer info provided to etcd and memberlist pools is now consistent
* Fixed a race in getGlobalRateLimit
* Fixed issues with EtcdPool
* Changes in preparation of MultiRegion support testing
### Added
* Added GUBER_K8S_WATCH_MECHANISM for k8s deployments.

## [1.0.0-rc.4] - 2020-12-18
### Change
* Fix leaky bucket algorithm

## [1.0.0-rc.3] - 2020-11-10
### Change
* Added TLS Support for both GRPC and HTTP interfaces #76
* Prometheus metrics are now prefixed with `gubernator_`
* Switched prometheus Histograms to Summary's
* Changed gubernator.Config.GRPCServer to GRPCServers to support registering
with GRPC instances on multiple ports.
* Gubernator now opens a second GRPC instance on a random localhost port when
TLS is enabled for use by the HTTP API Gateway.

## [1.0.0-rc.2] - 2020-11-05
### Change
* Add Service Account to k8s deployment yaml

## [1.0.0-rc.1] - 2020-10-22
### Change
* Added `GUBER_DATA_CENTER` as a config option
* Use `GUBER_PEER_DISCOVERY_TYPE` to pick a peer discovery type, removed
'Enable' options from k8s, etcd, and member-list.
* Added `GUBER_ADVERTISE_ADDRESS` to specify which address is published for
discovery
* Gubernator now attempts to detect the proper `GUBER_ADVERTISE_ADDRESS` if
not specified
* Gubernator now binds to `localhost` by default instead of binding to
`0.0.0.0:80` to avoid allowing
  access to a test version of gubernator from the network.
* Fix inconsistent tests failing #57
* Fix GRPC/HTTP Gateway #50
* Renamed functions to ensure clarity of version
* Removed deprecated `EtcdAdvertiseAddress` config option
* Refactored configuration options
* `member-list` metadata no longer assumes the member-list address is the same
  as the gubernator advertise address.
* Now MD5 sums the peer address key when using replicated hash. This ensures
  better key distribution when using domain names or ip address that are very
  similar. (gubernator-1, gubernator-2, etc...)
* Now defaults to `replicated-hash` if `GUBER_PEER_PICKER` is unset
* Added support for DataCenter fields when using etcd discovery
* Now storing member-list metadata as JSON instead of glob

## [0.9.2] - 2020-10-23
### Change
* ETCD discovery now sets the IsOwner property when updating the peers list.

## [0.9.1] - 2020-10-19
### Change
* Fix GUBER_PEER_PICKER_HASH and GUBER_PEER_PICKER
* Now warns if GUBER_PEER_PICKER value is incorrect
* Now ignoring spaces between `key = value` in config file

## [0.9.0] - 2020-10-13
### Change
* Fix GUBER_MEMBERLIST_ADVERTISE_PORT value type
* Fixed race condition and updated tests for limit change
* Fix limit change not having effect until reset

## [0.8.0] - 2020-01-04
### Added
* Allow cache users to invalidate a ratelimit after a specific time
* Changing limit and duration before expire should now work correctly
* Added Behavior RESET_REMAINING to reset any hits recorded in the cache for the
  specified rate limit

## Changes
* TokenBucketItem is now provided when `OnChange()` is called instead of `RateLimitResp`
* Fixed a bug in global behaviour where it would return an error if the async
  update had not occured before the a second request is made. Now it acts like it
  owns the rate limit until the owning node sends an update
* Always include reset_time in leaky bucket responses
* Fixed subtle bug during shutdown where PeerClient passed into goroutine could
  be out of scope/changed when routine runs
* Behavior is now a flag, this should be a backward compatible change for
  anyone using GLOBAL or NO_BATCHING but will break anyone using
  DURATION_IS_GREGORIAN. Use `HasBehavior()` function to check for behavior
  flags.

## [0.7.1] - 2019-12-10
### Added
* Added `Loader` interface for only loading and saving at startup and shutdown
* Added `Store` interface for continuous synchronization between the persistent store and the cache.
### Changes
* Moved `cache.Cache` into the `gubernator` package
* Changed the `Cache` interface to use `CacheItem` for storing and retrieving cached items.

## [0.6.0] - 2019-11-12
### Added
* DURATION_IS_GREGORIAN behavior to support interval based ratelimit durations
* Fixed issue where switching to leakybucket was impossible
* Fixed rate would never decrease if the client continued to add hits and failed.

## [0.5.0] - 2019-07-23
### Added
* Support for prometheus monitoring
* Support for environment based config
* Support for kubernetes peer discovery

## [0.4.0] - 2019-07-16
### Added
* Support for GLOBAL behavior
* Improved README documentation
### Changes
* GetRateLimits() now fetches rate limits asynchronously

## [0.3.2] - 2019-06-03
### Changes
* Now properly respecting the maxBatchLimit when talking with peers

## [0.3.1] - 2019-06-03
### Changes
* Minor log wording change when registering etcd pool

## [0.3.0] - 2019-05-16
### Changes
* Initial Release
