package internal

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type spanEntry struct {
	span      trace.Span
	createdAt time.Time
}

// Store holds in-flight spans keyed by HAProxy unique-id.
// A background goroutine evicts entries that never received a response
// (e.g. dropped connections) to prevent memory leaks.
type Store struct {
	entries sync.Map
	ttl     time.Duration
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewStore(ttl time.Duration) *Store {
	s := &Store{
		ttl:    ttl,
		stopCh: make(chan struct{}),
	}
	s.wg.Add(1)
	go s.cleanup()
	return s
}

func (s *Store) Set(id string, span trace.Span) {
	s.entries.Store(id, &spanEntry{
		span:      span,
		createdAt: time.Now(),
	})
}

func (s *Store) Get(id string) (trace.Span, bool) {
	v, ok := s.entries.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*spanEntry).span, true
}

func (s *Store) Delete(id string) {
	s.entries.Delete(id)
}

// Close stops the cleanup goroutine and ends all remaining in-flight spans.
// Must be called before TracerProvider.Shutdown to ensure all spans are flushed.
func (s *Store) Close() {
	close(s.stopCh)
	s.wg.Wait() // ensure cleanup goroutine has exited before draining
	s.entries.Range(func(k, v any) bool {
		s.evict(k, v, errors.New("agent shutdown: response never received"))
		return true
	})
}

func (s *Store) cleanup() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			s.entries.Range(func(k, v any) bool {
				e := v.(*spanEntry)
				if now.Sub(e.createdAt) > s.ttl {
					s.evict(k, v, fmt.Errorf("span TTL exceeded: no response received after %s", s.ttl))
				}
				return true
			})
		case <-s.stopCh:
			return
		}
	}
}

func (s *Store) evict(k, v any, err error) {
	e := v.(*spanEntry)
	e.span.RecordError(err)
	e.span.End()
	s.entries.Delete(k)
}
