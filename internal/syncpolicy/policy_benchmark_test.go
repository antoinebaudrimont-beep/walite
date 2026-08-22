package syncpolicy

import (
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

var policyBenchmarkSink model.RetentionDecision

func BenchmarkPolicyDecide(b *testing.B) {
	values := config.DefaultValues()
	policy, _ := New(values.Retention)
	now := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	usage, _ := model.NewCacheUsage(1024, 1, 1, 1, 0)
	fullUsage, _ := model.NewCacheUsage(values.Retention.CacheBytes, 1, 1, 1, 0)
	cases := []struct {
		name    string
		message model.Message
		state   model.RetentionState
	}{
		{"Eligible", benchmarkPolicyMessage(now), model.RetentionState{Now: now, Usage: usage, Origin: model.WriteRealtime}},
		{"TooOld", benchmarkPolicyMessage(now.Add(-values.Retention.MaxAge - time.Second)), model.RetentionState{Now: now, Usage: usage, Origin: model.WriteRealtime}},
		{"CountFull", benchmarkPolicyMessage(now), model.RetentionState{Now: now, NewerBodies: values.Retention.MessagesPerChat, Usage: usage, Origin: model.WriteRealtime}},
		{"CacheFull", benchmarkPolicyMessage(now), model.RetentionState{Now: now, Usage: fullUsage, Origin: model.WriteHistory}},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				policyBenchmarkSink, err = policy.Decide(test.message, test.state)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkPolicyMessage(sent time.Time) model.Message {
	message, _ := model.NewMessage(model.MessageInput{ChatID: "benchmark-chat", MessageID: "benchmark-message", SentAt: sent, Text: "Synthetic benchmark"})
	return message
}
