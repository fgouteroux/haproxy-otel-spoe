package internal

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// newNoopSpan returns a real SDK span. The SDK returns spans as pointers,
// making them safely comparable with == / != in tests.
func newNoopSpan(t *testing.T) trace.Span {
	t.Helper()
	tp := sdktrace.NewTracerProvider()
	_, span := tp.Tracer("test").Start(context.Background(), "test")
	return span
}

func TestStore_SetGet(t *testing.T) {
	s := NewStore(30 * time.Second)
	defer s.Close()

	span := newNoopSpan(t)
	s.Set("req-1", span)

	got, ok := s.Get("req-1")
	if !ok {
		t.Fatal("expected span to be found")
	}
	if got != span {
		t.Error("returned span does not match stored span")
	}
}

func TestStore_GetMissing(t *testing.T) {
	s := NewStore(30 * time.Second)
	defer s.Close()

	_, ok := s.Get("nonexistent")
	if ok {
		t.Error("expected false for missing key, got true")
	}
}

func TestStore_Delete(t *testing.T) {
	s := NewStore(30 * time.Second)
	defer s.Close()

	s.Set("req-1", newNoopSpan(t))
	s.Delete("req-1")

	_, ok := s.Get("req-1")
	if ok {
		t.Error("expected span to be gone after Delete")
	}
}

func TestStore_OverwriteKey(t *testing.T) {
	s := NewStore(30 * time.Second)
	defer s.Close()

	span1 := newNoopSpan(t)
	span2 := newNoopSpan(t)

	s.Set("req-1", span1)
	s.Set("req-1", span2)

	got, ok := s.Get("req-1")
	if !ok {
		t.Fatal("expected span to be found")
	}
	if got != span2 {
		t.Error("expected second span to overwrite first")
	}
}

func TestStore_TTLEviction(t *testing.T) {
	ttl := 60 * time.Millisecond
	s := NewStore(ttl)
	defer s.Close()

	s.Set("req-ttl", newNoopSpan(t))

	// Wait long enough for cleanup to fire (cleanup ticker = ttl/3 ≈ 20ms)
	// and the entry to exceed TTL.
	time.Sleep(3 * ttl)

	_, ok := s.Get("req-ttl")
	if ok {
		t.Error("expected span to be evicted after TTL")
	}
}

func TestStore_TTLDoesNotEvictFresh(t *testing.T) {
	ttl := 200 * time.Millisecond
	s := NewStore(ttl)
	defer s.Close()

	s.Set("req-fresh", newNoopSpan(t))

	// Wait less than TTL — entry must still be present.
	time.Sleep(ttl / 4)

	_, ok := s.Get("req-fresh")
	if !ok {
		t.Error("expected fresh span to still be present before TTL expires")
	}
}

func TestStore_CloseEndsRemainingSpans(t *testing.T) {
	s := NewStore(30 * time.Second)

	for i := range 5 {
		s.Set(string(rune('a'+i)), newNoopSpan(t))
	}

	// Close should not panic and all entries should be gone afterwards.
	s.Close()

	count := 0
	s.entries.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected store to be empty after Close, got %d entries", count)
	}
}

func TestStore_CloseIdempotentCallOrder(_ *testing.T) {
	// Verify Close can be called when the store is already empty.
	s := NewStore(30 * time.Second)
	s.Close() // must not panic
}
