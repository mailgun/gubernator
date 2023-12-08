# OpenTelemetry Tracing
Gubernator supports [OpenTelemetry](https://opentelemetry.io) for generating
detailed traces of application behavior and sending to [Jaeger
Tracing](https://www.jaegertracing.io/) server.

## Enabling Jaeger Exporter
Jaeger export is enabled by default and sends traces to localhost port
6831/udp.

Configure with environment variables.

```
OTEL_EXPORTER_JAEGER_PROTOCOL=http/thrift.binary
OTEL_EXPORTER_JAEGER_ENDPOINT=http://<jaeger-server>:14268/api/traces
```

See [OpenTelemetry configuration
spec](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/configuration/sdk-environment-variables.md).
for a complete list of available environment variables and `example.conf` for 
possible environment variable examples.

#### Why not set `OTEL_EXPORTER_JAEGER_AGENT_HOST`?
This setting works by exporting UDP datagrams to the remote host, instead of
localhost.  This will work, but runs the risk of data loss.  If a span is
exported with sufficient data, it will exceed the network interface's MTU size
and the datagram will be dropped.  This is only a problem when sending to a
remote host.  The loopback interface MTU is typically much larger and doesn't
encounter this problem.

When exporting via HTTP there is no MTU limit.  This effectively bypasses the
Jaeger Agent and sends directly to Jaeger's collector.

## HoneyComb.io
Use the following to send trace data to honeycomb.io directly. For most production
workloads you should setup [Refinery](https://github.com/honeycombio/refinery)
```
OTEL_TRACES_SAMPLER=always_on
OTEL_EXPORTER_OTLP_PROTOCOL=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=https://api.honeycomb.io:443
OTEL_EXPORTER_OTLP_HEADERS=x-honeycomb-team=<your-api-key>
```

## Sampling
Because Gubernator generates a trace for each request, the tracing volume could
exceed Jaeger's resources.  In production, it is recommended to set
`OTEL_TRACES_SAMPLER=parentbased_traceidratio` [sampler
type](https://opentelemetry.io/docs/concepts/sdk-configuration/general-sdk-configuration/#otel_traces_sampler) and
`OTEL_TRACES_SAMPLER_ARG` to a decimal number between 0 (none) and 1 (all) for
the proportion of traces to be sampled.

```
OTEL_TRACES_SAMPLER=always_on
OTEL_TRACES_SAMPLER_ARG=1.0
```

## Distributed Traces
OpenTelemetry defines capabilities for clients to send trace ids to downstream
services.  That service will link the client span with the server span.  When
the client and server both send traces to the same Jaeger server, the trace
will appear with the two spans linked in the same view.

## HTTP
When making HTTP requests, the tracing ids must be propagated in HTTP headers.  This is
typically done using OpenTelemetry instrumentation, such as [`otelhttp`](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp).

See [OpenTelemetry registry](https://opentelemetry.io/registry/?language=go)
for instrumentation using many other HTTP frameworks.

## Gubernator Standlone
When deployed as a standalone daemon, the Gubernator service will receive
embedded trace ids in requests from the client's `context` object.

For this to work, the client must be configured to embed tracing ids.

## Gubernator Module
When embedded into a dependent codebase as a Go module, most all Gubernator
functions create spans linked to the trace ids embedded into the `context`
object.

Follow the same steps to configure your codebase as the Gubernator standalone,
above.

## Holster Tracing Library
Read more at:
[https://github.com/mailgun/holster/blob/master/tracing/README.md](https://github.com/mailgun/holster/blob/master/tracing/README.md)

