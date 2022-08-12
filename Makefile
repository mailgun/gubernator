.DEFAULT_GOAL := release
VERSION=$(shell cat version)
LDFLAGS="-X main.Version=$(VERSION)"

.PHONY: lint
lint:
	go vet ./...

.PHONY: test
test:
	(go test -v -race -p=1 -count=1 -coverprofile coverage.out ./...; ret=$$?; \
		go tool cover -func coverage.out; \
		go tool cover -html coverage.out -o coverage.html; \
		exit $$ret)

.PHONY: docker
docker:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/mailgun/gubernator:$(VERSION) .
	docker tag ghcr.io/mailgun/gubernator:$(VERSION) ghcr.io/mailgun/gubernator:latest

.PHONY: release
release:
	go build -v -ldflags $(LDFLAGS) -o gubernator ./cmd/gubernator/main.go

.PHONY: clean
clean:
	rm -f gubernator gubernator-cli

.PHONY: proto
proto:
	scripts/proto.sh

.PHONY: certs
certs:
	rm certs/*.key || rm certs/*.srl || rm certs/*.csr || rm certs/*.pem || rm certs/*.cert || true
	openssl genrsa -out certs/ca.key 4096
	openssl req -new -x509 -key certs/ca.key -sha256 -subj "/C=US/ST=TX/O=Mailgun Technologies, Inc." -days 3650 -out certs/ca.cert
	openssl genrsa -out certs/gubernator.key 4096
	openssl req -new -key certs/gubernator.key -out certs/gubernator.csr -config certs/gubernator.conf
	openssl x509 -req -in certs/gubernator.csr -CA certs/ca.cert -CAkey certs/ca.key -set_serial 1 -out certs/gubernator.pem -days 3650 -sha256 -extfile certs/gubernator.conf -extensions req_ext
	openssl genrsa -out certs/gubernator_no_ip_san.key 4096
	openssl req -new -key certs/gubernator_no_ip_san.key -out certs/gubernator_no_ip_san.csr -config certs/gubernator_no_ip_san.conf
	openssl x509 -req -in certs/gubernator_no_ip_san.csr -CA certs/ca.cert -CAkey certs/ca.key -set_serial 2 -out certs/gubernator_no_ip_san.pem -days 3650 -sha256 -extfile certs/gubernator_no_ip_san.conf -extensions req_ext
	# Client Auth
	openssl req -new -x509 -days 3650 -keyout certs/client-auth-ca.key -out certs/client-auth-ca.pem -subj "/C=TX/ST=TX/O=Mailgun Technologies, Inc./CN=mailgun.com/emailAddress=admin@mailgun.com" -passout pass:test
	openssl genrsa -out certs/client-auth.key 2048
	openssl req -sha1 -key certs/client-auth.key -new -out certs/client-auth.req -subj "/C=US/ST=TX/O=Mailgun Technologies, Inc./CN=client.com/emailAddress=admin@mailgun.com"
	openssl x509 -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -set_serial 3 -passin pass:test -out certs/client-auth.pem
	openssl x509 -extfile certs/client-auth.conf -extensions ssl_client -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -set_serial 4 -passin pass:test -out certs/client-auth.pem
