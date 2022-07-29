# OpenTelemetry Tracing with Jaeger
Gubernator supports [OpenTelemetry](https://opentelemetry.io) for generating
detailed traces of application behavior and sending to [Jaeger
Tracing](https://www.jaegertracing.io/) server.

Read more at:
[https://github.com/mailgun/holster/blob/master/tracing/README.md](https://github.com/mailgun/holster/blob/master/tracing/README.md)

## Enabling Jaeger Exporter
Jaeger export is enabled by default and sends traces to localhost port
6831/udp.

Configure with environment variables.  See [OpenTelemetry configuration
spec](https://github.com/open-telemetry/opentelemetry-go/tree/main/exporters/jaeger).

## Sampling
Because Gubernator generates a trace for each request, the tracing volume could
exceed Jaeger's resources.  In production, it is recommended to set
`OTEL_TRACES_SAMPLER=parentbased_traceidratio` [sampler
type](https://www.jaegertracing.io/docs/1.30/sampling/) and
`OTEL_TRACES_SAMPLER_ARG` to a decimal number between 0 (none) and 1 (all) for
the proportion of traces to be sampled.

## Distributed Traces
OpenTelemetry defines capabilities for clients to send trace ids to downstream
services.  That service will link the client span with the server span.  When
the client and server both send traces to the same Jaeger server, the trace
will appear with the two spans linked in the same view.

When sending gRPC requests to Gubernator, be sure to use the [`otelgrpc`
interceptor](https://github.com/open-telemetry/opentelemetry-go-contrib) to
propagate the client's trace context to the server so it can add spans.

See [gRPC](#gRPC) section and `cmd/gubernator-cli/main.go` for usage examples.

## Exporting to Remote Jaeger Server
Normally, Jaeger all-in-one or a Jaeger Agent is running locally on the server
and listening to port 6831/udp.  Gubernator would talk to Jaeger by exporting
traces to localhost port 6831/udp.

However, in situations where Jaeger cannot be made accessable via localhost, it
is recommended to export traces via HTTP using this configuration:

```
OTEL_EXPORTER_JAEGER_PROTOCOL=http/thrift.binary
OTEL_EXPORTER_JAEGER_ENDPOINT=http://<jaeger-server>:14268/api/traces
```

**Why not set `OTEL_EXPORTER_JAEGER_AGENT_HOST`?**
This setting works by exporting UDP datagrams to the remote host, instead of
localhost.  This will work, but runs the risk of data loss.  If a span is
exported with sufficient data, it will exceed the network interface's MTU size
and the datagram will be dropped.  This is only a problem when sending to a
remote host.  The loopback interface MTU is typically much larger and doesn't
encounter this problem.

When exporting via HTTP there is no MTU limit.  This effectively bypasses the
Jaeger Agent and sends directly to Jaeger's collector.

## gRPC
If using Gubernator's Golang gRPC client, the client must be created like so:

```go
    import (
        "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
        "google.golang.org/grpc"
    )

    // ...

    opts := []grpc.DialOption{
        grpc.WithBlock(),
        grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
        grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
    }

    endpoint := "<your-endpoint-url>"
    conn, err := grpc.DialContext(ctx, endpoint, opts...)
```

## HTTP
If using HTTP, the tracing ids must be propagated in HTTP headers.  This is
typically done using OpenTelemetry instrumentation, such as [`otelhttp`](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp).

See [OpenTelemetry registry](https://opentelemetry.io/registry/?language=go)
for instrumentation using many other HTTP frameworks.

## Gubernator Standlone
When deployed as a standalone daemon, Gubernator's gRPC service will receive
embedded trace ids in requests from the client's `context` object.

For this to work, the client must be configured to embed tracing ids.

## Gubernator Module
When embedded into a dependent codebase as a Go module, most all Gubernator
functions create spans linked to the trace ids embedded into the `context`
object.

Follow the same steps to configure your codebase as the Gubernator standalone,
above.
