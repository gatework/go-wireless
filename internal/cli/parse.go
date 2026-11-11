package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gatework/go-wireless/internal/input"

	"github.com/gatework/go-wireless/vendors"

	wireless "github.com/gatework/go-wireless"
	"github.com/gatework/go-wireless/parser"
)

func (r *Runner) parse(ctx context.Context, o *documentFlags) int {
	values := o.Values
	if o.Help {
		return r.help("parse", false)
	}
	if o.Examples {
		if _, e := fmt.Fprintln(r.Output, "wireless parse --file running.cfg --complete\ncat aps.txt | wireless parse --dimension access_points --complete --json"); e != nil {
			return 2
		}
		return 0
	}
	if ctx.Err() != nil {
		return 130
	}
	// Validate profile/scope before consuming stdin or opening a file.
	dimension := wireless.Dimension(values["--dimension"])
	profile, message := parseRequest(values, dimension)
	if message != "" {
		return r.failure(message)
	}
	body, e := r.parseInput(ctx, values)
	if ctx.Err() != nil {
		return 130
	}
	if e != nil {
		return r.failure("离线解析文件错误")
	}
	observation, message := parseObservation(profile, values, dimension, string(body), o.Complete)
	if ctx.Err() != nil {
		return 130
	}
	if message != "" {
		return r.failure(message)
	}
	if o.JSON {
		e = r.outputJSONContext(ctx, observation)
	} else {
		e = r.writeObservationSummary(observation)
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

// parseRequest validates the dimension, platform, model and AP name; it
// returns the operator message for an invalid request.
func parseRequest(values map[string]string, dimension wireless.Dimension) (*parser.Profile, string) {
	if !slices.Contains(vendors.NewBuiltinRegistry().Dimensions(), dimension) {
		return nil, "未知维度"
	}
	profile, e := vendors.NewBuiltinRegistry().Fetch(values["--platform"], values["--model"])
	if e != nil {
		return nil, "离线解析平台或型号错误"
	}
	if dimension == wireless.LLDP && strings.TrimSpace(values["--ap-name"]) == "" {
		return nil, "LLDP 需要 --ap-name"
	}
	return profile, ""
}

// parseInput reads the --file document or standard input.
func (r *Runner) parseInput(ctx context.Context, values map[string]string) ([]byte, error) {
	if file, ok := values["--file"]; ok {
		return input.Document(ctx, file)
	}
	return io.ReadAll(input.Reader{Source: r.Input, Context: ctx})
}

// parseObservation parses body for dimension; it returns the operator
// message on failure.
func parseObservation(profile *parser.Profile, values map[string]string, dimension wireless.Dimension, body string, complete bool) (wireless.Observation, string) {
	if dimension == wireless.LLDP {
		p, e := parser.NewNeighborParser(profile, values["--model"])
		if e != nil {
			return wireless.Observation{}, "离线解析参数错误"
		}
		observation, e := p.Parse(body, values["--ap-name"], complete)
		if e != nil {
			return wireless.Observation{}, "离线解析参数或文件错误"
		}
		return observation, ""
	}
	p, e := parser.NewRuntimeParser(vendors.NewBuiltinRegistry(), values["--model"])
	if e != nil {
		return wireless.Observation{}, "离线解析参数错误"
	}
	observation, e := p.Parse(dimension, body, parser.ParseOptions{Platform: values["--platform"], Complete: complete})
	if e != nil {
		return wireless.Observation{}, "离线解析参数或文件错误"
	}
	return observation, ""
}

// writeObservationSummary prints the dimension, status, record count and
// issue codes of observation.
func (r *Runner) writeObservationSummary(observation wireless.Observation) error {
	if _, e := fmt.Fprintf(r.Output, "维度：%s；证据：%s；记录：%d\n", observation.Name(), observation.Status(), len(observation.Records())); e != nil {
		return e
	}
	codes := []string{}
	for _, issue := range observation.Issues() {
		codes = append(codes, StringValue(issue["code"]))
	}
	if len(codes) > 0 {
		if _, e := fmt.Fprintf(r.Output, "诊断：%s\n", strings.Join(codes, ", ")); e != nil {
			return e
		}
	}
	_, e := fmt.Fprintln(r.Output, "证据完整性不代表 无线网络健康。")
	return e
}
