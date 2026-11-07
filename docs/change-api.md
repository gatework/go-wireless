# Recommendations, plans, review and apply API

The ordinary Go runtime and `go test ./...` require no Ruby installation. These
APIs implement the frozen Ruby Wireless behavior; the fingerprint oracle is
Ruby 4.0.7 with **JSON gem 3.1.0**, including its bundled `fpconv.c`, not RFC 8785
or Go's ordinary JSON float formatting. Source hashes and fixture provenance are
in `docs/change-oracle.json`. The Boost Software License is retained in
`fpconv.go`, a pure-Go translation of that generator's Grisu2 implementation.

## Core package

```go
func CanonicalJSON(any) ([]byte, error)
func Fingerprint(any) (string, error)
func RenderChange(Record) (commands, rollback []string, err error)
func ChangeEligible(config, change Record) bool
func ChangeSatisfied(config, change Record) bool
func ValidateChangePlan(Record) error

type ObjectOrder map[uintptr][]string
func DecodeJSONDocument([]byte) (Record, ObjectOrder, error)
func DecodeReviewJSON([]byte) (Record, ObjectOrder, error)
func ValidateReviewDocument(Record) error
func Recommendations(Record, ...ObjectOrder) Record
```

Canonical JSON sorts object keys recursively and preserves array order. It
preserves arbitrary integer precision, distinguishes integer syntax from
`json.Number` decimal/exponent syntax, and encodes float64 using the Ruby
fpconv digit and formatting rules. Integral floats remain floats; negative zero
remains `-0.0`. UTF-8, HTML and U+2028/U+2029 remain unescaped, while JSON control
characters are escaped. Invalid UTF-8, malformed number tokens, nonfinite
numbers, unsupported values and cycles fail. Fingerprint is lowercase SHA256
of those exact canonical bytes, without newline.

The decoders require one JSON object and reject trailing content, nonfinite
numbers, invalid UTF-8 and malformed UTF-16 surrogate escapes. They preserve
number tokens and source object key order. Returned records and ObjectOrder are
caller-owned; the order is bound to those exact map instances and must not be
reused with a different document. Recommendations reads but does not mutate its
input and returns a deep-owned copy of device identity, advice and nested
evidence using the core record copier. Subsequent mutations of the input or
returned advice do not affect one another. Callers must not mutate the input
during the call. With ObjectOrder it preserves the Ruby source iteration
order of dimensions, nested reference evidence and resulting candidate indexes.
Without ObjectOrder, Go map keys use lexical order deterministically. Explicit
arrays always preserve their source order.

Review validation accepts generic historical device reports, including
`snapshot: null`, minimal observations without names/timestamps, and absent
optional fields. A present null or wrong-type container is not treated as an
absent field. It validates every container consumed by rules before output
creation, without constructing a live Snapshot with stronger invariants.

Recommendations covers incomplete evidence, legacy TKIP/WPA, missing or disabled
bound templates, historical IP conflicts, AP state and administratively down
radios. Every result is review-only, with no health assessment. The exact
`h3c_wireless` identity enables H3C rules; `h3c` remains a parsing/collection alias
but its recommendations are `platform_not_supported`, as in Ruby. Huawei never
receives H3C commands. Historical scalar template names can produce advisory
candidates under Ruby's name-string rule; only an actual valid string name can
become a typed plan or apply action. Historical numeric tokens are parsed before
Ruby-compatible scalar display (for example `1e+3` becomes `1000.0`); this display
conversion does not use or change canonical fingerprint formatting.

The only rendered action is `wireless_security`, with a known boolean enabled
state, an allowed 1–63 character template name, existing CCMP/RSN alternatives
and TKIP and/or WPA to remove. Rendering disables the template first and restores
its original enabled state. Eligibility compares original ordered protocol
arrays and enabled state. Satisfaction compares exact arrays after removing
TKIP/WPA and the original enabled state; CLI acknowledgement alone is insufficient.

Plan validation accepts schema 1, platform `h3c_wireless`, a canonical IP address,
a lowercase 64-digit expected fingerprint, and exact re-rendered commands and
rollback. It never executes arbitrary command text from a file. Source SHA256
is the hash of the source device JSON bytes; confirmation uses the distinct hash
of the exact plan file bytes.

## Report package

```go
func RecommendationHook(scripts bool) DeviceHook
func (*Report) WriteReview(context.Context, wireless.Record,
    basename, source, sourceSHA string, advice wireless.Record,
    scripts bool) (wireless.Record, error)
func (*Report) FinalizeExecution(context.Context, wireless.Record) error
```

Default collection installs the recommendation hook. Device JSON contains
diagnostics, and `recommendations/` contains JSON, TXT and deduplicated ordered
`verify.cli`. `--scripts` additionally preserves candidate commands/change data,
writes wholly commented `suggested.cfg`/`rollback.cfg`, and writes per-item
schema-1 plans only for eligible supported changes. Source path and hash bind to
the final enriched device JSON, including its trailing newline.

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

`cli.Run(ctx,args,Dependencies{})` and the executable install actual review/apply
behavior and default advice. Explicit `Commands` and `RecommendationHook` (or a
ReportOptions hook) remain injectable. Review accepts `--batch`, `--output`,
`--scripts`/`--no-scripts`, and help. It reads and validates all sorted source
files before creating a fresh private batch under `<batch>/reviews` or the
explicit output root. It preserves original source bytes and never alters their
reports. There is no review JSON-output flag. Apply accepts `--plan`, `--env-file`,
`--directory`, `--port`, and help; no unattended confirmation flag exists.

```go
// package wireless
type ApplySession interface {
    ReadConfiguration(context.Context) (Record, error)
    Write(context.Context, string) error
}
// package cli
type ApplyFactory func(context.Context, wireless.Record,
    connector.ConnectionConfig,
    func(wireless.ApplySession) error) (operationErr, cleanupErr error)
// Dependencies.ApplyFactory optionally supplies this boundary.
// package adapter
func Apply(context.Context, *connector.Device,
    func(wireless.ApplySession) error) (operationErr, cleanupErr error)
```

ApplyFactory owns a synchronous borrowed-session callback and independent
operation/cleanup results. The runner guards the callback against replay and
post-return entry, joins admitted work, and retains callback errors even if a
factory drops them. A factory must not open more than once, retry, reconnect,
automatically roll back or save configuration. ReadConfiguration must return
exactly one complete parsed configuration, never partial facts. Native
`adapter.Apply` connects once and owns one `WithOperationCleanup` lease through
preparation, precheck, all writes, postread and retirement. It uses
`ExecuteBorrowedCommand` with Borrowed true, SkipPrivilege true, protected output,
partial-receipt capture, and no rejection preservation. It never nests adapter
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
`adapter.SafeFailure`; unknown errors become allowlisted contextual fallbacks.
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
  go test ./cli -run TestRubyChangeInterop -v
```

This uses the reference bundle and JSON 3.1.0, reviews a full Go collect report
in Ruby, compares exact full fingerprints/commands, and applies the Go plan in
Ruby via a real PTY and injected session. Go's default tests apply the original
Ruby-generated plan through both injected sessions and real PTY confirmation;
native transport tests use in-memory duplex transcripts. No test here is a
live-device, SSH-server, external-service or production acceptance claim.
`export-ruby-fpconv.rb` and `export-ruby-recommendations.rb` are explicit development
oracles that emit corpus JSON to stdout; they are never called by Go production
code or the ordinary test suite.
