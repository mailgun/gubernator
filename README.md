# Gubernator

Gubernator is a rate limiting service which calculates rate limits
via a configurable algorithm.

## Architecture overview

![gubernator arch image](https://raw.githubusercontent.com/mailgun/gubernator/master/architecture.png)

Gubernator is designed to run as a cluster of peers which utilize an
in memory cache of all the currently active rate limits. Since
most ingress HTTP rate limit durations are held for only a few seconds
losing the in memory cache during a reboot or scheduled downtime isn't
a huge deal. For Gubernator we choose performance over accuracy as it's
acceptable for a small subset of traffic to be allowed to over request
for a short period of time. Gubernator could be expanded to store rate 
limits with longer durations to disk, but currently this is not supported.

When a rate limit request is made to Gubernator the request is keyed and 
a consistent hashing algorithm is applied to determine which of the 
peers will be the coordinator for the rate limit request. Choosing a
single coordinator for a rate limit avoids the complexity and latency
involved in distributing consistency across a cluster of peers. Although 
simple and performant this design can be susceptible a thundering herd 
of requests. To combat this the server can take multiple requests with
in a specified window for a single rate limit and batch the requests into
a single peer request, thus reducing the total number of requests to a 
single Gubernator peer tremendously. 

To ensure each peer in the cluster accurately calculates the correct hash
for a rate limit key, the list of peers in the cluster must be distributed
to each peer in the cluster in a timely and consistent manner. Currently
Gubernator uses Etcd to distribute the list of peers, This could be later
expanded to a consul or a custom consistent implementation which would further
simplify deployment.

## Gubernator Usage

TODO



## API
All Methods implement in GRPC and exposed to HTTP via a GRPC Gateway

#### Health Check
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
```
POST /v1/GetRateLimits
```

Example Payload
```javascript
{
    "rate_limits": [
        {
            "hits": 1,
            "namespace": "ns1",
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
}
```


## Development
Gubernator uses etcd to keep track of all it's peers. This peer list is
used by the consistent hash to calculate which peer is the coordinator 
for a rate limit. 

### Running locally with Docker Compose
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

