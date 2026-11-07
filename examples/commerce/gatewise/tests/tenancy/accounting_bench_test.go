package tenancy_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/tenancy"
	"testing"
	"time"
)

func BenchmarkAccounting(b *testing.B) {
	b.Run("service-only", func(b *testing.B) {
		l := admission.New(12, 4)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			l.TryAcquire(admission.Critical)
			l.Release(admission.Critical)
		}
	})
	b.Run("tenant-and-service", func(b *testing.B) {
		l := admission.New(12, 4)
		t := tenancy.New(tenancy.Config{Limit: 2, MaxTracked: 1024, IdleTTL: time.Minute})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lease, _ := t.Acquire("known-tenant", time.Now())
			l.TryAcquire(admission.Critical)
			lease.Retain()
			l.Release(admission.Critical)
			lease.Release(time.Now())
		}
	})
	b.Run("tenant-rejection", func(b *testing.B) {
		t := tenancy.New(tenancy.Config{Limit: 1, MaxTracked: 1024, IdleTTL: time.Minute})
		lease, _ := t.Acquire("known-tenant", time.Now())
		lease.Retain()
		defer lease.Release(time.Now())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			t.Acquire("known-tenant", time.Now())
		}
	})
	b.Run("registry-full", func(b *testing.B) {
		t := tenancy.New(tenancy.Config{Limit: 1, MaxTracked: 1, IdleTTL: time.Minute})
		lease, _ := t.Acquire("known-tenant", time.Now())
		lease.Retain()
		lease.Release(time.Now())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			t.Acquire("unknown-tenant", time.Now())
		}
	})
}
