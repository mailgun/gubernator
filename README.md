#### Docker Compose
You will need to be on the VPN to pull docker images from the repository.

```bash
# Start the containers
$ docker-compose up -d

# Run radar to create the configs in etcd (https://github.com/mailgun/radar)
push_configs --etcd-endpoint localhost:2379 --env-names test,dev

# Run gubernator
export ETCD3_ENDPOINT=localhost:2379
export MG_ENV=dev
$ gubernator --config config.yaml
```