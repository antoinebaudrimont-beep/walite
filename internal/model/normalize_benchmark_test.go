package model

import (
	"strings"
	"testing"
)

var normalizeBenchmarkSink string
var normalizeTruncatedSink bool

func BenchmarkNormalizeText(b *testing.B) {
	cases := []struct {
		name  string
		value string
	}{
		{"MaxRetainedValid", strings.Repeat("x", MaxRetainedTextBytes)},
		{"InvalidUTF8Prefix", string(append([]byte{0xff, 0xfe}, []byte(strings.Repeat("x", MaxRetainedTextBytes))...))},
		{"Valid1MiB", strings.Repeat("x", 1<<20)},
		{"Valid8MiB", strings.Repeat("x", 8<<20)},
		{"Valid32MiB", strings.Repeat("x", 32<<20)},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				normalizeBenchmarkSink, normalizeTruncatedSink = NormalizeText(test.value)
			}
		})
	}
}
