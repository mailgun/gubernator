## Demo Service
Example Mailgun application using eventbus, metrics and ELK logging

### How to Run
This demo app is designed to be run in staging or from you local
dev environment while connected to staging via VPN.

To run in staging the following environment variables must be set
```
export ETCD3_ENDPOINT=etcdv3-n01.staging.us-east-1.definbox.com:2379
export ETCD3_CA=/etc/mailgun/ssl/localhost/etcd-ca.pem
export ETCD3_USER=service
export ETCD3_PASSWORD=<password>
export MG_ENV=staging
```

The service config should be set in etcd at `/mailgun/configs/staging/demo-service`.

Note:

 * The configuration format is JSON.

 * Special keys like `_INCLUDES` and `_SECRETS` are *not* handled by
 `mailgun/service`, it's a job of deployment machinery in staging and
 production environments.


Here's a sample configuration:

```
{
  "legacy_api_host": "mailgun.net",
  "protected_api_host": "localhost",
  "protected_api_url": "http://localhost:9001",
  "public_api_host": "api.mailgun.net",
  "public_api_url": "https://api.mailgun.net",
  "vulcand_namespace": "/vulcand/",
  "foo-response": "Thanks for your submission!",
  "base_api_port": 8080,
  "grpc_port": 8081,
  "metrics": {
    "host": "graphite-relay-n01.prod.us-east-1.postgun.com",
    "period": "1s",
    "port": 8125
  },
  "logging": [
    {
      "name": "syslog",
      "severity": "info"
    },
    {
      "name": "kafka",
      "severity": "info",
      "topic": "udplog",
      "nodes": [
        "kafka-n01.staging.us-east-1.definbox.com:9092",
        "kafka-n02.staging.us-east-1.definbox.com:9092",
        "kafka-n03.staging.us-east-1.definbox.com:9092"
      ]
    }
  ]
}
```

Add the following snippet to the `logging` array for debugging:
```
     {
        "name": "console",
        "severity": "debug"
     }
```

## Features
* Setting `MG_ENV` environment variable will set `Meta.Env`
* Setting `MG_INSTANCE_ID` environment variable will set `Meta.InstanceID`
* Setting `MG_HTTP_PORT` environment variable will set `BasicCfg().HTTPPort`
* Setting `MG_GRPC_PORT` environment variable will set `BasicCfg().GRPCPort`

## TODO
* Provide a default mapping if none exists in config.yaml or MG_ENV = "dev"
* Read from a config file if `Meta.Env` contains a file like `./my-local-config.yaml`
* Allow the framework user to provide a Health call back function which will respond to `/_health` requests
* Provide `make dev run`
* Perhaps some docker-compose file to setup etcd and kafka-pixy locally?
