.PHONY: release docker proto certs
.DEFAULT_GOAL := release

VERSION=$(shell cat version)

LDFLAGS="-X main.Version=$(VERSION)"

test:
	go test ./... -v -race -p=1 -count=1

docker:
	docker build --build-arg VERSION=$(VERSION) -t thrawn01/gubernator:$(VERSION) .
	docker tag thrawn01/gubernator:$(VERSION) thrawn01/gubernator:latest

release:
	GOOS=darwin GOARCH=amd64 go build -ldflags $(LDFLAGS) -o gubernator.darwin ./cmd/gubernator/main.go
	GOOS=linux GOARCH=amd64 go build -ldflags $(LDFLAGS) -o gubernator.linux ./cmd/gubernator/main.go

proto:
	scripts/proto.sh

certs:
	rm certs/*.key certs/*.srl certs/*.csr certs/*.pem
	openssl genrsa -out certs/ca.key 4096
	openssl req -new -x509 -key certs/ca.key -sha256 -subj "/C=US/ST=TX/O=Mailgun Technologies, Inc." -days 3650 -out certs/ca.cert
	openssl genrsa -out certs/gubernator.key 4096
	openssl req -new -key certs/gubernator.key -out certs/gubernator.csr -config certs/gubernator.conf
	openssl x509 -req -in certs/gubernator.csr -CA certs/ca.cert -CAkey certs/ca.key -CAcreateserial -out certs/gubernator.pem -days 3650 -sha256 -extfile certs/gubernator.conf -extensions req_ext
	# Client Auth
	openssl req -new -x509 -days 3650 -keyout certs/client-auth-ca.key -out certs/client-auth-ca.pem -subj "/C=TX/ST=TX/O=Mailgun Technologies, Inc./CN=mailgun.com/emailAddress=admin@mailgun.com" -passout pass:test
	openssl genrsa -out certs/client-auth.key 2048
	openssl req -sha1 -key certs/client-auth.key -new -out certs/client-auth.req -subj "/C=US/ST=TX/O=Mailgun Technologies, Inc./CN=client.com/emailAddress=admin@mailgun.com"
	openssl x509 -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -passin pass:test -out certs/client-auth.pem
	openssl x509 -extfile certs/client-auth.conf -extensions ssl_client -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -passin pass:test -out certs/client-auth.pem

