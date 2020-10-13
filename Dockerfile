# Build image
FROM golang:1.15.1 as build

WORKDIR /go/src

# This should create cached layer of our dependencies for subsequent builds to use
COPY go.mod /go/src
COPY go.sum /go/src
RUN go mod download

# Copy the local package files to the container
ADD . /go/src
ENV VERSION=dev-build

# Build the bot inside the container
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo \
    -ldflags "-w -s -X main.Version=${VERSION}" -o /gubernator /go/src/cmd/gubernator/main.go /go/src/cmd/gubernator/config.go

# Create our deploy image
FROM scratch

# Certs for ssl
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy our static executable.
COPY --from=build /gubernator /gubernator

# Run the server
ENTRYPOINT ["/gubernator"]

EXPOSE 80
EXPOSE 81
EXPOSE 7946
