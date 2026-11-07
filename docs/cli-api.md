# CLI and private report API (Task 3)

All implementations are Go. No Ruby runtime is used. Module:
`github.com/gatework/go-wireless`. Run `python3 scripts/prepare-workspace.py`
before Go commands to expand the pinned connector workspace.

## Entry point and extension registration

```go
func cli.Run(context.Context, []string, cli.Dependencies) int
func cli.New(cli.Dependencies) *cli.Runner
func (*cli.Runner) Run(context.Context, []string) int
func (*cli.Runner) Help(command string) int
func (*cli.Runner) ReadInput(context.Context, []byte) (int,error)
func (*cli.Runner) ReadLine(context.Context) (string,error)

type cli.Command func(context.Context, []string, *cli.Runner) int
```

`cmd/wireless` owns SIGINT/SIGTERM and the process exit. Library entry points only
return a code. No command means `collect`; leading collection flags also select
collect. Explicit commands `collect` and `parse` work. Global help/version and
all four command helps read no configuration/input/inventory and create no files.
The default runner implements `review` and `apply`; `Dependencies.Commands` can
override commands explicitly. Global usage advertises all four commands.
See `docs/change-api.md` for the final recommendation/review/apply contracts. `--help-all` aliases
command help; collect adds discovery/environment details. Collect/parse examples
are also informational.

```go
type cli.Discovery struct {
    CWD, Home, Program string
    Directories []string // nil = real discovery; nonnil empty = none
}
type cli.Connection struct {
    Open wireless.OpenSession
    Platform string // actual connector platform, separately checked by core
}
type cli.SessionFactory func(context.Context, wireless.Record,
    connector.ConnectionConfig) (cli.Connection, error)
type cli.SourceFactory func(*cli.Options) (netdisco.Source, string, error)
type cli.Executor func(context.Context, int, []wireless.Record,
    func(context.Context, wireless.Record) error) error
```

`Dependencies` has `Environment map[string]string`, `Input io.Reader`,
`Output, Error io.Writer`, `Discovery`, `SessionFactory`, `SourceFactory`,
`APFactory wireless.APFactory`, `PrepareResources func(context.Context,int)error`,
`Execute cli.Executor`, `ReportOptions report.Options`,
`RecommendationHook func(scripts bool) report.DeviceHook`,
`NewReport func(string,report.Options)(*report.Report,error)`,
`ApplyFactory cli.ApplyFactory`,
`Commands map[string]cli.Command`, and `Terminal func(any)bool`.
Defaults use real streams/environment, native sources/connector/adapter,
`resources.Prepare`, `parallel.Map`, `report.New`, and descriptor terminal tests.
Environment is copied when creating a Runner; nil reads `os.Environ`, empty
nonnil means explicitly empty. Run state resets on each invocation. An injected
Executor must join all admitted callbacks before returning. Session factories
are invoked only inside device workers; the CLI prevalidates every selected
credential and native ConnectionConfig before resources/report construction.
All tasks are joined before one finalizer publishes batch status.

Input is borrowed, exclusively read by one command, and never closed by the CLI.
`ReadInput` synchronously supports `*os.File` (including executable stdin) through
bounded descriptor polling, without modifying descriptor flags or starting a
reader goroutine. Cancellation wakes held-open pipe/terminal input. Other injected
blocking inputs must implement both io.Reader and `cli.ContextReader`:
`ReadContext(context.Context, []byte) (int,error)`, returning promptly when its
context is canceled. Ordinary injected io.Reader values must return promptly;
the CLI checks context before and after each call but cannot interrupt a reader
that violates this contract. `ReadLine` uses that same boundary, reads through one
newline without prefetching later input, and returns the line plus any EOF/error.
Apply confirmation uses `Runner.ReadLine(ctx)` and observe cancellation
before accepting a token or creating execution state. Optional initialization
uses it too. Parse checks after reading and parsing, then again before JSON bytes
are written; canceled input does not become a successful fact export.

`SourceFactory` replaces only source construction. Rules, selectors, verified
cache and cache mode remain real. Return a stable nonempty identity containing
no password; the native cache hashes it. Source may be nil only for cache-only.
The native default supports paginated HTTP and read-only PostgreSQL with bounded
responses, whole inventory and timeout, then authenticated private native cache.
No real external inventory or device service was used in Task 3 tests.

