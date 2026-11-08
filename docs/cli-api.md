# CLI, configuration and report contracts

`cmd/wireless` owns process signals and exit status. `internal/cli` uses one Cobra
command tree with explicit collect, parse, review and apply commands. Root flags
still select collect; root help/version and command help perform no configuration,
input, inventory or report I/O. pflag owns value parsing and aliases; compatibility
validation rejects boolean `=value`, positional arguments and combined short flags.
Repeated selectors union, dimensions replace, scripts/no-scripts use occurrence
order, and conflicting cache modes fail before configuration loads.

The CLI is an application implementation, not a public embedding package. SDK
callers use `parser`, `collect`, `review`, `change`, `report` and
`adapter/netconnector`; see [migration](architecture.md).

`internal/bootstrap` owns configuration discovery, the `Request` produced by flags,
immutable validated runtime settings, credential resolution and explicit lazy
session/report assembly. Diagnostic `Values`, `Environment` and `Sources` views
are detached from execution settings. Mutating views cannot change workers,
dimensions, selected devices, source/cache settings, credentials or connections.
Construct a new settings snapshot to change execution. Zero options cannot execute.
`LoadConfiguration` loads no inventory, session or report. Missing configuration is
reported explicitly so the CLI can prompt; initialization loads only required
environment inputs and publishes missing templates without overwriting files.

Connector `config.InventoryEnvironment(values, higher)` and `ValidateInventory`
own the shared inventory schema. Presence in a higher layer masks lower fields
before decoding, including false, null and empty-list values. Wireless retains
its own target policy and validation. `InventorySettings` exposes detached source
and cache settings only; formatting omits credentials. Its accessors still contain
inventory credentials and are not diagnostic output. `FetchInventory` returns
selected Devices, Total before selectors, and the complete `netdisco.CacheResult`,
including refresh/write diagnostics. Native fetches use Connector's shared cache
and source lifecycle. Injected sources own their client and fetch policy.

`internal/workflow/collect` owns admitted task results, joined workers,
cancellation, durable report finalization and output-failure downgrades. CLI
callbacks render progress/JSON. `internal/workflow/review` validates all source
reports before publication and invokes domain recommendations; `report` owns
artifact text and durable publication. `internal/workflow/apply` owns the confirmed
plan state machine behind consumer-owned Opener and Journal contracts. Bootstrap
binds transport/session factories; help never constructs these dependencies.

Report failure never repeats collection or device writes. Started evidence survives
cancellation. The most recent confirmed summary is returned; a failed publication
never returns an earlier complete summary. Failure history survives recovery.

`internal/input` owns bounded descriptor polling and regular-file admission.
Borrowed stdin is never closed or reconfigured. Other blocking injected readers
must implement `ReadContext(context.Context, []byte) (int, error)` and stop when
canceled; ordinary readers must return promptly. Prompt reading consumes one line
without prefetch. Parse checks cancellation after reading and parsing and before
output. Directories/FIFOs are rejected before confirmation/publication; symlinks to
regular files are supported. Exact evidence/plan bytes have no configuration-size
budget. Only descriptors opened by this package are closed.

## Configuration

Precedence is CLI > process environment > YAML > .env > built-in defaults.
Discovery, deduplicated in order: `/etc/net-connector`,
`/usr/local/etc/net-connector`, `$XDG_CONFIG_HOME/net-connector` (fallback
`$HOME/.config/net-connector`), executable directory, launch CWD. Each directory
loads `.env`, then flat `net-connector.yml` and `wireless.yml`. Shared configuration is validated
against the full connector schema, then projects only netdisco/inventory and
SSH host_key_policy/known_hosts. Shared protocol/vendor_protocols/logging fields
do not change wireless behavior. Wireless SSH port/timeouts remain in wireless.yml.
Legacy common/wireless containers fail with an explicit migration diagnostic.
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
shared dynamic inventory. Nonempty static devices, inline dynamic inventory and
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
internal/bootstrap/inventory.go, not the broader native schema. Device-rule keys are only
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

The default vendor catalog is one injected `dialect.Registry`, used consistently
for validation, fleet inventory matching and connection construction. The default
is `vendors.NewBuiltinRegistry()`. No global registry mutation is used; parser
capabilities remain the private Profile table.

HTTP cache identity uses URL, authentication mode, nonsecret username/namespace and
query shape. API keys never enter the identity, even hashed. API-key mode with caching enabled requires
a username or explicit cache namespace; rotating a key within the same account
keeps the identity. The new connector identity never reads legacy Wireless cache
entries. Prefer and refresh collect a new entry; cache-only returns the existing `cache_unavailable`
code plus `netdisco.ErrCacheRefreshRequired` until the new identity has a verified
cache. The CLI asks for `--refresh-inventory` without changing the exit code.
Unsafe cache permissions or symlinks remain ordinary cache-unavailable failures.
Legacy cache files are left untouched.

The private file-descriptor precheck budgets `128 + 16*min(workers, targets)` and
serializes process-wide limit updates. Zero targets need no descriptors. It raises
only the soft limit, bounded by the existing hard limit; failure precedes device I/O.
