package azem

import (
	"errors"
	"sync"
)

type EventEnvelope struct {
	Event Event
	Err   error
}

type Subscription struct {
	C       <-chan EventEnvelope
	runtime *Runtime
	id      uint64
	once    sync.Once
}

type subscriptionState struct {
	channel chan EventEnvelope
	closed  bool
}

func (runtime *Runtime) Subscribe(buffer int) (*Subscription, error) {
	if err := runtime.ensureOpen(); err != nil {
		return nil, err
	}
	if buffer <= 0 {
		buffer = 256
	}
	if buffer < 16 || buffer > 4096 {
		return nil, errors.New("azem: subscription buffer must be between 16 and 4096")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return nil, errors.New("azem: runtime is closed")
	}
	runtime.nextID++
	id := runtime.nextID
	state := &subscriptionState{channel: make(chan EventEnvelope, buffer)}
	runtime.subs[id] = state
	return &Subscription{C: state.channel, runtime: runtime, id: id}, nil
}

func (subscription *Subscription) Close() {
	if subscription == nil || subscription.runtime == nil {
		return
	}
	subscription.once.Do(func() {
		runtime := subscription.runtime
		runtime.mu.Lock()
		if state := runtime.subs[subscription.id]; state != nil {
			state.close(EventEnvelope{})
			delete(runtime.subs, subscription.id)
		}
		runtime.mu.Unlock()
	})
}

func (state *subscriptionState) deliver(event EventEnvelope) bool {
	if state == nil || state.closed {
		return false
	}
	select {
	case state.channel <- event:
		return true
	default:
		return false
	}
}

func (state *subscriptionState) close(final EventEnvelope) {
	if state == nil || state.closed {
		return
	}
	state.closed = true
	if final.Err != nil {
		select {
		case state.channel <- final:
		default:
			select {
			case <-state.channel:
			default:
			}
			select {
			case state.channel <- final:
			default:
			}
		}
	}
	close(state.channel)
}
