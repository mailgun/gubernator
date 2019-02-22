# Errors
Package is a fork of [https://github.com/pkg/errors](https://github.com/pkg/errors) with additional
 functions for improving the relationship between structured logging and error handling in go.
 
## Adding structured context to an error
Wraps the original error while providing structured context data
```go
_, err := ioutil.ReadFile(fileName)
if err != nil {
        return errors.WithContext{"file": fileName}.Wrap(err, "read failed")
}
```

## Retrieving the structured context
Using `errors.WithContext{}` stores the provided context for later retrieval by upstream code or structured logging
systems
```go
// Pass to logrus as structured logging
logrus.WithFields(errors.ToLogrus(err)).Error("open file error")
```
Stack information on the source of the error is also included
```go
context := errors.ToMap(err)
context == map[string]interface{}{
      "file": "my-file.txt",
      "go-func": "loadFile()",
      "go-line": 146,
      "go-file": "with_context_example.go"
}
```

## Conforms to the `Causer` interface
Errors wrapped with `errors.WithContext{}` are compatible with errors wrapped by `github.com/pkg/errors`
```go
switch err := errors.Cause(err).(type) {
case *MyError:
        // handle specifically
default:
        // unknown error
}
```

## Proper Usage
The context wrapped by `errors.WithContext{}` is not intended to be used to by code to decide how an error should be 
handled. It is a convenience where the failure is well known, but the context is dynamic. In other words, you know the
database returned an unrecoverable query error, but creating a new error type with the details of each query
error is overkill **ErrorFetchPage{}, ErrorFetchAll{}, ErrorFetchAuthor{}, etc...**

As an example
```go
func (r *Repository) FetchAuthor(isbn string) (Author, error) {
    // Returns ErrorNotFound{} if not exist
    book, err := r.fetchBook(isbn)
    if err != nil {
        return nil, errors.WithContext{"isbn": isbn}.Wrap(err, "while fetching book")
    }
    // Returns ErrorNotFound{} if not exist
    author, err := r.fetchAuthorByBook(book)
    if err != nil {
        return nil, errors.WithContext{"book": book}.Wrap(err, "while fetching author")
    }
    return author, nil
}
```

You should continue to create and inspect error types
```go
type ErrorAuthorNotFound struct {}

func isNotFound(err error) {
    _, ok := err.(*ErrorAuthorNotFound)
    return ok
}

func main() {
    r := Repository{}
    author, err := r.FetchAuthor("isbn-213f-23422f52356")
    if err != nil {
        // Fetch the original Cause() and determine if the error is recoverable
        if isNotFound(error.Cause(err)) {
                author, err := r.AddBook("isbn-213f-23422f52356", "charles", "darwin")
        }
        if err != nil {
                logrus.WithFields(errors.ToLogrus(err)).Errorf("while fetching author - %s", err)
                os.Exit(1)
        }
    }
    fmt.Printf("Author %+v\n", author)
}
```

## Context for concrete error types
If the error implements the `errors.HasContext` interface the context can be retrieved
```go
context, ok := err.(errors.HasContext)
if ok {
    fmt.Println(context.Context())
}
```

This makes it easy for error types to provide their context information.
 ```go
type ErrorBookNotFound struct {
    ISBN string
}
// Implements the `HasContext` interface
func (e *ErrorBookNotFound) func Context() map[string]interface{} {
    return map[string]interface{}{
        "isbn": e.ISBN,
    }
 }
```
Now we can create the error and logrus knows how to retrieve the context
 
```go
func (* Repository) FetchBook(isbn string) (*Book, error) {
    var book Book
    err := r.db.Query("SELECT * FROM books WHERE isbn = ?").One(&book)
    if err != nil {
        return nil, ErrorBookNotFound{ISBN: isbn}
    }
}

func main() {
    r := Repository{}
    book, err := r.FetchBook("isbn-213f-23422f52356")
    if err != nil {
        logrus.WithFields(errors.ToLogrus(err)).Errorf("while fetching book - %s", err)
        os.Exit(1)
    }
    fmt.Printf("Book %+v\n", book)
}
```


## A Complete example
The following is a complete example using
http://github.com/mailgun/logrus-hooks/kafkahook to marshal the context into ES
fields.

```go
package main

import (
    "github.com/mailgun/holster/errors"
    "github.com/mailgun/logrus-hooks/kafkahook"
    "github.com/sirupsen/logrus"
    "log"
    "io/ioutil"
)

func OpenWithError(fileName string) error {
    _, err := ioutil.ReadFile(fileName)
    if err != nil {
            // pass the filename up via the error context
            return errors.WithContext{
                "file": fileName,
            }.Wrap(err, "read failed")
    }
    return nil
}

func main() {
    // Init the kafka hook logger
    hook, err := kafkahook.New(kafkahook.Config{
        Endpoints: []string{"kafka-n01", "kafka-n02"},
        Topic:     "udplog",
    })
    if err != nil {
        log.Fatal(err)
    }

    // Add the hook to logrus
    logrus.AddHook(hook)

    // Create an error and log it
    if err := OpenWithError("/tmp/non-existant.file"); err != nil {
        // This log line will show up in ES with the additional fields
        //
        // excText: "read failed"
        // excValue: "read failed: open /tmp/non-existant.file: no such file or directory"
        // excType: "*errors.WithContext"
        // filename: "/src/to/main.go"
        // funcName: "main()"
        // lineno: 25
        // context.file: "/tmp/non-existant.file"
        // context.domain.id: "some-id"
        // context.foo: "bar"
        logrus.WithFields(logrus.Fields{
            "domain.id": "some-id",
            "foo": "bar",
            "err": err,
        }).Error("log messge")
    }
}
```
