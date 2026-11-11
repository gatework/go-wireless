# Assessment, plans, review and apply API

The Go runtime and `go test ./...` require no Ruby installation. Ruby byte
compatibility was dropped with schema 3; Ruby fixtures remain only as test
inputs whose provenance is recorded in `docs/change-oracle.json`.

## Evidence, review and change packages

```go
// package wireless
func Fingerprint(any) (string, error)
// package change
type TemplateSecurity struct{ Name string; Enabled bool; CipherSuites, SecurityIEs []string }
func (TemplateSecurity) Eligible(*wlan.Configuration) error
func (TemplateSecurity) Satisfied(*wlan.Configuration) error
func DecodeTemplateSecurity(any) (TemplateSecurity, error)
type ChangePlan struct { /* immutable, private state */ }
func NewChangePlan(Record) (ChangePlan, error)
func (ChangePlan) Device() Record
func (ChangePlan) Commands() []string
func (ChangePlan) Rollback() []string
func (ChangePlan) ToMap() Record
func (ChangePlan) CheckBefore(Record) error
func (ChangePlan) CheckAfter(Record) (string, error)

// package wireless
func DecodeJSONDocument([]byte) (Record, error)
// package wlan
func DecodeDocument([]byte) (*Controller, error)
// package rules
func NewAssessor(assess.Policy, Commands) (*Assessor, error)
func (*Assessor) AssessWith(context.Context, []*wlan.Controller, Inputs) (Report, error)
```

`Fingerprint` is the go-netops canonical SHA-256 (`evidence.Fingerprint`: sorted
keys, numbers kept as written, no HTML escaping) of a value's `MarshalJSON` wire
encoding. Because the live readback and the stored JSON pass through the same
encoder, they fingerprint alike, including integral floats (`20.0`) and large
integers. Invalid UTF-8 is rejected rather than replaced, so distinct byte
strings can never share a fingerprint; nonfinite numbers and invalid number
tokens fail too.

`DecodeJSONDocument` requires one JSON object and rejects trailing content,
invalid UTF-8 and malformed UTF-16 surrogate escapes. Numbers keep their source
text as `json.Number`.

`wlan.DecodeDocument` checks the report envelope strictly before any output is
created: device name and host are strings, `snapshot` is present (`null` for a
report without evidence), every dimension has a valid status, and records and
issues are arrays of objects. Fields inside records are decoded strictly per
dimension; a field the typed model does not know marks that dimension
`partial` with an `unmodeled_fact` issue, so rules requiring it are skipped
and no change can be authorized from it.

The schema 3 assessment (package `rules`) replaced the legacy
`review.Recommendations`. It covers incomplete evidence, legacy TKIP/WPA,
missing or disabled bound templates, historical IP conflicts, AP state,
administratively down radios, baseline and temporal drift. Completeness is not
health. The exact `h3c_wireless` identity enables H3C configuration rules;
other platforms receive a blocked `platform_not_supported` finding and Huawei
never receives H3C commands. Only an actual valid string template name can
become a typed change.

The only rendered action is `wireless_security`, with a known boolean enabled
state, an allowed 1–63 character template name, existing CCMP/RSN alternatives
and TKIP and/or WPA to remove. Rendering disables the template first and restores
its original enabled state. `TemplateSecurity.Eligible` compares original ordered
protocol arrays and enabled state on the strictly decoded typed configuration. Satisfaction compares exact arrays after removing
TKIP/WPA and the original enabled state; CLI acknowledgement alone is insufficient.

NewChangePlan validates once and deeply owns its document, device, action and
rendered commands. Accessors return detached copies; the zero value cannot execute.
Schema is compared exactly: 1, 1.0 and 1e0 are valid; a near-one decimal such as
1.0000000000000001 is rejected without float64 rounding. Huge exponents do not
allocate exponent-sized integers. JSON schema and Ruby canonical bytes stay unchanged.

