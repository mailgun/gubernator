# Prometheus Metrics
Gubernator can be monitored realtime using [Prometheus](https://prometheus.io/) metrics.

## Enabling Metric Collection
Metrics are exposed under two possible deployment scenarios:

1. Gubernator deployed as a standalone daemon.
   * Metrics endpoint published at the HTTP `/metrics` URI.
2. Gubernator embedded as a Go module.
   * The dependant codebase is responsible for publishing the HTTP `/metrics` URI.
   * See `daemon.go` for examples using the `promhttp` module.

Finally, configure a Prometheus job to scrape the server's `/metrics` URI.

## Metrics

| Metric                                 | Type    | Description |
| -------------------------------------- | ------- | ----------- |
| `gubernator_async_durations`           | Summary | The timings of GLOBAL async sends in seconds. |
| `gubernator_asyncrequest_retries`      | Counter | The count of retries occurred in asyncRequests() forwarding a request to another peer. |
| `gubernator_broadcast_durations`       | Summary | The timings of GLOBAL broadcasts to peers in seconds. |
| `gubernator_cache_access_count`        | Counter | The count of LRUCache accesses during rate checks. |
| `gubernator_cache_size`                | Gauge   | The number of items in LRU Cache which holds the rate limits. |
| `gubernator_check_counter`             | Counter | The number of rate limits checked. |
| `gubernator_check_error_counter`       | Counter | The number of errors while checking rate limits. |
| `gubernator_concurrent_checks_counter` | Summary | 99th quantile of concurrent rate checks.  This includes rate checks processed locally and forwarded to other peers. |
| `gubernator_func_duration`             | Summary | The 99th quantile of key function timings in seconds. |
| `gubernator_getratelimit_counter`      | Counter | The count of getRateLimit() calls.  Label \"calltype\" may be \"local\" for calls handled by the same peer, \"forward\" for calls forwarded to another peer, or \"global\" for global rate limits. |
| `gubernator_grpc_request_counts`       | Counter | The count of gRPC requests. |
| `gubernator_grpc_request_duration`     | Summary | The 99th quantile timings of gRPC requests in seconds. |
| `gubernator_over_limit_counter`        | Counter | The number of rate limit checks that are over the limit. |
| `gubernator_pool_queue_length`         | Summary | The 99th quantile of rate check requests queued up in GubernatorPool.  The is the work queue for local rate checks. |
| `gubernator_queue_length`              | Summary | The 99th quantile of rate check requests queued up for batching to other peers by getPeerRateLimitsBatch().  This is the work queue for remote rate checks.  Label "peerAddr" indicates queued requests to that peer. |