## Frozen configuration

```go
func cli.LoadOptions(argv []string, env map[string]string,
    discovery cli.Discovery) (*cli.Options, error)
func cli.LoadEnvironment(env map[string]string, envFile string,
    discovery cli.Discovery) (map[string]string, wireless.Record, error)
func cli.NormalizeDevices([]any, defaultPlatform string) ([]wireless.Record,error)
```

LoadOptions reads/validates one configuration/environment snapshot without
inventory, sessions, reports or prompts. Missing configuration is an error.
Options exposes owned `Values wireless.Record`, `Environment map[string]string`,
`Sources wireless.Record`, `Config, EnvFile string`, `Flags cli.Flags`,
`Dynamic() bool`, and `Initialize(io.Writer) error`. Public values are separate
from credentials. Do not log Options.Environment. LoadEnvironment performs the
same `.env` discovery/explicit-last merge and source-relative environment paths,
without YAML. Apply calls it only after confirmation with the desired
Discovery and explicit env-file; `Directories: []string{}` disables automatic
`.env` discovery when an explicit-only policy is needed. Both APIs return owned
maps for their caller, which must not mutate a snapshot shared with workers.

Precedence is CLI > process environment > YAML > .env > built-in defaults.
Discovery, deduplicated in order: `/etc/net-connector`,
`/usr/local/etc/net-connector`, `$XDG_CONFIG_HOME/net-connector` (fallback
`$HOME/.config/net-connector`), executable directory, launch CWD. Each directory
loads `.env`, then `net-connector.yml` common/wireless overlays and `wireless.yml`.
Explicit env/config files are applied last. Blank WIRELESS_CONFIG selectors are
absent. Explicit missing files fail; explicit empty file arguments fail.
Unknown/duplicate keys, aliases, nonstring keys, extra YAML documents, invalid
types and invalid effective policy fail without echoing private contents.
Sources includes configuration_files, environment_files and dotted YAML field origins (for example parallel and ssh.known_hosts); empty map overlays retain inherited leaf origins.

Top-level schema: platform, ap_names, devices, netdisco, inventory,
netdisco_config, parallel, directory, dimensions, ssh, hosts, exclude_hosts,
names, exclude_names. No unknown/credential fields are accepted. Device fields:
host, name, model, platform; normalized device identity is exactly
`{host, name, model, platform}`, model explicitly null when unknown. Duplicate
canonical IPs fail before execution. h3c/h3c_wireless aliases retain identity but
native H3C sessions use h3c_wireless. Static dedicated devices do not inherit
common dynamic inventory. Nonempty static devices, inline dynamic inventory and
external netdisco_config are mutually exclusive. Empty `devices: []` alone is a
valid zero-target inventory. Null dimensions means profile defaults excluding
LLDP; explicit empty/duplicate dimensions fail. Host and name selectors intersect;
values within each list union; exclusions always win. CIDRs filter existing hosts
without scanning or expanding addresses.

Built-in defaults: platform h3c, parallel 4 (1..500), directory
`<launch-cwd>/wireless-reports`, null dimensions, empty devices/AP/filter lists,
SSH strict, command_timeout 60s, login_timeout 15s. SSH schema is port (1..65535),
command_timeout/login_timeout positive finite seconds, host_key_policy
strict/accept_new, known_hosts path. Public env includes WIRELESS_PLATFORM,
WIRELESS_DIRECTORY, WIRELESS_PARALLEL (fallback NC_PARALLEL), NC_HOST_KEY_POLICY,
NC_KNOWN_HOSTS, and exactly the pinned inventory schema's environment keys.
Paths are resolved relative to their YAML/.env file; CLI/process paths use CWD.
`db_sslrootcert: system` remains a literal libpq trust marker.

Netdisco/inventory schema is the actual net-connector 0.14.2 schema recorded in
cli/inventory_schema.go, not the broader native schema. Device-rule keys are only
vendor, connector, os, model_prefix. External files validate known section names
but consume only netdisco/inventory, ignoring unrelated sections' contents;
source-relative paths are retained in `inventory_configuration`. This effective
public document is included by --show-config without fetching inventory or
requiring credentials. Native inventory Rules resolve readiness/controller vendor
before Wireless CLI selectors. General H3C switches never become controllers.

