package main

import (
	"encoding/json"
	"fmt"
	guber "github.com/mailgun/gubernator/v2"
	"io"
	"net/http"
	"os"
)

func main() {
	url := os.Getenv("GUBER_HTTP_ADDRESS")
	if url == "" {
		url = "localhost:80"
	}
	resp, err := http.DefaultClient.Get(fmt.Sprintf("http://%s/v1/HealthCheck", url))
	if err != nil {
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		os.Exit(1)
	}

	var hc guber.HealthCheckResp
	json.Unmarshal(body, &hc)
	if hc.Status != "healthy" {
		os.Exit(1)
	}
}
