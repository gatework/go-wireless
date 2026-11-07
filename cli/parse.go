package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	wireless "github.com/gatework/go-wireless"
)

func (r *Runner) parse(ctx context.Context, argv []string) int {
	values := map[string]string{"--dimension": "configuration", "--platform": "h3c"}
	complete, jsonOutput, help, examples := false, false, false, false
	for i := 0; i < len(argv); i++ {
		key, value, has := strings.Cut(argv[i], "=")
		switch key {
		case "--file", "--dimension", "--platform", "--ap-name", "--model":
			if !has {
				i++
				if i >= len(argv) {
					return r.failure("离线解析参数错误")
				}
				value = argv[i]
			}
			values[key] = value
		default:
			if has {
				return r.failure("离线解析参数错误")
			}
			switch key {
			case "--complete":
				complete = true
			case "--json":
				jsonOutput = true
			case "--help", "--help-all", "-h":
				help = true
			case "--examples":
				examples = true
			default:
				return r.failure("离线解析参数错误")
			}
		}
	}
	if help {
		return r.help("parse", false)
	}
	if examples {
		_, e := fmt.Fprintln(r.Output, "wireless parse --file running.cfg --complete\ncat aps.txt | wireless parse --dimension access_points --complete --json")
		if e != nil {
			return 2
		}
		return 0
	}
	if ctx.Err() != nil {
		return 130
	}
	// Validate profile/scope before consuming stdin or opening a file.
	dimension := wireless.Dimension(values["--dimension"])
	valid := false
	for _, d := range wireless.SupportedDimensions() {
		valid = valid || d == dimension
	}
	if !valid {
		return r.failure("未知维度")
	}
	if _, e := wireless.NewProfile(values["--platform"], values["--model"]); e != nil {
		return r.failure("离线解析平台或型号错误")
	}
	if dimension == wireless.LLDP && strings.TrimSpace(values["--ap-name"]) == "" {
		return r.failure("LLDP 需要 --ap-name")
	}
	var body []byte
	var e error
	if file, ok := values["--file"]; ok {
		body, _, e = readRegularFile(file, true, false, 0)
	} else {
		body, e = io.ReadAll(inputReader{runner: r, ctx: ctx})
	}
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return r.failure("离线解析文件错误")
	}
	var observation wireless.Observation
	if dimension == wireless.LLDP {
		p, pe := wireless.NewNeighborParser(values["--platform"], values["--model"])
		if pe != nil {
			return r.failure("离线解析参数错误")
		}
		observation, e = p.Parse(string(body), values["--ap-name"], complete)
	} else {
		p, pe := wireless.NewRuntimeParser(values["--model"])
		if pe != nil {
			return r.failure("离线解析参数错误")
		}
		observation, e = p.Parse(dimension, string(body), wireless.ParseOptions{Platform: values["--platform"], Complete: complete})
	}
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return r.failure("离线解析参数或文件错误")
	}
	if jsonOutput {
		e = r.outputJSONContext(ctx, observation)
	} else {
		_, e = fmt.Fprintf(r.Output, "维度：%s；证据：%s；记录：%d\n", observation.Name(), observation.Status(), len(observation.Records()))
		if e == nil {
			codes := []string{}
			for _, issue := range observation.Issues() {
				codes = append(codes, stringValue(issue["code"]))
			}
			if len(codes) > 0 {
				_, e = fmt.Fprintf(r.Output, "诊断：%s\n", strings.Join(codes, ", "))
			}
		}
		if e == nil {
			_, e = fmt.Fprintln(r.Output, "证据完整性不代表 无线网络健康。")
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return 2
	}
	if observation.Complete() {
		return 0
	}
	return 1
}
