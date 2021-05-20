package main

import (
	"fmt"
	"sync"
	"time"

	deduper "github.com/tonalfitness/deduper/deduper_go1"
)

// Simple request type; typically this would be a wrapper around something like an http.Request
// Implements the Request interface
type helloRequest string

// Key should produce a unique request identifier
// All concurrent requests for the same Key will get the same value from Get
func (hr helloRequest) Key() interface{} {
	return hr
}

// Payload is optional, but used in this case
func (hr helloRequest) Payload() interface{} {
	return string(hr)
}

func main() {
	// will be run by the internal worker pool as necessary
	workerFunc := func(req deduper.Request) (interface{}, error) {
		// Do some slow work
		fmt.Println("Called the workerfunc")
		time.Sleep(100 * time.Millisecond)
		// not strictly necessary for this trivial example to do the type assertion
		if payload, ok := req.Payload().(string); ok {
			return fmt.Sprintf("Hello, %v", payload), nil
		}
		return nil, fmt.Errorf("Value not expected string %T", req.Payload())
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
			// not strictly necessary for this trivial example to do the type assertion
			if valStr, ok := val.(string); ok {
				fmt.Println(valStr)
			}
			wg.Done()
		}()
	}
	// start all the requests at once
	close(startChan)

	wg.Wait()
}
