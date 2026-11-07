# Collection and native transport API

Package `github.com/gatework/go-wireless` adds collection without importing a
transport. Package `github.com/gatework/go-wireless/adapter` binds the pinned native
connector. Run `python3 scripts/prepare-workspace.py` before Go commands. The
unpublished native module placeholders require this verified local workspace.

```go
type Clock func() time.Time
type Session interface {
    State(context.Context) (bool, error)
    Execute(context.Context, string) (Receipt, error)
}
type OpenSession func(context.Context, func(Session) error) error
type CollectorOptions struct { Clock Clock; APFactory APFactory }
func NewCollector(CollectorOptions) *Collector
type CollectOptions struct {
    Target Record
    Platform, Model, ConnectorPlatform string
    Dimensions []Dimension
    APNames []string
    APCredentials APCredentialsSource
    Open OpenSession
}
func (*Collector) Collect(context.Context, CollectOptions) (Snapshot, error)
```

Open owns connect/callback/close. Sessions are borrowed: Execute must never
connect/retry/reenter implicitly. Open should call its callback synchronously;
repeated/concurrent callbacks are guarded and never replay commands. Collection
waits for an already admitted callback to complete. Retained callbacks invoked
after Open returns cannot submit commands. Errors become safe observations;
invalid inputs return errors before opening. Nil Dimensions selects declaration
order excluding LLDP; an explicit empty scope is invalid. LLDP adds AP inventory;
AP groups/service templates add configuration. Unsupported platform dimensions
remain explicit. The caller supplies the actual connector platform separately;
aliases normalize through the same profile registry.

```go
type ReceiptOptions struct {
    Command, Output, Body string
    Steps int
    Success bool
    Failure *Failure
}
func NewReceipt(ReceiptOptions) Receipt
func (Receipt) Output() string
func (Receipt) Body() string
func NewReader(Session, Clock) (*Reader, error)
func (*Reader) Read(context.Context, string) (Capture, error)
func (*Reader) Failure(context.Context) *Failure

type CaptureOptions struct {
    Command string
    Body *string
    StartedAt, CompletedAt time.Time
    Evidence []Record
    Failure *Failure
    Skipped bool
}
func NewCapture(CaptureOptions) (Capture, error)
func (Capture) Command() string
func (Capture) Body() string
func (Capture) StartedAt() time.Time
func (Capture) CompletedAt() time.Time
func (Capture) Evidence() []Record
func (Capture) Failure() *Failure
func (Capture) Success() bool
func (Capture) Skipped() bool
func (Capture) ToMap() Record
func NewFailure(code, stage string) *Failure
func (*Failure) Code() string
func (*Failure) Stage() string
func (*Failure) ToMap() Record
```

Receipts are immutable and opaque. Reader validates the requested command and
one successful step. Evidence hashes the exact receipt Output before decoding:
`command`, `sha256`, `bytes`, `observed_at` (UTC, six fractional digits). Body is
explicit and absent from formatting/JSON; failed captures cannot contain Body.
Evidence accessors return deep copies. A rejected command remains a failure and
can never acknowledge an apply command. A synchronized rejected query allows
independent later queries; closed/unknown state preserves the first failure and
skips subsequent commands. Reader rejects callback reentry without execution.
Failure vocabulary and stage are allowlisted; arbitrary error messages are never
serialized. Zero clocks are invalid. Typed interfaces eliminate Ruby's wrong-type
receipt/skipped/credential shapes; zero receipts and zero captures remain invalid.

```go
type APCredentials struct { Username, Password string }
type APCredentialsSource func(context.Context) (APCredentials, error)
type APOutcome struct {
    Result *Capture
    Error, CleanupError, CloseError *Failure
    Skipped bool
}
type APReader interface { Read(context.Context, Record) (APOutcome, error) }
type APFactory func(context.Context, Session, APCredentials) (APReader, error)
const LLDPCommand = "display lldp neighbor-information verbose"
```

