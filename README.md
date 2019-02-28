# Gubernator

Gubernator is a rate limiting service which calculates rate limits
via a configurable algorithm.

## Architecture overview

![gubernator arch image](/architecture.png)

Gubernator is designed to run as a cluster of peers which utilize an
in memory cache of all the currently active rate limits, no data is 
ever synced to disk. Since most ingress HTTP rate limit durations are 
held for only a few seconds losing the in memory cache during a reboot 
or scheduled downtime isn't a huge deal. For Gubernator we choose 
performance over accuracy as it's acceptable for a small subset of 
traffic to be allowed to over request for a short period of time. 
Gubernator could be expanded in the future to store rate limits with 
longer durations to disk, but currently this is not supported.

When a rate limit request is made to Gubernator the request is keyed and 
a consistent hashing algorithm is applied to determine which of the 
peers will be the coordinator for the rate limit request. Choosing a
single coordinator for a rate limit make atomic increments of counts very fast 
avoids the complexity and latency involved in distributing counts consistently 
across a cluster of peers. Although simple and performant this design can be 
susceptible a thundering herd of requests since a single coordinator is responsible
for possibly many requests to a rate limit. To combat this the server can take
multiple requests within a specified window and batch the requests into a single
peer request, thus reducing the total number of requests to a single Gubernator 
peer tremendously.

To ensure each peer in the cluster accurately calculates the correct hash
for a rate limit key, the list of peers in the cluster must be distributed
to each peer in the cluster in a timely and consistent manner. Currently
Gubernator uses Etcd to distribute the list of peers, This could be later
expanded to a consul or a custom consistent implementation which would further
simplify deployment.

## Gubernator Operation

Gubernator does not read from a list of pre-configured rate limits. 
Instead each request to a peer includes the rate limit to be applied to the request.
This allows clients who understand their rate limit problem space to create and apply 
new rate limit configurations without the need of an out of band process to configure
the rate limiting service. 

The rate limit configuration is stored with the current rate limit in the local cache of
the coordinator owner. Rate limits and their configuration that are stored in the local 
cache will only exist for the specified duration of the rate limit configuration. After 
the duration time has expired, and if the rate limit was not requested again within the 
duration it is dropped from the cache. Subsequent requests for the same unique_key will 
recreate the config and rate limit in the cache and the cycle will repeat.

An example rate limit request sent via GRPC might look like the following
```yaml
rate_limits:
    # Scopes the unique_key to your application to avoid collisions with 
    # other applications that might also use the same unique_key
    namespace: my-app
    # A unique_key that identifies this rate limit request
    unique_key: account_id=24b00c590856900ee961b275asdfd|source_ip=172.0.0.1
    # The number of hits we are requesting
    hits: 1
    # The rate limit config to be applied to this rate limit
    rate_limit_config:
      # The total number of requests allowed for this rate limit
      limit: 100
      # The duration of the rate limit in milliseconds
      duration: 1000
      # The algorithm used to calculate the rate limit  
      # 0 = Token Bucket
      # 1 = Leaky Bucket
      algorithm: 0
```

And example response would be

```yaml
rate_limits:
      # The status of the rate limit.  OK = 0, OVER_LIMIT = 1
      status: 0,
      # The current configured limit
      current_limit: 10,
      # The number of requests remaining
      limit_remaining: 7,
      # A unix timestamp in milliseconds of when the bucket will reset, or if 
      # OVER_LIMIT is set it is the time at which the rate limit will no 
      # longer return OVER_LIMIT.
      reset_time: 1551309219226,
      # Additional metadata about the request the client might find useful
      metadata:
        # This is the name of the coordinator that rate limited this request
        "owner": "api-n03.staging.us-east-1.definbox.com:9041"
```

#### Rate limit Algorithm
Gubernator currently supports 2 rate limit algorithms.

1. [Token Bucket](https://en.wikipedia.org/wiki/Token_bucket) is useful for rate limiting very 
bursty traffic. The downside to token bucket is that once you have hit the limit no more requests 
are allowed until the configured rate limit duration resets the bucket to zero.
2. [Leaky Bucket](https://en.wikipedia.org/wiki/Leaky_bucket) is useful for metering traffic
at a consistent rate, as the bucket leaks at a consistent rate allowing traffic to continue
without the need to wait for the configured rate limit duration to reset the bucket to zero.

## API
All Methods implement in GRPC are exposed to HTTP via the
[GRPC Gateway](https://github.com/grpc-ecosystem/grpc-gateway)

#### Health Check
Health check returns `unhealthy` in the event a peer is reported by etcd as `up` but the server
instance is unable to contact the peer via it's advertised address.

```
GET /v1/HealthCheck
```

Example response:

```javascript
{
  "status": "healthy",
  "peer_count": 3
}
```

#### Get Rate Limit
Rate limits can be applied or retrieved using this interface. If the client
makes a request to the server with `hits: 0` then current state of the rate 
limit is retrieved but not incremented.

```
POST /v1/GetRateLimits
```

Example Payload
```javascript
{
    "rate_limits": [
        {
            "hits": 1,
            "namespace": "my-app",
            "unique_key": "domain.id=1234",
            "rate_limit_config": {
                "duration": 60000,
                "limit": 10
            }
        }
    ]
}
```

Example response:

```javascript
{
  "rate_limits": [
    {
      "current_limit": "10",
      "limit_remaining": "7",
      "reset_time": "1551309219226",
    }
  ]
}
```


## Development with Docker Compose
Gubernator uses etcd to keep track of all it's peers. This peer list is
used by the consistent hash to calculate which peer is the coordinator 
for a rate limit, the docker compose file starts a single instance of
etcd which is suitable for testing the server locally.

You will need to be on the VPN to pull docker images from the repository.

```bash
# Start the containers
$ docker-compose up -d

# Run radar to create the configs in etcd (https://github.com/mailgun/radar)
push_configs --etcd-endpoint localhost:2379 --env-names test,dev

# Run gubernator
export ETCD3_ENDPOINT=localhost:2379
export MG_ENV=dev
$ cd golang
$ go run ./cmd/gubernator --config config.yaml
```

