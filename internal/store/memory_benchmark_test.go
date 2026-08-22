package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var memoryBenchmarkUsage model.CacheUsage
var memoryBenchmarkPrune model.PruneResult

func BenchmarkMemoryWrite(b *testing.B) {
	for _, size := range []int{1, 25, 50} {
		batch := benchmarkWriteBatch(size, "benchmark-write-chat")
		b.Run("Unique"+strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				memory, _ := NewMemory(100)
				b.StartTimer()
				if err := memory.Write(context.Background(), batch); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("Duplicate"+strconv.Itoa(size), func(b *testing.B) {
			memory, _ := NewMemory(100)
			if err := memory.Write(context.Background(), batch); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := memory.Write(context.Background(), batch); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryPruneEndToEnd(b *testing.B) {
	for _, population := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(population), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				memory, plan := populatedMemoryAndPlan(b, population)
				var err error
				memoryBenchmarkPrune, err = memory.ApplyPrune(context.Background(), plan)
				if err != nil {
					b.Fatal(err)
				}
				memoryBenchmarkUsage, err = memory.Usage(context.Background())
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkWriteBatch(size int, chat string) model.WriteBatch {
	messages := make([]model.Message, size)
	start := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	for index := range messages {
		messages[index], _ = model.NewMessage(model.MessageInput{ChatID: chat, MessageID: "benchmark-message-" + strconv.Itoa(index), SentAt: start.Add(time.Duration(index) * time.Second), Text: "Synthetic benchmark body"})
	}
	batch, _ := model.NewWriteBatch(model.WriteRealtime, messages)
	return batch
}

func populatedMemoryAndPlan(b *testing.B, population int) (*Memory, model.PrunePlan) {
	b.Helper()
	chatCount := (population + 499) / 500
	memory, _ := NewMemory(chatCount + 1)
	start := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	var firstChatIDs []model.MessageID
	for chatIndex, remaining := 0, population; remaining > 0; chatIndex++ {
		chatSize := 500
		if remaining < chatSize {
			chatSize = remaining
		}
		chat := "benchmark-prune-chat-" + strconv.Itoa(chatIndex)
		for offset := 0; offset < chatSize; offset += model.MaxWriteBatchMessages {
			count := model.MaxWriteBatchMessages
			if chatSize-offset < count {
				count = chatSize - offset
			}
			messages := make([]model.Message, count)
			for index := range messages {
				id := "benchmark-prune-message-" + strconv.Itoa(offset+index)
				messages[index], _ = model.NewMessage(model.MessageInput{ChatID: chat, MessageID: id, SentAt: start.Add(time.Duration(offset+index) * time.Second), Text: "Synthetic benchmark body"})
				if chatIndex == 0 {
					firstChatIDs = append(firstChatIDs, messages[index].MessageID())
				}
			}
			batch, _ := model.NewWriteBatch(model.WriteHistory, messages)
			if err := memory.Write(context.Background(), batch); err != nil {
				b.Fatal(err)
			}
		}
		remaining -= chatSize
	}
	chatID, _ := model.NewChatID("benchmark-prune-chat-0")
	plan, err := model.NewPrunePlan(chatID, firstChatIDs, nil)
	if err != nil {
		b.Fatal(err)
	}
	return memory, plan
}
