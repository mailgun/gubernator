# Gubernator

Gubernator is a distributed, high performance, cloud native and stateless rate
limiting service.


#### Features of Gubernator
* Gubernator evenly distributes rate limit requests across the entire cluster,
  which means you can scale the system by simply adding more nodes. 
* Gubernator doesn’t rely on external caches like memcache or redis, as such
  there is no deployment synchronization with a dependant service. This makes
  dynamically growing or shrinking the cluster in an orchestration system like
  kubernetes or nomad trivial.
* Gubernator holds no state on disk, It’s configuration is passed to it by the
  client on a per-request basis.
* Gubernator provides both GRPC and HTTP access to it’s API.
* Can be run as a sidecar to services that need rate limiting or as a separate service.
* Can be used as a library to implement a domain specific rate limiting service.
* Supports optional eventually consistent rate limit distribution for extremely
  high throughput environments. (See GLOBAL behavior [architecture.md](/architecture.md))
* Gubernator is the english pronunciation of governor in Russian, also it sounds cool.

### Stateless configuration
Gubernator is stateless in that it doesn’t require disk space to operate. No
configuration or cache data is ever synced to disk. This is because every
request to gubernator includes the config for the rate limit. At first you
might think this an unnecessary overhead to each request. However, In reality a
rate limit config is made up of only 4, 64bit integers.

An example rate limit request sent via GRPC might look like the following
```yaml
rate_limits:
    # Scopes the request to a specific rate limit
  - name: requests_per_sec
    # A unique_key that identifies this instance of a rate limit request
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

An example response would be

```yaml
rate_limits:
    # The status of the rate limit.  OK = 0, OVER_LIMIT = 1
  - status: 0,
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

### Rate limit Algorithm
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

### Performance
In our production environment, for every request to our API we send 2 rate
limit requests to gubernator for rate limit evaluation, one to rate the HTTP
request and the other is to rate the number of recipients a user can send an
email too within the specific duration. Under this setup a single gubernator
node fields over 2,000 requests a second with most batched responses returned
in under 1 millisecond.

![requests graph](/images/requests-graph.png)

Peer requests forwarded to owning nodes typically respond in under 30 microseconds. 

![peer requests graph](/images/peer-requests-graph.png)

NOTE The above graphs only report the slowest request within the 1 second sample time.
 So you are seeing the slowest requests that gubernator fields to clients.

Gubernator allows users to choose non-batching behavior which would further
reduce latency for client rate limit requests. However because of throughput
requirements our production environment uses Behaviour=BATCHING with the
default 500 microsecond window. In production we have observed batch sizes of
1,000 during peak API usage. Other users who don’t have the same high traffic
demands could disable batching and would see lower latencies but at the cost of
throughput.

### API
All methods are accessed via GRPC but are also exposed via HTTP using the
[GRPC Gateway](https://github.com/grpc-ecosystem/grpc-gateway)

#### Health Check
Health check returns `unhealthy` in the event a peer is reported by etcd or kubernetes
 as `up` but the server instance is unable to contact that peer via it's advertised address.

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

### Deployment
NOTE: Gubernator uses etcd or kubernetes to discover peers and establish a cluster. If you
don't have either, the docker-compose method is the simplest way to try gubernator out.

##### Docker with existing etcd cluster
```bash
$ docker run -p 8081:81 -p 8080:80 -e GUBER_ETCD_ENDPOINTS=etcd1:2379,etcd2:2379 \
   thrawn01/gubernator:latest 
   
# Hit the API at localhost:8080 (GRPC is at 8081)
$ curl http://localhost:8080/v1/HealthCheck
```

##### Docker compose
The docker compose file includes a local etcd server and 2 gubernator instances
```bash
# Download the docker-compose file
$ curl -O https://raw.githubusercontent.com/mailgun/gubernator/master/docker-compose.yaml

# Edit the compose file to change the environment config variables
$ vi docker-compose.yaml

# Run the docker container
$ docker-compose up -d

# Hit the API at localhost:8080 (GRPC is at 8081)
$ curl http://localhost:8080/v1/HealthCheck
```

##### Kubernetes
```bash
# Download the kubernetes deployment spec
$ curl -O https://raw.githubusercontent.com/mailgun/gubernator/master/k8s-deployment.yaml

# Edit the deployment file to change the environment config variables
$ vi k8s-deployment.yaml

# Create the deployment (includes headless service spec)
$ kubectl create -f k8s-deployment.yaml
```

### Configuration
Gubernator is configured via environment variables with an optional `--config` flag
which takes a file of key/values and places them into the local environment before startup.

See the `example.conf` for all available config options and their descriptions.


### Architecture
See [architecture.md](/architecture.md) for a full discription of the architecture and the inner 
workings of gubernator.