Plan validation accepts schema 1, platform `h3c_wireless`, a canonical IP address,
a lowercase 64-digit expected fingerprint, and exact re-rendered commands and
rollback. It never executes arbitrary command text from a file. Source SHA256
is the hash of the source device JSON bytes; confirmation uses the distinct hash
of the exact plan file bytes.

## Report package

```go
func AssessmentHook(scripts bool) DeviceHook
func (*Report) WriteAssessment(context.Context, wireless.Record, *wlan.Controller,
    rules.ControllerAssessment, basename, source, sourceSHA string,
    scripts bool) (wireless.Record, error)
func (*Report) FinalizeExecution(context.Context, wireless.Record) error
```

Default collection installs the assessment hook, which embeds the controller's
assessment in device JSON under `assessment`. `recommendations/` contains the
assessment JSON, a risk-ordered TXT report and deduplicated ordered
`verify.cli`. Without `--scripts` no change, command or rollback is embedded or
published. `--scripts` additionally writes wholly commented
`suggested.cfg`/`rollback.cfg`, and one schema-1 plan per candidate whose typed
change is still eligible on the decoded configuration. Plan contents are
byte-identical to the retired legacy writer (`report/plan_parity_test.go`
replays `testdata/typed/plan-oracle.json`); the one input where legacy planned
and the typed review does not carries an unmodeled configuration field. Source
path and hash bind to the final enriched device JSON, including its trailing
newline.

All artifacts use the existing private publisher. A writer returning nil error
must still provide a durable storage receipt. Execution intent and acknowledgement
use `WriteExecution`; each successful intent publication must finish before
sending its command. FinalizeExecution first durably stores an independent
`execution-checkpoint.json` with status `uncertain`, preserving steps and observed
readback, then publishes `execution.json` as `configuration_verified`. A committed
final failure or late cancellation restores uncertainty. If restoration fails,
the untrusted final file is removed where possible while the durable checkpoint
remains. `RecoveryFailed` reports failed recovery; CLI exits nonzero and never
replays commands to repair logs.

## CLI and native transport

The executable and its `internal/cli` command tree install actual review/apply
behavior and default advice. Explicit `Commands` and `RecommendationHook` (the
device hook selector, default `report.AssessmentHook`, or a ReportOptions hook)
remain injectable. Review accepts `--batch`, `--output`,
`--scripts`/`--no-scripts`, and help. It reads and validates all sorted source
files before creating a fresh private batch under `<batch>/reviews` or the
explicit output root. It preserves original source bytes and never alters their
reports. There is no review JSON-output flag. Apply accepts `--plan`, `--env-file`,
`--directory`, `--port`, and help; no unattended confirmation flag exists.

```go
// package change
type ApplySession interface {
    ReadConfiguration(context.Context) (Record, error)
    Write(context.Context, string) error
}
// package internal/bootstrap (application composition, not an embedding API)
type ApplyFactory func(context.Context, wireless.Record,
    connector.ConnectionConfig,
    func(context.Context, change.ApplySession) error) (operationErr, cleanupErr error)
// Dependencies.ApplyFactory optionally supplies this boundary.
// package netconnector
func Apply(context.Context, *connector.Device,
    func(context.Context, change.ApplySession) error) (operationErr, cleanupErr error)
```

`internal/workflow/apply.Run` owns the confirmed/precheck/intent/send/acknowledgement/readback/
cleanup/finalization sequence behind consumer-owned Opener and Journal interfaces.
CLI owns TTY checks, exact-file confirmation and presentation. Bootstrap resolves
credentials and binds transport factories only after confirmation.
Callback work retains the context supplied by the opener; new work requires both
the caller context and operation context to remain active. A completed write still
gets its durable acknowledgement before cancellation stops the next command.

