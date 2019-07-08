# Gubernator

Gubernator is a distributed, high performance, cloud native and stateless rate
limiting service designed to support many different rate limiting scenarios.

#### Scenarios
* Meter ingress traffic
* Meter egress traffic
* Limit bursts on network queues
* Enforce capacity limits on network services

## Architecture overview

![gubernator arch image](/architecture.png)

Gubernator is designed to run as a distributed cluster of peers which utilize
an in memory cache of all the currently active rate limits, as such no data is
ever synced to disk. Since most network based rate limit durations are held for
only a few seconds losing the in memory cache during a reboot or scheduled
downtime isn't a huge deal. For Gubernator we choose performance over accuracy
as it's acceptable for a small subset of traffic to be allowed to over request
for a short period of time (usually milliseconds) in the case of cache loss.

When a rate limit request is made to Gubernator the request is keyed and a
consistent hashing algorithm is applied to determine which of the peers will be
the owner of the rate limit request. Choosing a single owner for a rate limit
makes atomic increments of counts very fast and avoids the complexity and
latency involved in distributing counts consistently across a cluster of peers.

Although simple and performant this design can be susceptible to a thundering
herd of requests since a single coordinator is responsible for possibly
hundreds of thousands of requests to a rate limit. To combat this peers can
take multiple requests within a specified window and batch the requests into a
single peer request, thus reducing the total number of requests to a single
Gubernator peer tremendously.

To ensure each peer in the cluster accurately calculates the correct hash
for a rate limit key, the list of peers in the cluster must be distributed
to each peer in the cluster in a timely and consistent manner. Currently
Gubernator uses Etcd to distribute the list of peers, This could be later
expanded to a consul or a custom consistent implementation which would further
simplify deployment.

## Gubernator Operation

Unlike other generic rate limit service implementations, Gubernator does not have
the concept of pre-configured rate limit that clients make requests against.
Instead each request to the service includes the rate limit config to be
applied to the request. This allows clients the flexibility to govern their
rate limit problem domain without the need to coordinate rate limit
configuration deployments with Gubernator.

When a client or service makes a request to Gubernator the rate limit config is
provided with each request by the client. The rate limit configuration is then
stored with the current rate limit status in the local cache of the rate limit
owner. Rate limits and their configuration that are stored in the local cache
will only exist for the specified duration of the rate limit configuration.
After the duration time has expired, and if the rate limit was not requested
again within the duration it is dropped from the cache. Subsequent requests for
the same `name` and `unique_key` pair will recreate the config and rate limit
in the cache and the cycle will repeat.  Subsequent requests with different
configs will overwrite the previous config and will apply the new config
immediately.

An example rate limit request sent via GRPC might look like the following
```yaml
rate_limits:
    # Scopes the unique_key to your application to avoid collisions with 
    # other applications that might also use the same unique_key
    name: requests_per_sec
    # A unique_key that identifies this rate limit request
    unique_key: account_id=123|source_ip=172.0.0.1
    # The number of hits we are requesting
    hits: 1
    # The total number of requests allowed for this rate limit
    limit: 100
    # The duration of the rate limit in milliseconds
    duration: 1000
    # The algorithm used to calculate the rate limit  
    # 0 = Token Bucket
    # 1 = Leaky Bucket
    algorithm: 0
    # The behavior of the rate limit in gubernator.
    # 0 = BATCHING (Enables batching of requests to peers)
    # 1 = NO_BATCHING (Disables batching)
    # 2 = GLOBAL (Enable global caching for this rate limit)
    behavior: 0
```

And example response would be

```yaml
rate_limits:
      # The status of the rate limit.  OK = 0, OVER_LIMIT = 1
      status: 0,
      # The current configured limit
      limit: 10,
      # The number of requests remaining
      remaining: 7,
      # A unix timestamp in milliseconds of when the bucket will reset, or if 
      # OVER_LIMIT is set it is the time at which the rate limit will no 
      # longer return OVER_LIMIT.
      reset_time: 1551309219226,
      # Additional metadata about the request the client might find useful
      metadata:
        # This is the name of the coordinator that rate limited this request
        "owner": "api-n03.staging.us-east-1.mailgun.org:9041"
```

#### Rate limit Algorithm
Gubernator currently supports 2 rate limit algorithms.

1. **Token Bucket** implementation starts with an empty bucket, then each `Hit`
   adds a token to the bucket until the bucket is full. Once the bucket is
   full, requests will return `OVER_LIMIT` until the `reset_time` is reached at
   which point the bucket is emptied and requests will return `UNDER_LIMIT`.
   This algorithm is useful for enforcing very bursty limits. (IE: Applications
   where a single request can add more than 1 `hit` to the bucket; or non network
   based queuing systems.) The downside to this implementation is that once you
   have hit the limit no more requests are allowed until the configured rate
   limit duration resets the bucket to zero.

