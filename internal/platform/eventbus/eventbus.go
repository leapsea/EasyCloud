// Package eventbus is a tiny in-process fan-out bus. Publish never blocks.
package eventbus

import "sync"

type Bus[T any] struct {
	mu   sync.RWMutex
	subs map[int]chan T
	next int
}

func New[T any]() *Bus[T] { return &Bus[T]{subs: map[int]chan T{}} }

// Subscribe returns a buffered channel and a cancel func. Slow subscribers drop events.
func (b *Bus[T]) Subscribe(buf int) (<-chan T, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan T, buf)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
	}
}

func (b *Bus[T]) Publish(v T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- v:
		default:
		}
	}
}
