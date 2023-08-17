/*
Copyright 2018-2023 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	guber "github.com/mailgun/gubernator/v2"
)

func main() {
	url := os.Getenv("GUBER_HTTP_ADDRESS")
	if url == "" {
		url = "localhost:80"
	}
	resp, err := http.DefaultClient.Get(fmt.Sprintf("http://%s/v1/HealthCheck", url))
	if err != nil {
		panic(err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}

	var hc guber.HealthCheckResp
	if err := json.Unmarshal(body, &hc); err != nil {
		panic(err)
	}
	if hc.Status != "healthy" {
		os.Exit(2)
	}
}
