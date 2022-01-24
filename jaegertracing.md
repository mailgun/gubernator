# Jaeger Tracing
Gubernator supports [OpenTracing](https://opentracing.io) for generating
detailed traces of server behavior using [Jaeger
Tracing](https://www.jaegertracing.io/) tools.

## Enabling Jaeger
Jaeger is enabled by default and sends traces to localhost port 6831/udp.

Configure with environment variables, such as:

| Name                   | Description |
| ---------------------- | ----------- |
| `JAEGER_SERVICE_NAME`  | Service name. |
| `JAEGER_AGENT_HOST`    | Jaeger server hostname or IP. |
| `JAEGER_SAMPLER_TYPE`  | The sampler type: `remote`, `const`, `probablistic`, or `ratelimiting`. |
| `JAEGER_SAMPLER_PARAM` | The sampler parameter. |
| `JAEGER_DISABLED`      | Set to `true` to disable sending Jaeger traces. |

See also the [full list of variables](https://github.com/jaegertracing/jaeger-client-go#environment-variables).

## Sampling
Because Gubernator generates a trace for each request, it is recommended to use
`probablistic` or `ratelimiting` [sampler
type](https://www.jaegertracing.io/docs/1.30/sampling/) to reduce the volume of
data sent to your Jaeger server.

## Distributed Traces
OpenTracing defines capabilities for clients to send trace ids to downstream
services.  That service will link the client span with the server span.  When
the client and server both send traces to the same Jaeger server, the trace
will appear with the two spans linked in the same view.

See `tracing/tracing.go` for usage examples.

### Gubernator Standlone
When deployed as a standalone daemon, Gubernator's gRPC service will receive
embedded trace ids in requests from the client's `context` object.

For this to work, the client must be configured to embed tracing ids.

#### gRPC
If using Gubernator's Golang gRPC client, the client must be created like so:

```go
    import (
        "github.com/opentracing/opentracing-go"
        otgrpc "github.com/opentracing-contrib/go-grpc"
        "google.golang.org/grpc"
    )

    // ...

    tracer := opentracing.GlobalTracer()
    tracingUnaryInterceptor := otgrpc.OpenTracingClientInterceptor(tracer)
    tracingStreamInterceptor := otgrpc.OpenTracingStreamClientInterceptor(tracer)

    opts := []grpc.DialOption{
        grpc.WithBlock(),
        grpc.WithUnaryInterceptor(tracingUnaryInterceptor),
        grpc.WithStreamInterceptor(tracingStreamInterceptor),
    }

    endpoint := "<your-endpoint>"
    conn, err := grpc.DialContext(ctx, endpoint, opts...)
```

#### HTTP
If using HTTP, the tracing ids must be embedded in HTTP headers.  This is
typically done using Jaeger client functionality.

See: https://medium.com/opentracing/distributed-tracing-in-10-minutes-51b378ee40f1

### Gubernator Module
When embedded into a dependent codebase as a Go module, most all Gubernator
functions create spans linked to the trace ids embedded into the `context`
object.

Follow the same steps to configure your codebase as the Gubernator standalone,
above.