The verbatim initialized YAML defaults HTTP cache enabled/prefer, TTL 259200s,
page 500, max_pages 10000, max_response_bytes 16777216,
max_inventory_bytes 134217728, max_devices 100000, inventory_timeout 300s,
allow_insecure_http false, include_vendors [h3c_wireless], SSH accept_new.
Init creates missing user-directory files with 0600, publishes exclusively by
hard link, fsyncs, preserves existing files, and does not read inventory. NC_PARALLEL
inheritance is kept out of generated YAML unless WIRELESS_PARALLEL or CLI explicitly
overrides it. No interactive input is consumed outside real terminals. Forced
--interactive requires input and error terminals. Help precedes that check.

Credentials are snapshotted from .env/process, platform NC_H3C_WIRELESS_* or
NC_HUAWEI_* before NC_DEVICE_USERNAME/PASSWORD, with NC_ENABLE_PASSWORD.
Username is mandatory for every selected controller; password may be omitted for
SSH key/agent. Explicit LLDP alone enables the H3C AP adapter; AP credentials are
resolved lazily from independent WIRELESS_AP_USERNAME/PASSWORD. Default collection
never opens AP sessions. Parallelism is bounded; queries within a controller stay
sequential. `resources.Prepare(ctx,min(parallel,selected))` runs before reports or
sessions. Internal fatal storage cancellation is distinct from user cancellation.

## Parse/output and codes

Parse supports --file or stdin, singular --dimension (default configuration),
--platform, --model, --ap-name, --complete, --json, help/all/examples. Completeness
is false unless explicit; parser diagnostics remain authoritative. Human output
contains metadata only. JSON explicitly exports facts. Collection stdout is JSON
only with --json, except explicitly requested --show-config; human progress and
final summaries use stderr, respecting --quiet/--no-progress and --verbose. TTY
refresh uses control sequences; redirected stderr uses plain notices/results.
Errors expose safe classifications, not raw source/transport/storage messages.

Exit 0: complete evidence or nonempty preview/init/help. Exit 1: partial/failed
collection, no targets or progress-output failure. Exit 2: invalid inputs,
credential/resource/source/execution/report failure, or failed stdout delivery.
Exit 130: caller cancellation, preserving precedence even if final recovery fails.
Completed/pending results survive cancellation; facts and device commands are never
replayed to repair persistence or display failures. Late progress failure or late
cancellation can republish the final checkpoint through the same owner.

## Numeric wire boundary

`wireless.MarshalJSON(any)` and `wireless.MarshalJSONIndent(any,prefix,indent)`
preserve floats in nested fact maps/slices. Finite float64(20) writes 20.0;
negative zero writes -0.0. Decimal/exponent json.Number tokens retain intent;
arbitrary integers are exact. Observation/Snapshot/Coverage/Projection JSON
marshalers use this boundary. Arbitrary unrelated structs retain encoding/json's
normal semantics. Use the shared helper when explicitly exporting ToMap/facts.
When reading JSON into generic maps, always use Decoder.UseNumber: normal
Unmarshal into map[string]any rounds large integers and loses numeric kinds.
The core CanonicalJSON/Fingerprint APIs implement Ruby JSON 3.1 canonical float
fingerprint spelling separately; see docs/change-api.md.

## Report publisher and recommendation hook

```go
type report.Options struct {
    Now func() time.Time
    Write report.WriteFunc
    DeviceHook report.DeviceHook
}
type report.WriteFunc func(context.Context,string,[]byte)(storage.Receipt,error)
func report.New(root string, report.Options) (*report.Report,error)
func (*report.Report) Directory() string
func (*report.Report) Publish(context.Context, relative string, []byte) error
func (*report.Report) WriteDevice(context.Context, report.Result,
    *wireless.Snapshot) error
func (*report.Report) Finish(context.Context, []report.Result,
    report.FinishOptions) (wireless.Record,error)
func (*report.Report) Finalize(context.Context, relative string,
    checkpoint, final wireless.Record,
    artifacts func(context.Context,*report.Report)error) error
func (*report.Report) WriteExecution(context.Context, any) error
func (*report.Report) RecoveryFailed() bool
func report.Basename(wireless.Record) string
```