ApplyFactory owns a synchronous borrowed-session callback and independent
operation/cleanup results. The runner guards the callback against replay and
post-return entry, joins admitted work, and retains callback errors even if a
factory drops them. A factory must not open more than once, retry, reconnect,
automatically roll back or save configuration. ReadConfiguration must return
exactly one complete parsed configuration, never partial facts. Native
`netconnector.Apply` connects once and owns one `WithBorrowedOperation` lease through
preparation, precheck, all writes, postread and retirement. It uses
`Operation.ExecuteCommand` with SkipPrivilege true, protected output and no
rejection preservation; failed Attempts remain separate from accepted Steps. It never nests adapter
collection sessions or leases. Native reads prepare screen length, bind the
exact query prompt, parse the existing H3C configuration grammar, and reject
partial evidence. Each write requires one confirmed matching command receipt.

All three default streams must be real terminals. Help runs before plan,
credential or terminal checks. Confirmation is the exact line
`APPLY <canonical-IP> <first-12-plan-SHA256>` (only its line terminator is removed).
`Runner.ReadLine(ctx)` preserves cancellation and borrowed input ownership.
Credentials load only after confirmation: automatic dotenv discovery remains,
explicit env-file is the final file overlay, process environment wins, public
YAML is not loaded, and SSH host trust is always strict. Native SSH compatibility
fallback/retry options remain disabled.

Apply saves confirmed state before opening, rechecks the complete full-config
fingerprint and exact eligibility, then saves each command intent before send
and each acknowledgement after send. Once an intent is durably saved (or its
publication is known committed), any later error/cancellation is `uncertain`,
including cancellation before the corresponding send is confirmed. No failure
causes a command retry, reconnect, automatic rollback or save. Readback truth
(`readback_verified`, `observed_config_sha256`) survives independent publication
or cleanup failures; only successful readback and final publication yield
`configuration_verified`. Exit 0 confirms configuration facts, not business
acceptance. Invalid/failing apply returns 2, wrong confirmation returns 1, and
caller cancellation returns 130.

Failure records retain the compatibility `status`/`error` summary and independent
`operation_errors`, `cancellation_errors`, `cleanup_errors`, and `report_errors`
arrays. Joined causes retain first-seen order, deduplicated by safe metadata.
Typed Failure code/stage and valid connector phases are preserved through
the shared internal classifier; unknown errors become allowlisted contextual fallbacks.
No arbitrary error text, transport output or connector Source field is retained.
Existing `cleanup_error` and first `report_error` summary fields remain.
A failure-record publication error is added to the safe stderr diagnostic record;
that newly observed publication diagnostic is **stderr-only**, not asserted to
be durable execution evidence. The previous intent checkpoint is preserved;
no device work is replayed and no extra failure-record write is attempted.


## Offline interop and verification

The default suite includes 16,138 independently generated finite binary64
oracles, canonical strings/integers, the actual Ruby Config/Snapshot/Report
fixture, recommendation parity (including source key order and historical scalar
names), native transport transcripts, durable publication faults and real PTYs.

Optional live bidirectional oracle (reference checkout remains read-only):

```sh
WIRELESS_RUBY_SOURCE=/path/to/ruby/wireless \
WIRELESS_RUBY=/path/to/ruby \
  go test ./internal/cli -run TestRubyChangeInterop -v
```

This uses the reference bundle and JSON 3.1.0, reviews a full Go collect report
in Ruby, compares exact full fingerprints/commands, and applies the Go plan in
Ruby via a real PTY and injected session. Go's default tests apply the original
Ruby-generated plan through both injected sessions and real PTY confirmation;
native transport tests use in-memory duplex transcripts. No test here is a
live-device, SSH-server, external-service or production acceptance claim.
`export-ruby-recommendations.rb` is an explicit development oracle that emits corpus JSON to stdout; it is never called by Go production
code or the ordinary test suite.

The apply workflow keeps progress in a private typed execution state. Journal and
terminal records are detached projections, including commands, device identity,
acknowledged steps and diagnostics. An initial steps list remains `[]`; pending
command, readback and error fields appear only when previously present in the wire
contract. Journal changes to a projected map cannot affect later device commands
or acknowledgements. `report.Error.PublicationCommitted()` exposes the commit
boundary through a consumer-owned interface; a committed intent failure remains
uncertain even when no device command was sent. Workflow error traversal uses
`internal/nativeerr` without importing the concrete connector or report packages.
