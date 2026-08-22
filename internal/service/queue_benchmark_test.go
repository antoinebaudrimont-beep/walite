package service

import (
	"errors"
	"testing"
)

var queueBenchmarkErr error

func benchmarkIntWeight(int) (int64, error) { return 1, nil }

func BenchmarkBoundedQueueTryPut(b *testing.B) {
	b.Run("AvailableEntryAndBytes", func(b *testing.B) {
		const batchSize = 256
		budget, _ := newByteBudget(batchSize)
		queue, _ := newBoundedQueue(batchSize, budget, benchmarkIntWeight)
		b.ReportAllocs()
		b.StopTimer()
		b.ResetTimer()
		processed := 0
		for processed < b.N {
			count := batchSize
			if remaining := b.N - processed; remaining < count {
				count = remaining
			}
			b.StartTimer()
			for i := 0; i < count; i++ {
				queueBenchmarkErr = queue.TryPut(1)
			}
			b.StopTimer()
			if queueBenchmarkErr != nil {
				b.Fatal(queueBenchmarkErr)
			}
			for i := 0; i < count; i++ {
				owned, ok := queue.TryTake()
				if !ok {
					b.Fatal("missing benchmark value")
				}
				if err := owned.Release(); err != nil {
					b.Fatal(err)
				}
			}
			processed += count
		}
	})
	b.Run("EntryFull", func(b *testing.B) {
		budget, _ := newByteBudget(2)
		queue, _ := newBoundedQueue(1, budget, benchmarkIntWeight)
		_ = queue.TryPut(1)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queueBenchmarkErr = queue.TryPut(2)
			if !errors.Is(queueBenchmarkErr, errQueueFull) {
				b.Fatal(queueBenchmarkErr)
			}
		}
	})
	b.Run("ByteFull", func(b *testing.B) {
		budget, _ := newByteBudget(1)
		queue, _ := newBoundedQueue(2, budget, benchmarkIntWeight)
		charge, _ := budget.tryAcquire(1)
		defer charge.Release()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			queueBenchmarkErr = queue.TryPut(1)
			if !errors.Is(queueBenchmarkErr, errQueueFull) {
				b.Fatal(queueBenchmarkErr)
			}
		}
	})
}
