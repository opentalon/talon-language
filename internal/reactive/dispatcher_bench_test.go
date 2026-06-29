package reactive

import (
	"context"
	"fmt"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
)

// BenchmarkDispatchManyBlocks measures per-event cost with N registered
// `on change` blocks indexed by a unique Attr each. The old linear-scan
// dispatcher was O(N) per event; the indexed one is O(1) plus the
// wildcard slice (zero here). Run:
//
//	go test -bench=BenchmarkDispatch -benchmem -run=^$ ./internal/reactive
func BenchmarkDispatchManyBlocks(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			d := New(func(_ context.Context, _ *ast.OnBlock, _ factstore.Event) {})
			for i := 0; i < n; i++ {
				d.Register(&ast.OnBlock{Trigger: "change", Attr: fmt.Sprintf("attr-%d", i)})
			}
			// Always fire on the last-registered attribute so we hit
			// the worst case under the old linear scan and the
			// best/typical case under the indexed scheme.
			ev := factstore.Event{
				Kind: factstore.EventChange,
				Fact: factstore.Fact{Attribute: fmt.Sprintf("attr-%d", n-1)},
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.handle(context.Background(), ev)
			}
		})
	}
}

// BenchmarkDispatchNoMatch measures per-event cost when an event hits
// no registered block — this is the dominant case in real workloads
// where most attributes don't have reactive subscribers.
func BenchmarkDispatchNoMatch(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			d := New(func(_ context.Context, _ *ast.OnBlock, _ factstore.Event) {})
			for i := 0; i < n; i++ {
				d.Register(&ast.OnBlock{Trigger: "change", Attr: fmt.Sprintf("attr-%d", i)})
			}
			ev := factstore.Event{
				Kind: factstore.EventChange,
				Fact: factstore.Fact{Attribute: "never-registered"},
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.handle(context.Background(), ev)
			}
		})
	}
}

// BenchmarkDispatchWildcards measures the worst case for the indexed
// scheme: every block is registered with an empty anchor, so each
// event has to walk the wildcard slice. This is still O(W) where W is
// the wildcard count, but it isolates that cost from the specific-
// anchor fast path.
func BenchmarkDispatchWildcards(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			d := New(func(_ context.Context, _ *ast.OnBlock, _ factstore.Event) {})
			for i := 0; i < n; i++ {
				d.Register(&ast.OnBlock{Trigger: "change"}) // empty Attr = wildcard
			}
			ev := factstore.Event{
				Kind: factstore.EventChange,
				Fact: factstore.Fact{Attribute: "anything"},
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.handle(context.Background(), ev)
			}
		})
	}
}
