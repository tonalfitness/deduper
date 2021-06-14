package deduper

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type treq int

func (tr treq) Key() interface{} {
	return int(tr)
}
func (tr treq) Payload() interface{} {
	return fmt.Sprintf("hello %d", tr)
}

type worker struct {
	counter int32
}

func (w *worker) TestWorkerFunc(req Request) (interface{}, error) {
	atomic.AddInt32(&w.counter, 1)
	time.Sleep(100 * time.Millisecond)
	return req.Key().(int), nil
}

func TestDedupe(t *testing.T) {
	w := &worker{}
	dd := NewDeduper(3, w.TestWorkerFunc)

	r := []treq{7, 14}

	startChan := make(chan struct{})
	for i := 0; i < 10; i++ {
		req := r[i%2]
		go func(req treq) {
			<-startChan
			v, _ := dd.Get(req)
			if v != int(req) {
				t.Errorf("got value: %v expected 7", v)
			}
		}(req)
	}
	close(startChan)
	v, _ := dd.Get(r[0])
	if vint, ok := v.(int); ok {
		if vint+3 != 10 {
			t.Errorf("got value: %v expected 7", v)
		}
	}
	if w.counter != 2 {
		t.Errorf("didn't dedupe: %d", w.counter)
	}
	dd.Shutdown()
}

func TestUniq(t *testing.T) {
	w := &worker{}
	dd := NewDeduper(20, w.TestWorkerFunc)

	uniqueRequests := 100

	startChan := make(chan struct{})
	wg := &sync.WaitGroup{}
	for i := 0; i < uniqueRequests; i++ {
		req := treq(i)
		wg.Add(1)
		go func(req treq, wg *sync.WaitGroup) {
			<-startChan
			v, _ := dd.Get(req)
			if v != int(req) {
				t.Errorf("got value: %v expected %v", v, int(req))
			}
			wg.Done()
		}(req, wg)
	}
	close(startChan)
	wg.Wait()

	if w.counter != int32(uniqueRequests) {
		t.Errorf("didn't dedupe: %d", w.counter)
	}
	dd.Shutdown()
}

type delayworker struct {
	counter   int32
	startChan chan struct{}
}

func (w *delayworker) TestWorkerFunc(req Request) (interface{}, error) {
	<-w.startChan
	atomic.AddInt32(&w.counter, 1)
	time.Sleep(100 * time.Millisecond)
	return req.Key().(int), nil
}

func TestShutdown(t *testing.T) {
	w := &delayworker{
		counter:   0,
		startChan: make(chan struct{}),
	}
	dd := NewDeduper(5, w.TestWorkerFunc)

	uniqueRequests := 1
	requestCount := 50

	startChan := make(chan struct{})
	wg := &sync.WaitGroup{}
	wg.Add(requestCount)
	for i := 0; i < requestCount; i++ {
		req := treq(7)
		go func(req treq, wg *sync.WaitGroup) {
			<-startChan
			v, _ := dd.Get(req)
			if v != int(req) {
				t.Errorf("got value: %v expected %v", v, int(req))
			}
			wg.Done()
		}(req, wg)
	}
	// start the requests
	close(startChan)
	// wait for them all to queue
	time.Sleep(100 * time.Millisecond)
	// shutdown
	dd.Shutdown()
	// unblock the workers
	close(w.startChan)
	wg.Wait()

	// wait for the shutdown
	time.Sleep(100 * time.Millisecond)

	if w.counter != int32(uniqueRequests) {
		t.Errorf("didn't dedupe: %d", w.counter)
	}
}
