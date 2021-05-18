# Deduper
This project was written to try out Go2 generics.
Deduper transparently deduplicates long running or expensive requests through its Get method.
It uses a Worker pool to execute a given function only once to get a result for many possible callers.
That result is passed to all callers.

There is a package with similar functionality: golang.org/x/sync/singleflight. This deduplicates
requests with the same key, but the execution is unbounded for different keys. This implementation
has a fixed worker pool so requests will queue until the workers are free. 

## Usage
This is a go2 project so you need to use go2go setup:
https://blog.golang.org/generics-next-step
This probably will change fast... this is written in May 2021 using go2go (around the 1.16.3 timeframe).

For the full code, see example/main.go. It runs using `go tool go2go run main.go`

```
// Simple request type; typically this would be a wrapper around something like an http.Request
// Implements the Request[string] interface
type helloRequest string

// ID should produce a unique request identifier
// All concurrent requests for the same ID will get the same value from Get
func (hr helloRequest) ID() interface{} {
	return hr
}

// Payload is optional, but used in this case
func (hr helloRequest) Payload() string {
	return string(hr)
}

// will be run by the internal worker pool as necessary
workerFunc := func(req deduper.Request[string]) (string, error) {
    // Do some slow work
    fmt.Println("Called the workerfunc")
    time.Sleep(100 * time.Millisecond)
    return fmt.Sprintf("Hello, %v", req.Payload()), nil
}

dd := deduper.NewDeduper(3, workerFunc)

wg := &sync.WaitGroup{}

// kick off 5 concurrent requests
startChan := make(chan struct{})
for i := 0; i < 5; i++ {
    wg.Add(1)
    go func() {
        <-startChan
        // Ask the deduper for the same request (as specified by the ID method)
        val, _ := dd.Get(helloRequest("world!"))
        fmt.Println(val)
        wg.Done()
    }()
}
// start all the requests at once
close(startChan)

wg.Wait()

```

Output, one call to the worker, 5 values returned
```
Called the workerfunc
Hello, world!
Hello, world!
Hello, world!
Hello, world!
Hello, world!
```

## Internal Design
Calls to Get are passed via channel into a single goroutine broker along with an individual channel for the return values. 
The broker is responsible for checking if the request is already in-progress or is new. New requests are then queued to a worker
channel. The workers perform the request and send the results back to the broker. The broker then returns all the value to all
concurrent requests.

Shutdown was an afterthought since normally we run services like this for the lifetime of the process. It's a bit tricky to 
implement without dropping requests so I thought it might be useful to someone.

## Generics Usage
In a go1 implementation, Get would return an interface{}. The WorkerFunc would also need to return a generic interface{}. These
were both replaced by the generic type T.

The Payload type U is a bit more contrived. In our actual go1 use case for this pattern we needed a struct payload to make the request.
Having the Payload function return the concrete type would be slightly helpful and get rid of an additional type assertion.

This library was based from some code we wrote to perform slow download and video file processing. The types T and U were
hardcoded in the implementation. I recognized the pattern was a bit tricky to implement and I wanted to see if it could be made
more generic. I think it's a pretty good example of where go2's generics would be helpful.

## Disclaimer
My first experience with go2go. The tooling mostly works like the vanilla go command line tools.

	* Syntax highlighting kinda seems to work if you force it to go
    * VS Code auto-gofmt and completion doesn't work (I had to remember struct fields and run gofmt -w like it's 2015)
	* Many go tools are missing or not working like test coverage
		`coverage: [no statements]`
	* go test didn't detect a deadlock
	* Weird bug using "hash/maphash": caused the example to deadlock when used to calculate the ID()
