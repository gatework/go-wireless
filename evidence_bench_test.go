package wireless_test

import (
	"fmt"
	"testing"

	wireless "github.com/gatework/go-wireless"
)

func BenchmarkEvidenceClone(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprintf("aps=%d", size), func(b *testing.B) {
			document := wireless.Record{
				"device":     wireless.Record{"host": "192.0.2.10", "platform": "h3c_wireless", "model": nil},
				"dimensions": wireless.Record{"access_points": wireless.Record{"status": "complete", "records": benchmarkAccessPoints(size), "issues": []wireless.Record{}}},
			}
			if _, err := wireless.CopyValue(document); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				wireless.CloneRecord(document)
			}
		})
	}
}
