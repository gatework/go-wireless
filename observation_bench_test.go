package wireless_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	wireless "github.com/gatework/go-wireless"
)

func benchmarkAccessPoints(size int) []wireless.Record {
	records := make([]wireless.Record, size)
	for i := range records {
		records[i] = wireless.Record{
			"name": fmt.Sprintf("AP-%04d", i), "ap_id": int64(i), "operational_status": "online",
			"management_ip": fmt.Sprintf("10.0.%d.%d", i/254, i%254+1), "uptime": json.Number("9007199254740993"),
			"radios":   []wireless.Record{{"radio_id": 1, "power": 13.0, "clients": 7}, {"radio_id": 2, "power": 16.5, "clients": 0}},
			"location": nil, "tags": []string{}, "alerts": []any(nil),
		}
	}
	return records
}

func BenchmarkObservationProjection(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprintf("aps=%d", size), func(b *testing.B) {
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			observation, err := wireless.NewObservation(wireless.ObservationOptions{
				Name: wireless.AccessPoints, Status: wireless.Complete, Records: benchmarkAccessPoints(size),
				Evidence:  []wireless.Record{{"command": "display wlan ap all verbose", "sha256": "fixture", "bytes": int64(size * 256)}},
				StartedAt: &now, CompletedAt: &now,
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				observation.ToMap()
			}
		})
	}
}
