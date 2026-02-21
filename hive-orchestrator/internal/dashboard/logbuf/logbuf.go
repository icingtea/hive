// Package logbuf provides a thread-safe, per-deployment in-memory log ring
// buffer. The orchestrator writes lines; the dashboard SSE handler tails them.
package logbuf

import (
	"sync"
)

const defaultCap = 2000

// Registry maps deployment IDs to their log buffers.
type Registry struct {
	mu   sync.RWMutex
	bufs map[string]*Buffer
}

func NewRegistry() *Registry {
	return &Registry{bufs: make(map[string]*Buffer)}
}

// Get returns the buffer for a deployment, creating it if needed.
func (r *Registry) Get(deploymentID string) *Buffer {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.bufs[deploymentID]
	if !ok {
		b = newBuffer(defaultCap)
		r.bufs[deploymentID] = b
	}
	return b
}

// Delete removes the buffer (called after deployment is deleted from DB).
func (r *Registry) Delete(deploymentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.bufs[deploymentID]; ok {
		b.close()
		delete(r.bufs, deploymentID)
	}
}

// Buffer is a bounded ring of log lines with subscriber notifications.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
	head  int // index of oldest entry
	size  int
	subs  []chan struct{}
	done  chan struct{}
}

func newBuffer(cap int) *Buffer {
	return &Buffer{
		lines: make([]string, cap),
		cap:   cap,
		done:  make(chan struct{}),
	}
}

// Write appends a log line and notifies all subscribers.
func (b *Buffer) Write(line string) {
	b.mu.Lock()
	idx := (b.head + b.size) % b.cap
	b.lines[idx] = line
	if b.size < b.cap {
		b.size++
	} else {
		b.head = (b.head + 1) % b.cap // overwrite oldest
	}
	subs := b.subs
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Lines returns all lines starting from offset (for catch-up on subscribe).
func (b *Buffer) Lines(offset int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return nil
	}
	out := make([]string, 0, b.size)
	for i := 0; i < b.size; i++ {
		out = append(out, b.lines[(b.head+i)%b.cap])
	}
	if offset >= len(out) {
		return nil
	}
	return out[offset:]
}

// Len returns the current number of lines stored.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// Subscribe returns a channel that receives a signal when new lines arrive,
// and a cancel function. The channel is buffered to avoid blocking the writer.
func (b *Buffer) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		for i, s := range b.subs {
			if s == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Done returns a channel closed when the buffer is closed (deployment ended).
func (b *Buffer) Done() <-chan struct{} { return b.done }

// Close signals that no more lines will be written (deployment finished).
func (b *Buffer) Close() { b.close() }
func (b *Buffer) close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}
