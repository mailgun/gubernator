.PHONY: release docker proto
.DEFAULT_GOAL := release

VERSION=$(shell cat version)

LDFLAGS="-X main.Version=$(VERSION)"

proto:
	scripts/proto.sh

docker:
	docker build --build-arg VERSION=$(VERSION) -t mailgun/gubernator:$(VERSION) .
	docker tag thrawn01/gubernator:$(VERSION) thrawn01/gubernator:latest

