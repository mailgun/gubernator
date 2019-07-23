.PHONY: release docker
.DEFAULT_GOAL := release

VERSION=$(shell cat version)

LDFLAGS="-X main.Version=$(VERSION)"

docker:
	docker build --build-arg VERSION=$(VERSION) -t thrawn01/gubernator:$(VERSION) .
	docker tag thrawn01/gubernator:$(VERSION) thrawn01/gubernator:latest

release:
	GOOS=darwin GOARCH=amd64 go build -ldflags $(LDFLAGS) -o gubernator.darwin ./cmd/gubernator/main.go ./cmd/gubernator/config.go
	GOOS=linux GOARCH=amd64 go build -ldflags $(LDFLAGS) -o gubernator.linux ./cmd/gubernator/main.go ./cmd/gubernator/config.go
