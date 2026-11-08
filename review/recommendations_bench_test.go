package review

import (
	"encoding/json"
	"fmt"
	"testing"

	wireless "github.com/gatework/go-wireless"
)

func benchmarkTemplateConfiguration(size int) wireless.Record {
	templateCount := max(size/16, 1)
	templates := make([]wireless.Record, templateCount)
	for i := range templates {
		templates[i] = wireless.Record{"name": fmt.Sprintf("template-%03d", i), "enabled": i%2 == 0}
	}
	radio := func(index int) wireless.Record {
		return wireless.Record{"service_templates": []wireless.Record{{
			"name": fmt.Sprintf("template-%03d", index%(templateCount+1)), "vlan_id": json.Number("100"),
		}}}
	}
	aps := make(wireless.Record, size)
	for i := range size {
		name := fmt.Sprintf("AP-%04d", i)
		aps[name] = wireless.Record{"name": name, "radio": wireless.Record{"1": radio(i), "2": radio(i + 1)}}
	}
	groups := make([]wireless.Record, max(size/64, 1))
	for i := range groups {
		groups[i] = wireless.Record{"name": fmt.Sprintf("floor-%03d", i), "radio": wireless.Record{"1": radio(i)}}
	}
	return wireless.Record{"access_points": aps, "ap_groups": groups, "service_templates": templates}
}

func BenchmarkTemplateBindings(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprintf("aps=%d", size), func(b *testing.B) {
			configuration := benchmarkTemplateConfiguration(size)
			b.ReportAllocs()
			for b.Loop() {
				bindingTasks(configuration, nil)
			}
		})
	}
}

func BenchmarkTemplateReferences(b *testing.B) {
	for _, size := range []int{64, 512, 4096} {
		b.Run(fmt.Sprintf("aps=%d", size), func(b *testing.B) {
			configuration := benchmarkTemplateConfiguration(size)
			b.ReportAllocs()
			for b.Loop() {
				appendTemplateReferences([]wireless.Record{}, configuration["access_points"], "access_points", nil)
			}
		})
	}
}