2. [Leaky Bucket](https://en.wikipedia.org/wiki/Leaky_bucket) is implemented
   similarly to **Token Bucket** where `OVER_LIMIT` is returned when the bucket
   is full. However tokens leak from the bucket at a consistent rate which is
   calculated as `duration / limit`. This algorithm is useful for metering, as
   the bucket leaks allowing traffic to continue without the need to wait for
   the configured rate limit duration to reset the bucket to zero.

## Global Limits
Since Gubernator rate limits are hashed and handled by a single peer in the
cluster. Rate limits that apply to every request in a data center would result
in the rate limit request being handled by a single peer for the entirety of
the data center.  For example, consider a rate limit with
`name=requests_per_datacenter` and a `unique_id=us-east-1`. Now imagine that a
request is made to Gubernator with this rate limit for every http request that
enters the `us-east-1` data center. This could be hundreds of thousands,
potentially millions of requests per second that are all hashed and handled by
a single peer in the cluster. Because of this potential scaling issue
Gubernator introduces a configurable `behavior` called `GLOBAL`.

When a rate limit is configured with `behavior=GLOBAL` the rate limit request
that is received from a client will not be forwarded to the owning peer but
will be answered from an internal cache handled by the peer. `Hits` toward the
rate limit will be batched by the receiving peer and sent asynchronously to the
owning peer where the hits will be totaled and `OVER_LIMIT` calculated.  It
is then the responsibility of the owning peer to update each peer in the
cluster with the current status of the rate limit, such that peer internal
caches routinely get updated with the most current rate limit status.

##### Side effects of global behavior
Since `Hits` are batched and forwarded to the owning peer asynchronously, the
immediate response to the client will not include the most accurate `remaining`
counts. As that count will only get updated after the async call to the owner
peer is complete and the owning peer has had time to update all the peers in
the cluster. As a result the use of `GLOBAL` allows for greater scale but at
the cost of consistency. Using `GLOBAL` also  increases the amount of traffic
per rate limit request. `GLOBAL` should only be used for extremely high volume
rate limits that don't scale well with the traditional non `GLOBAL` behavior.

## Performance
TODO: Show some performance metrics of gubernator running in production

## API
All methods are accessed via GRPC but are also exposed via HTTP using the
[GRPC Gateway](https://github.com/grpc-ecosystem/grpc-gateway)

#### Health Check
Health check returns `unhealthy` in the event a peer is reported by etcd as `up` but the server
instance is unable to contact the peer via it's advertised address.

###### GRPC
```grpc
rpc HealthCheck (HealthCheckReq) returns (HealthCheckResp)
```

###### HTTP
```
GET /v1/HealthCheck
```

Example response:

```json
{
  "status": "healthy",
  "peer_count": 3
}
```

#### Get Rate Limit
Rate limits can be applied or retrieved using this interface. If the client
makes a request to the server with `hits: 0` then current state of the rate 
limit is retrieved but not incremented.

###### GRPC
```grpc
rpc GetRateLimits (GetRateLimitsReq) returns (GetRateLimitsResp)
```

###### HTTP
```
POST /v1/GetRateLimits
```

Example Payload
```json
{
    "requests":[
        {
            "name": "requests_per_sec",
            "unique_key": "account.id=1234",
            "hits": 1,
            "duration": 60000,
            "limit": 10
        }
    ]
}
```

Example response:

```json
{
  "responses":[
    {
      "status": 0,
      "limit": "10",
      "remaining": "7",
      "reset_time": "1551309219226",
    }
  ]
}
```


## Installation
TODO: Show how to run gubernator in a docker container with just environs


## Development with Docker Compose
Gubernator uses etcd to keep track of all it's peers. This peer list is
used by the consistent hash to calculate which peer is the coordinator 
for a rate limit, the docker compose file starts a single instance of
etcd which is suitable for testing the server locally.

You will need to be on the VPN to pull docker images from the repository.

```bash
# Start the containers
$ docker-compose up -d

# Run gubernator
$ cd golang
$ go run ./cmd/gubernator --config config.yaml
```

### What kind of name is Gubernator?
Gubernator is the [english pronunciation of governor](https://www.google.com/search?q=how+to+say+governor+in+russian&oq=how+to+say+govener+in+russ)
in Russian, also it sounds cool.
