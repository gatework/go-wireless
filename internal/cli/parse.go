package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gatework/go-wireless/internal/input"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func (r *Runner) parse(ctx context.Context, o *documentFlags) int {
	values := o.Values
	complete, jsonOutput, help, examples := o.Complete, o.JSON, o.Help, o.Examples
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
	for _, d := range vendors.NewBuiltinRegistry().Dimensions() {
		valid = valid || d == dimension
	}
	if !valid {
		return r.failure("未知维度")
	}
	profile, e := vendors.NewBuiltinRegistry().Fetch(values["--platform"], values["--model"])
	if e != nil {
		return r.failure("离线解析平台或型号错误")
	}
	if dimension == wireless.LLDP && strings.TrimSpace(values["--ap-name"]) == "" {
		return r.failure("LLDP 需要 --ap-name")
	}
	var body []byte
	if file, ok := values["--file"]; ok {
		body, e = input.Document(ctx, file)
	} else {
		body, e = io.ReadAll(input.Reader{Source: r.Input, Context: ctx})
	}
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return r.failure("离线解析文件错误")
	}
	var observation wireless.Observation
	if dimension == wireless.LLDP {
		p, pe := parser.NewNeighborParser(profile, values["--model"])
		if pe != nil {
			return r.failure("离线解析参数错误")
		}
		observation, e = p.Parse(string(body), values["--ap-name"], complete)
	} else {
		p, pe := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), values["--model"])
		if pe != nil {
			return r.failure("离线解析参数错误")
		}
		observation, e = p.Parse(dimension, string(body), parser.ParseOptions{Platform: values["--platform"], Complete: complete})
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
				codes = append(codes, StringValue(issue["code"]))
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