One writer publishes all collect/review/recommendation/execution artifacts.
Root is a real private directory, symlink/public roots fail, existing modes are
never chmodded. New batches use UTC `YYYY-MM-DD_HH-MM-SS`, `_1`, `_2` collision
suffixes and private devices/. Files are 0600. Publish confines relative paths,
rejects symlink/public descendant directories, and delegates native same-directory
temporary/write/file fsync/rename/directory fsync/cleanup. A report.Error exposes
Relative, Phase, Committed, Durable, safe Error()/ToMap(). Cancellation identity
is preserved through errors.Is without retaining arbitrary private causes. The
injected writer returns native storage receipts for failure-phase tests.

Result fields: Device Record, Status wireless.Status,
Dimensions map[string]report.DimensionSummary, Error string, DurationMS int64,
ReportSaved bool. DimensionSummary: Status, Records int, Issues []string.
Result.ToMap has device/status/dimensions/duration_ms/report_saved and optional
error. A worker records ReportSaved=false in batch state BEFORE any persistence;
only after all device JSON, TXT, and hook artifacts succeed is it set true.
Device JSON omits report_saved and always contains snapshot (explicit null for
pre-snapshot failure). Snapshot envelopes remain target/dimensions/observed_at.
Known unknown model remains null. Failed later writes retain already committed
JSON and a report-error receipt in batch summary.

```go
type report.DeviceHook func(context.Context,wireless.Record)(report.Enrichment,error)
type report.Enrichment struct {
    Recommendations any
    Artifacts func(context.Context,*report.Report,report.PublishedDevice)error
}
type report.PublishedDevice struct {
    Basename, Path, SHA256 string
    JSON []byte
    Document wireless.Record
}
```

CLI selects `Dependencies.RecommendationHook(effectiveScriptsFlag)` per run;
otherwise it uses ReportOptions.DeviceHook. The runner installs its actual selector by default
(diagnostic-only, --scripts explicit candidates), with real review/apply commands. WriteDevice first materializes an owned generic wire
Record using the shared numeric encoder plus Decoder.UseNumber. The hook's input
is exactly the device envelope WITHOUT report_saved/recommendations and WITH
snapshot. All numeric values are json.Number, including decimal/exponent floats
and negative zero. The hook computes recommendations without publishing files.
Its returned Recommendations is inserted in device JSON before its final bytes
are encoded. JSON then TXT publish; only then Artifacts runs. PublishedDevice.Path
is the absolute final device JSON path, JSON is its exact bytes including trailing
newline, SHA256 is the lowercase hex digest of those exact bytes, Document is the
matching enriched wire document, Basename matches devices/<basename>.json.
Artifacts writes recommendations/*.json/.txt/.verify.cli and optional scripts/
plans through this Report.Publish, binding source path/hash to the exact source.
Returning an artifact error keeps report_saved=false; committed source JSON stays.
RecommendationHook now generates the actual advice, scripts and typed plans.

FinishOptions has Interrupted bool, Requested []Record,
ReportError/ExecutionError/OutputError any. Summary fields are status, total,
completed, pending, counts, devices, directory, evidence_only true and optional
report_error/execution_error/output_error. Complete requires nonempty results,
no pending, all status complete and saved, and no report/execution/output errors.
Summary JSON checkpoint is incomplete/interrupted with
report_finalization_unconfirmed before summary.txt and final summary.json.
Finalize generalizes the same lifecycle for review.json/review.txt; checkpoint
must say incomplete/interrupted. Committed final errors restore checkpoint;
failed restoration removes untrusted complete documents where possible and sets
RecoveryFailed. It never deletes device facts. WriteExecution atomically publishes
execution.json on the same writer for apply per-command checkpoints.

Final recommendation rules, canonical plans, offline review, interactive apply and
publication recovery use these same CLI/report/configuration boundaries. See
`docs/change-api.md` for their complete contracts.
