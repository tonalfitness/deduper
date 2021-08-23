// A common pattern is for many goroutines to request the same resource at the same time.
// If that resource is expensive or long running to get, it's beneficial to deduplicate
// requests for identical resources. This library uses a fixed size worker pool that's
// managed by a broker which will only use the worker pool if the new request is unique.
package deduper

import "sync"

// Deduper runs a fixed size worker pool that can run long-running
// requests. The Deduper will de-duplicate requests that arrive while
// a current request of the same ID is still in-flight.
// There is no attempt to recover if your WorkerFunc panics, try to avoid that
// or recover internally
type Deduper[T any, U any] struct {
	requestCh    chan *requestWrapper[T, U]
	workerCh     chan Request[U]
	resultCh     chan *resultWrapper[T, U]
	shutdownChan chan struct{}
	shutdownOnce *sync.Once
	wg           *sync.WaitGroup
	workerFunc   WorkerFunc[T, U]
	cache        Cache[T]
}

// NewDeduper creates a new Deduper with specified size worker pool and WorkerFunc
func NewDeduper[T any, U any](workerCount int, worker WorkerFunc[T, U]) *Deduper[T, U] {
	return NewDeduperWithCache(workerCount, worker, nil)
}

// NewDeduperWithCache creates a new Deduper with specified size worker pool and WorkerFunc and a Cache
func NewDeduperWithCache[T any, U any](workerCount int, worker WorkerFunc[T, U], cache Cache[T]) *Deduper[T, U] {
	dd := &Deduper[T, U]{
		requestCh:    make(chan *requestWrapper[T, U], 50),
		workerCh:     make(chan Request[U], 2*workerCount),
		resultCh:     make(chan *resultWrapper[T, U], 2*workerCount),
		shutdownChan: make(chan struct{}),
		shutdownOnce: &sync.Once{},
		wg:           &sync.WaitGroup{},
		workerFunc:   worker,
		cache:        cache,
	}
	go dd.broker()

	dd.wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go dd.worker()
	}
	return dd
}

// Cache is an optional type that allows persistent caching of the
// results. If present, the value will be returned from the cache.
// The cache will be used from a single goroutine so it doesn't need
// to be goroutine safe
type Cache[T any] interface {
	Get(key interface{}) (T, bool)
	Set(key interface{}, value T)
}

// Request is a deduplicatable request which includes a Payload that will be passed to the WorkerFunc
type Request[U any] interface {
	// Key should produce a unique request identifier
	// All concurrent requests for the same Key will get the same value from Get
	Key() interface{}
	// Payload is optional and not directly used by Deduper. It can be anything extra that needs
	// to be passed to the WorkerFunc in order to retrieve the resource (like a URL or http.Request).
	Payload() U
}

// WorkerFunc is a function that performs the long running Request and return type T. The Payload of the Request is type U.
type WorkerFunc[T any, U any] func(req Request[U]) (T, error)

type requestWrapper[T any, U any] struct {
	request  Request[U]
	returnCh chan *resultWrapper[T, U]
}

type resultWrapper[T any, U any] struct {
	request Request[U]
	value   T
	err     error
}

// Get will block until the value of type T is available or there is an error.
func (dd *Deduper[T, U]) Get(m Request[U]) (T, error) {
	returnCh := make(chan *resultWrapper[T, U], 1)
	dd.requestCh <- &requestWrapper[T, U]{m, returnCh}
	rw := <-returnCh
	return rw.value, rw.err
}

// Shutdown kills the workers and the broker. Any in-flight requests will be completed.
// Calling Get after Shutdown will panic (with a write to a closed channel)
func (dd *Deduper[T, U]) Shutdown() {
	dd.shutdownOnce.Do(func() { close(dd.shutdownChan) })
}

func (dd *Deduper[T, U]) broker() {
	savedRequest := make(map[interface{}][]*requestWrapper[T, U])
	shutdown := false
	for !shutdown {
		select {
		case request := <-dd.requestCh:
			dd.queueRequestChan(request, savedRequest)
		case rw := <-dd.resultCh:
			dd.processResult(rw, savedRequest)
		case <-dd.shutdownChan:
			shutdown = true
		}
	}

	dd.cleanup(savedRequest)
}

func (dd *Deduper[T, U]) cleanup(savedRequest map[interface{}][]*requestWrapper[T, U]) {
	// Stop accepting new requests and drain any remaining in the channel
	close(dd.requestCh)
	for request := range dd.requestCh {
		dd.queueRequestChan(request, savedRequest)
	}
	// Close the workerCh and process the results
	close(dd.workerCh)
	wgDoneCh := make(chan struct{})
	go dd.workersDone(wgDoneCh)
	wgDone := false
	for !wgDone {
		select {
		case rw := <-dd.resultCh:
			dd.processResult(rw, savedRequest)
		case <-wgDoneCh:
			wgDone = true
		}
	}
	// not done yet... even though the workers are finished, there still could be unprecessed results left in resultCh
	close(dd.resultCh)
	for rw := range dd.resultCh {
		dd.processResult(rw, savedRequest)
	}
}

func (dd *Deduper[T, U]) workersDone(wgDoneCh chan struct{}) {
	dd.wg.Wait()
	close(wgDoneCh)
}

func (dd *Deduper[T, U]) queueRequestChan(request *requestWrapper[T, U], savedRequest map[interface{}][]*requestWrapper[T, U]) {
	// return fast if found in the cache
	if dd.cache != nil {
		val, cached := dd.cache.Get(request.request.Key())
		if cached {
			request.returnCh <- &resultWrapper[T, U]{request.request, val, nil}
			return
		}
	}

	// if the request is already in-flight, just save for later
	requests, found := savedRequest[request.request.Key()]
	savedRequest[request.request.Key()] = append(requests, request)
	if !found {
		// if the request is not in-flight then send to the worker to get it
		// this loops ensures we don't block on the workerCh send if the
		// channel is full. If we drain the resultCh, then the workerCh
		// will eventually free up
		sent := false
		for !sent {
			select {
			case dd.workerCh <- request.request:
				sent = true
			case rw := <-dd.resultCh:
				dd.processResult(rw, savedRequest)
			}
		}
	}
}

func (dd *Deduper[T, U]) processResult(rw *resultWrapper[T, U], savedRequest map[interface{}][]*requestWrapper[T, U]) {
	if rw.err == nil && dd.cache != nil {
		dd.cache.Set(rw.request.Key(), rw.value)
	}
	requests := savedRequest[rw.request.Key()]
	for _, r := range requests {
		r.returnCh <- rw
	}

	delete(savedRequest, rw.request.Key())
}

func (dd *Deduper[T, U]) worker() {
	defer dd.wg.Done()
	for m := range dd.workerCh {
		// do the work
		value, err := dd.workerFunc(m)
		dd.resultCh <- &resultWrapper[T, U]{m, value, err}
	}
}
