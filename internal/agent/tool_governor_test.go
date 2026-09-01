package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

type dynamicConcurrencyTestDriver struct {
	entered chan string
	release chan struct{}
}

func (*dynamicConcurrencyTestDriver) Definition() tool.Definition {
	return tool.Definition{Name: "dynamic.concurrency", InputSchema: tool.Schema{Type: "object"}, Concurrency: tool.ConcurrencyParallel}
}

func (*dynamicConcurrencyTestDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	var input struct {
		Write bool `json:"write"`
	}
	_ = json.Unmarshal(call.Arguments, &input)
	if input.Write {
		return agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectWrite, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "dynamic-writes"}
	}
	return agentruntime.ToolPolicy{Effect: agentruntime.ToolEffectReadOnly, Concurrency: tool.ConcurrencyParallel}
}

func (driver *dynamicConcurrencyTestDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	select {
	case driver.entered <- call.ID:
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
	select {
	case <-driver.release:
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "done"}, nil
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
}

func TestExecutePolicyCallKeepsReadsParallelAndSerializesDynamicWrites(t *testing.T) {
	service := &Service{}
	driver := &dynamicConcurrencyTestDriver{entered: make(chan string, 4), release: make(chan struct{}, 4)}
	executePair := func(write bool) (<-chan error, <-chan struct{}) {
		t.Helper()
		done := make(chan error, 2)
		attempted := make(chan struct{}, 2)
		arguments, err := json.Marshal(map[string]bool{"write": write})
		if err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		group.Add(2)
		for _, id := range []string{"one", "two"} {
			id := id
			go func() {
				defer group.Done()
				attempted <- struct{}{}
				_, executeErr := service.ExecutePolicyCall(context.Background(), driver, tool.Call{ID: id, Name: "dynamic.concurrency", Arguments: arguments}, nil)
				done <- executeErr
			}()
		}
		go func() {
			group.Wait()
			close(done)
		}()
		return done, attempted
	}

	readDone, readAttempted := executePair(false)
	<-readAttempted
	<-readAttempted
	<-driver.entered
	select {
	case <-driver.entered:
	case <-time.After(time.Second):
		t.Fatal("independent read call was serialized")
	}
	driver.release <- struct{}{}
	driver.release <- struct{}{}
	for err := range readDone {
		if err != nil {
			t.Fatal(err)
		}
	}

	writeDone, writeAttempted := executePair(true)
	<-writeAttempted
	<-writeAttempted
	<-driver.entered
	select {
	case id := <-driver.entered:
		t.Fatalf("dynamic write %q entered before the first write released", id)
	case <-time.After(100 * time.Millisecond):
	}
	driver.release <- struct{}{}
	select {
	case <-driver.entered:
	case <-time.After(time.Second):
		t.Fatal("second dynamic write did not enter after release")
	}
	driver.release <- struct{}{}
	for err := range writeDone {
		if err != nil {
			t.Fatal(err)
		}
	}
}
