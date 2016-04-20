package statsd

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/atlassian/gostatsd/types"

	"golang.org/x/net/context"
)

type testAggregator struct {
	agrNumber int
	af        *testAggregatorFactory
	types.MetricMap
}

func (a *testAggregator) ReceiveMetric(m *types.Metric, t time.Time) {
	a.af.Mutex.Lock()
	defer a.af.Mutex.Unlock()
	a.af.receiveInvocations[a.agrNumber]++
}

func (a *testAggregator) Flush(f func() time.Time) *types.MetricMap {
	a.af.Mutex.Lock()
	defer a.af.Mutex.Unlock()
	a.af.flushInvocations[a.agrNumber]++
	return nil
}

func (a *testAggregator) Process(f ProcessFunc) {
	a.af.Mutex.Lock()
	defer a.af.Mutex.Unlock()
	a.af.processInvocations[a.agrNumber]++
	f(&a.MetricMap)
}

func (a *testAggregator) Reset(t time.Time) {
	a.af.Mutex.Lock()
	defer a.af.Mutex.Unlock()
	a.af.resetInvocations[a.agrNumber]++
}

type testAggregatorFactory struct {
	sync.Mutex
	receiveInvocations map[int]int
	flushInvocations   map[int]int
	processInvocations map[int]int
	resetInvocations   map[int]int
	numAgrs            int
}

func (af *testAggregatorFactory) Create() Aggregator {
	agrNumber := af.numAgrs
	af.numAgrs++
	af.receiveInvocations[agrNumber] = 0
	af.flushInvocations[agrNumber] = 0
	af.processInvocations[agrNumber] = 0
	af.resetInvocations[agrNumber] = 0

	agr := testAggregator{
		agrNumber: agrNumber,
		af:        af,
	}
	agr.Counters = types.Counters{}
	agr.Timers = types.Timers{}
	agr.Gauges = types.Gauges{}
	agr.Sets = types.Sets{}

	return &agr
}

func newTestFactory() *testAggregatorFactory {
	return &testAggregatorFactory{
		receiveInvocations: make(map[int]int),
		flushInvocations:   make(map[int]int),
		processInvocations: make(map[int]int),
		resetInvocations:   make(map[int]int),
	}
}

func TestNewDispatcherShouldCreateCorrectNumberOfWorkers(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	n := r.Intn(5) + 1
	factory := newTestFactory()
	d := NewDispatcher(n, 1, factory).(*dispatcher)
	if len(d.workers) != n {
		t.Errorf("workers: expected %d, got %d", n, len(d.workers))
	}
	if factory.numAgrs != n {
		t.Errorf("aggregators: expected %d, got %d", n, factory.numAgrs)
	}
}

func TestRunShouldReturnWhenContextCancelled(t *testing.T) {
	d := NewDispatcher(5, 1, newTestFactory())
	ctx, cancelFunc := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelFunc()

	err := d.Run(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected %v, got %v", context.DeadlineExceeded, err)
	}
}

func TestDispatchMetricShouldDistributeMetrics(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	n := r.Intn(5) + 1
	factory := newTestFactory()
	d := NewDispatcher(n, 1000, factory).(*dispatcher)
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	var wgFinish sync.WaitGroup
	wgFinish.Add(1)
	go func() {
		defer wgFinish.Done()
		if err := d.Run(ctx); err != context.Canceled {
			t.Errorf("unexpected exit error: %v", err)
		}
	}()
	numMetrics := r.Intn(1000)
	var wg sync.WaitGroup
	wg.Add(numMetrics)
	for i := 0; i < numMetrics; i++ {
		go func() {
			defer wg.Done()
			m := &types.Metric{
				Type:  types.COUNTER,
				Name:  fmt.Sprintf("counter.metric.%d", r.Int63()),
				Tags:  nil,
				Value: r.Float64(),
			}
			if err := d.DispatchMetric(ctx, m); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()       // Wait for all metrics to be dispatched
	cancelFunc()    // After all metrics have been dispatched, we signal dispatcher to shut down
	wgFinish.Wait() // Wait for dispatcher to shutdown

	receiveInvocations := getTotalInvocations(factory.receiveInvocations)
	if numMetrics != receiveInvocations {
		t.Errorf("expected number of receiver metrics: %d, got %d", numMetrics, receiveInvocations)
	}
}

func getTotalInvocations(inv map[int]int) int {
	var counter int
	for _, i := range inv {
		counter += i
	}
	return counter
}
