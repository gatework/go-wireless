# Frozen build dependencies

These are private/local source snapshots, **not evidence of public versions**.
The root module uses `v0.0.0` placeholders; `scripts/prepare-workspace.py` verifies
and materializes them under `.work/`, then writes a relative `go.work` with
version-specific replacements. No sibling source directory is needed to build.

`manifest.json` records SHA256 for each archive, every original source file,
every patched source file, and each applied patch. `SHA256SUMS` covers the
tracked dependency artifacts. Both archives contain regular files only, rooted
in the module name. Inputs are strictly allowlisted production Go code, required embedded templates,
`go.mod`, `go.sum`, and LICENSE; tests, binaries, reports, captures, credential
files, Git data, documentation archives and private backups are excluded.

## Source provenance

- **go-net-connector**: working source at HEAD
  `4f3774de892f1907531e48117d2eba4a413f02fc`, frozen on 2026-10-26.
  The 82 original allowlisted files include all production library packages
  needed by collection, CLI, inventory, Netdisco, resources, pools and apply,
  plus transitive packages and required embedded TextFSM templates. Commands,
  examples and internal CLI sources are excluded. This was a
  dirty pre-existing workspace, so per-file working-source hashes are authoritative.
- **go-expect**: the verified dependency candidate actually used by that
  go-net-connector workspace. Its original `dependencies/go-expect-source.tar.gz`,
  `patches/go-expect-compat.patch`, original manifest, and candidate manifest hashes
  are recorded under `upstream_inputs`. Repackaging retains 11 original production
  files; applying the filtered production compatibility delta reproduces the
  15-file candidate byte-for-byte. The original candidate manifest was checked
  against every source file before extraction.
- The current sibling go-expect checkout does **not** provide the connector's
  required `SendAll`, `SendAllPoll`, `Poll`, `WithOSPolling`, declaration-priority,
  streaming-fallback and streaming-redactor APIs. The parent approved using the
  connector's actual frozen candidate instead. Sibling HEAD and working-file hashes
  are retained as provenance with the incompatibility reason. No unused sibling
  archive is retained and no sibling files were edited.

## Wireless connector delta

`go-net-connector-wireless.patch` changes only the frozen copy and adds:

- `ExecuteOptions.Borrowed`: fail on a disconnected borrowed device; no auto-connect.
- `PreserveSessionOnRejection`: one synchronized failed query may keep its session
  and exact receipt. Cancellation, callback/logging errors, output limits and
  arbitrary failures cannot become healthy rejection state. Default rejection
  still drops the receipt and closes as before.
- `CapturePartialOutput`: retain explicit failed/partial receipts, including
  pending unconsumed bytes and invalid encoding. These remain errors, not command
  acknowledgements. Default paths remain unchanged.
- `Operation.ExecuteBorrowedCommand`: only a live operation can defer automatic
  closure to its owner. The corresponding option is private, never exposed to
  Device callers. `Operation.Retire` retires under the existing lease even after
  cancellation; stale, retired and observer-reentrant calls are rejected. An
  explicitly retired operation cannot reconnect or be automatically retired again
  by WithOperation. The existing closeResources may itself make two close attempts
  after a failed underlying Close; this inherited behavior is recorded by tests.
- `Operation.Connected` and `StepResult.Body`: read the current state and reuse
  the existing terminal renderer. Body removes only the matched prompt and first
  command echo, retaining indentation, later identical lines and the newline
  consumed by Go's prompt boundary (Ruby uses a zero-width boundary).

Native tests are in `adapter/native_test.go`, outside the frozen source archives.
They use local `net.Pipe` byte streams through `WithTransportFactory`; no network
devices are contacted. They check the patch through real native execution,
including defaults, rejected/partial receipts, AP dialogue/pagination, cancellation,
cleanup/close diagnostics, exact prompts, busy/stale/reentrant/retired leases and
body rendering. All 15 native library packages compile from this same snapshot. The wireless adapter preserves the first retirement close error
separately, even if an outer owner's later close succeeds.

No upstream publication, release, live device compatibility, or deployment is
asserted. Linux/macOS platform builds and independent-consumer gates remain the
final integration stage. The native connector intentionally uses host SSH/telnet
executables; no Ruby runtime or second SSH engine is introduced.

Review round 1 adds `Device.WithOperationCleanup(ctx, run) (operationErr, closeErr error)`
to retain the first close result through the entire owned-operation exit, including
fallback retirement triggered after the callback returns. Wireless query and AP
adapters consume both results. The error-only WithOperation wrapper stays compatible.
The bootstrap now validates the complete strict unified-diff form and all target
paths against the original/patched allowlists before invoking patch. It preflights
all destinations including go.work, stages the workspace file, and renames through
a rollback journal. Existing directories/files are retained until the complete
publication succeeds; recovery failure leaves original backups for manual recovery.