AP credentials are resolved lazily only when selected inventory is nonempty and
an AP factory exists. The factory gets the actual borrowed controller as a
separate argument, which credentials cannot override. Each result must be one
valid capture for LLDP; skipped success is rejected. AP identity/command/status/
attempted/count are bound by collection, overriding adapter evidence claims.
Read, cleanup and close errors are independent, deduplicated by stable code while
successful captured neighbor facts survive cleanup errors.

```go
// package adapter
func Borrow(*connector.Device) (*Session, error)
func (*Session) CloseFailure() *wireless.Failure
func Open(*connector.Device) wireless.OpenSession
func APFactory(wireless.Clock) wireless.APFactory
func NewAPSession(*connector.Device, wireless.APCredentials, wireless.Clock) (*APSession, error)
func (*APSession) Read(context.Context, wireless.Record) (wireless.APOutcome, error)
```

Construct the native device through `vendors.Profile` + `connector.New` (inject
`connector.WithTransportFactory` for tests). `Borrow` itself never connects or
closes. Its Execute permits only the fixed profile preparation/query declarations,
binds the exact current prompt, skips privilege escalation, protects output,
retains rejected/partial evidence and requests rejection preservation. It owns
failure retirement within that query lease and retains the first close failure for
Open (or a custom host via CloseFailure), preserving it even if a later close succeeds. AP login
uses nested telnet through the controller, bounded sensitive credentials and one
`Device.WithOperation` around login/preparation/query/exit/retirement. AP session
must be online with a nonzero IPv4 management address and valid name. A busy lease
is untouched. Unknown prompt/cancellation retires without quit; only confirmed AP
state may send quit, and return must match the original controller prompt exactly.

Frozen connector additions (all opt-in; defaults preserved):
`ExecuteOptions.Borrowed`, `PreserveSessionOnRejection`, `CapturePartialOutput`;
`Device.WithOperationCleanup`, `Operation.ExecuteBorrowedCommand`,
`Operation.Retire`, `Operation.Connected`;
`StepResult.Body`. ExecuteBorrowedCommand is the only way to defer automatic close
to the lease owner; this private flag cannot be set via Device APIs. Retire works
with canceled contexts, rejects stale/reentrant operations, and prevents automatic
second retirement by WithOperation. Rejection preservation requires one complete
synchronized receipt, no cancellation, no output limit/callback/log error. Body
uses the existing native terminal renderer and removes only exact prompt/first
command echo. Apply must retain `Borrowed: true` (no reconnect), while leaving rejection
preservation disabled and explicitly classifying acknowledgement errors; failed/partial
collection receipts must never become apply acknowledgements.

No external devices, release, deployment or public dependency availability is
asserted by this API. The native connector uses the host SSH/telnet executables;
the wireless implementation and pinned libraries require no Ruby runtime.

Review round 1 lifecycle refinement: `Device.WithOperationCleanup(ctx, run)` returns
`(operationErr, closeErr error)`. It fixes both results while holding the device
lease, including fallback retirement after callback return or scope join. Session
and AP adapters use this boundary so late cancellation cannot discard close
failure. Plain WithOperation retains its original error-only interface. Apply
callers that need complete cleanup diagnostics should use WithOperationCleanup
and retain its two errors independently.

Collector opener errors may use `errors.Join` for independent primary and close
failures. Collection issues retain their stable codes in original order, including
cancellation/deadline and explicit Failure codes, with duplicate codes removed.
Only a generic untyped post-callback error is inferred as session_close_failed.

Final typed apply uses `adapter.Apply` and `wireless.ApplySession`, documented in
`docs/change-api.md`. It owns one `WithOperationCleanup` lease across preparation,
precheck, writes, readback and retirement; it never nests the collection adapter.

`adapter.SafeFailure(err, fallback, stage)` exposes the existing allowlisted
single-diagnostic mapping for apply hosts. It preserves typed Wireless stage and
validated native connector phase, omits arbitrary text/Source/cause, and leaves
ordered traversal of joined errors to the host. Existing collection mapping is
unchanged. See the independent diagnostic arrays in `docs/change-api.md`.
