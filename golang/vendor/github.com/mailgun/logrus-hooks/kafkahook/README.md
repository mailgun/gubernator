# Kafka Logrus Hook

A Logrus Hook for sending log info to [Kafka](https://kafka.apache.org)


# Usage
```go
import (
    "github.com/sirupsen/logrus"
    "github.com/mailgun/logrus-hooks/kafkahook"
)

hook, err := kafkahook.New(kafkahook.Config{Endpoints: []string{"localhost:9092"}})
if err != nil {
    panic(err)
}

// Tell logrus about the hook
logrus.AddHook(hook)

// Log a line
logrus.Info("Your mother milk chicken for a living")

// You must close the hook to flush messages before exit
err := hook.Close()
if err != nil {
        panic(err)
}
````

will result in json
```json
{
	"context": null,
	"appname": "logrus-kafkahook.test",
	"hostname": "localhost",
	"logLevel": "INFO",
	"filename": "/mailgun/logrus-hooks/kafkahook/functional_test.go",
	"funcName": "github.com/mailgun/logrus-hooks/kafkahook.(*FunctionalTest).TestUDPHookExported",
	"lineno": 55,
	"message": "Your mother milk chicken for a living",
	"timestamp": 1485482245.473685
}
```
`logrus.WithFields()` places the fields into the `context` object.
```go
log.WithFields(logrus.Fields{
	"domain.id": "282b0862-e425-11e6-a897-600308a97d8c",
	"domain.name": "example.com",
	"bar": "foo",
	"foo.bar": "bar",
}).Error("Your mother milk chicken for a living")
```
will result in json
```json
{
    "context": {
        "domain": {
            "id": "282b0862-e425-11e6-a897-600308a97d8c",
            "name": "example.com"
        },
        "bar": "foo",
        "foo": {
            "bar": "bar"
        }
    },
	"appname": "logrus.test",
	"hostname": "localhost",
	"logLevel": "INFO",
	"filename": "/mailgun/logrus-hooks/kafkahook/functional_test.go",
	"funcName": "github.com/mailgun/logrus-hooks/kafkahook.(*FunctionalTest).TestUDPHookExported",
	"lineno": 75,
	"message": "Your mother milk chicken for a living",
	"timestamp": 1485482245.472957
}
```
`logrus.WithFields()` also understands `http.Request` objects
```go
func (c *Controller) Get(w http.ResponseWriter, r *http.Request) (interface{}, error) {
        log.WithFields{logrus.Fields{ "http": r, }).Info("Get Called")
}
```
will result in json
```json
{
    "context": {
        "http": {
            "headers": {
                "User-Agent": ["test-agent"]
            },
            "ip": "192.0.2.1:1234",
            "method": "POST",
            "params": {
                "param1": ["1"],
                "param2": ["2"]
            },
            "size": 4,
            "url": "http://example.com?param1=1&param2=2",
            "useragent": "test-agent"
        }
    },
	"appname": "logrus.test",
	"hostname": "localhost",
	"logLevel": "INFO",
	"filename": "/mailgun/logrus-hooks/kafkahook/functional_test.go",
	"funcName": "github.com/mailgun/logrus-hooks/kafkahook.(*FunctionalTest).TestUDPHookExported",
	"lineno": 175,
	"message": "Get Called",
	"timestamp": 1485482245.472957
}
```

