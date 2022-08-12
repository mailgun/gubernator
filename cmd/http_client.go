package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/mailgun/holster/v4/tracing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
)

// func initTracer() *sdktrace.TracerProvider {
// 	// Create stdout exporter to be able to retrieve
// 	// the collected spans.
// 	exporter, err := stdout.New(stdout.WithPrettyPrint())
// 	if err != nil {
// 		log.Fatal(err)
// 	}
//
// 	// For the demonstration, use sdktrace.AlwaysSample sampler to sample all traces.
// 	// In a production application, use sdktrace.ProbabilitySampler with a desired probability.
// 	tp := sdktrace.NewTracerProvider(
// 		sdktrace.WithSampler(sdktrace.AlwaysSample()),
// 		sdktrace.WithBatcher(exporter),
// 	)
// 	otel.SetTracerProvider(tp)
// 	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
// 	stdr.SetVerbosity(5)
// 	return tp
// }

func main() {
	// tp := initTracer()
	// defer func() {
	// 	if err := tp.Shutdown(context.Background()); err != nil {
	// 		log.Printf("Error shutting down tracer provider: %v", err)
	// 	}
	// }()

	// Initialize tracing.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("Gubernator:http_client"),
		),
	)
	if err != nil {
		logrus.WithError(err).Fatal("Error in resource.Merge")
	}

	ctx := context.Background()
	err = tracing.InitTracing(ctx, "http_client", tracing.WithResource(res))
	defer func() {
		tracing.CloseTracing(context.Background())
	}()

	url := "http://localhost:9980/v1/GetRateLimits"
	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	var body []byte

	err = tracing.NamedScope(ctx, "GetRateLimits", func(ctx context.Context) error {
		content := `
{
    "requests": [
        {
            "name": "Foobar",
            "unique_key": "Foobar",
            "hits": 1,
            "duration": 1000,
            "limit": 100
        }
    ]
}
`
		req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(content))

		fmt.Printf("Sending request...\n")
		res, err := client.Do(req)
		if err != nil {
			panic(err)
		}
		body, err = ioutil.ReadAll(res.Body)
		_ = res.Body.Close()

		return err
	})

	if err != nil {
		panic(err)
	}

	fmt.Printf("Response Received: %s\n\n\n", body)
	fmt.Printf("Waiting for few seconds to export spans ...\n\n")
	// time.Sleep(2 * time.Second)
	fmt.Printf("Inspect traces on stdout\n")
}
