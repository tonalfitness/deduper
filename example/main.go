package main

import (
	"github.com/tonalfitness/deduper"
	"fmt"
	"sync"
	"time"
)

// Simple request type; typically this would be a wrapper around something like an http.Request
// Implements the Request[string] interface
type helloRequest string

// Key should produce a unique request identifier
// All concurrent requests for the same Key will get the same value from Get
func (hr helloRequest) Key() interface{} {
	return hr
}

// Payload is optional, but used in this case
func (hr helloRequest) Payload() string {
	return string(hr)
}

func main() {
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
}
