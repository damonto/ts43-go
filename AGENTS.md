# AGENTS.md

Repository instructions for changes anywhere in this project. Follow more specific task instructions and any applicable instructions in subdirectories.

## Project scope

- `github.com/damonto/ts43-go` is a Go library; the root package is `ts43`. Use the Go version declared in [go.mod](go.mod) and keep code compatible with it.
- The library owns TS.43 requests, EAP-AKA relay, carrier discovery, transfer state, and Wi-Fi Calling entitlement. SIM transports belong to `wwan-go`; LPA operations, profile installation, UI hosting, and persistent scheduling belong to integrating applications.
- [examples](examples) is a separate Go module with a local replacement for the root module. It uses `euicc-go` for the transfer CLI. Root `./...` commands do not include this module.
- Keep the flat package layout. Add focused files beside related code; introduce packages or dependencies only when the requested change justifies them.

## Where to work

Read the implementation and nearby tests relevant to the task; use [README.md](README.md) for user-facing setup and usage.

| Concern | Main files |
| --- | --- |
| Public API and transfer lifecycle | `types.go`, `client.go`, `service.go`, `service_test.go`, `service_resume_test.go` |
| Request construction and carrier differences | `request.go`, `operation.go`, `google_request.go`, `samsung_request.go`, `request_response_test.go` |
| Response and value codecs | `response.go`, `constants.go`, `encoding_test.go`, `conformance_test.go` |
| Carrier matching and supplied data | `identity.go`, `entitlement.go`, `carrier_config.go`, `config_reference_test.go` |
| Wi-Fi Calling | `wfc.go`, `wfc_response.go`, `wfc_test.go`, `wfc_response_test.go` |
| SIM authentication and HTTP | `source.go`, `aka.go`, `http_client.go`, and their matching test files |
| Websheet callbacks and CLI integration | `websheet.go`, `websheet_result.go`, `examples/*.go`, `examples/*_service_flow.js` |

## Go conventions

- Follow the [Google Go Style Guide](https://google.github.io/styleguide/go/), including its Style Decisions and Best Practices. Format changed Go files with `gofmt`; use `goimports` to organize imports when installed.
- Prefer the standard library and existing dependencies. Use `slices` and `maps` when they make the code clearer; straightforward loops are appropriate when they express the operation better.
- Keep control flow explicit, use early returns, and choose concrete types where practical. Use small interfaces at dependency boundaries such as `Channel` and `HTTPDoer`.
- Keep library files in `package ts43` and runnable examples in `package main`. Preserve exported APIs and defaults unless the task changes their contract; update tests and usage documentation together.
- Wrap errors with `%w` when adding useful context; preserve sentinel errors, typed errors, and underlying causes. Use `errors.Is` and typed error inspection rather than comparing error strings. Error messages should describe the action without redundant prefixes such as "failed to".
- Accept `context.Context` as the first parameter of operations that can block. Preserve cancellation through SIM calls, HTTP requests, and polling. Give goroutines a defined shutdown path and close resources at their owning boundary.
- Library code returns errors; process exits and interactive prompts belong in the CLI. Explain protocol constraints and non-obvious choices in comments, and document exported API contracts.
- Verify unfamiliar APIs against pinned dependency source or official documentation, using Context7 when available. If a library is not indexed, inspect its source in the Go module cache or its official documentation. Do not guess APIs.

## Compatibility contracts

- **Wire behavior:** Preserve Google and Samsung request differences, including authentication mode, source/target identifiers, EID placement, and operation-specific fields. Omitted, empty, and zero values can have different meanings, including `OmitAppName` and explicit message buttons. Cover changed GET and POST behavior in tests.
- **Transfer continuation:** Configuration preparation and activation confirmation are distinct pending states. Preserve a non-nil resumable `Result` alongside polling errors where supported. Resuming after an accepted subscription update must not submit that update again. Results retain an in-memory session; do not imply that serializing them enables process-restart recovery.
- **Credentials:** Keep source authentication tokens and temporary transfer tokens separate. Preserve expiry, cookies, permitted operations, and bounded rejection retries. Post-installation credential refresh may require `Target.Channel`; do not silently reuse source-SIM authentication in its place.
- **Carrier discovery:** Preserve identity qualifiers and ambiguity/unsupported errors. Google matching and Samsung/WFC matching use different rules. Supplied transfer records prefer Google coverage, with Samsung records only for MCC/MNC values absent from the Google list. When updating data, identify the upstream source and intended behavior in the change summary; do not invent records or relax matching to force a result.
- **Wi-Fi Calling:** Keep WFC usable without transfer-only target fields. Preserve cancellation status, VERS reset behavior, and the distinction between initial activation and emergency-address updates. A successful portal callback triggers an entitlement recheck; cancellation preserves the previous entitlement state.
- **HTTP transport:** Preserve caller-owned clients and transports, explicit TLS settings, supported carrier compatibility, response-body restoration after logging, and response headers in `HTTPError`. Keep unsupported Android-backend and transfer-type-8 flows explicit rather than routing them through an incompatible HTTP operation.
- **Encoding and AKA:** Preserve protocol numeric values and standard text/binary codec contracts. Test malformed input and decoder reuse where relevant. Keep known authentication vectors passing; do not replace protocol cryptography with an ad hoc scheme.

## Tests and verification

Use native `testing`, table-driven cases, and `t.Run` for related scenarios. Extend nearby tests or add a focused regression test for changed behavior. Test externally observable results, emitted requests, and state transitions rather than duplicating implementation details.

Reuse existing fake channels and HTTP doers where their behavior fits. Keep ordinary tests independent of carrier services, PC/SC, and physical SIMs. Use synthetic subscriber data, and use `Config.Now` and `Config.Sleep` for deterministic expiry and polling tests instead of wall-clock delays.

Run focused tests during implementation. For Go changes, run these checks in each affected module; root API or dependency changes can also affect `examples` through its local replacement.

From the repository root:

```sh
go test -mod=readonly ./...
go vet -mod=readonly ./...
```

From `examples`:

```sh
go test -mod=readonly ./...
go vet -mod=readonly ./...
```

- Use `go test -race -mod=readonly ./...` for concurrency changes when supported. For CLI flag or usage changes, also run `go run -mod=readonly . -h` from `examples`.
- Run `go mod tidy` in each affected module when the task changes imports or dependencies, and review both module files and checksums. Avoid unrelated version upgrades. For an existing dependency mismatch outside the task, report it and use an isolated copy or alternate module files if needed for verification.
- Tests in `config_reference_test.go` skip reference comparisons when the optional local `refs` corpus is absent. Keep ordinary tests independent of it, and report skipped reference checks when relevant to carrier-data changes. Do not add that corpus as a runtime dependency.
- For documentation-only edits, check referenced paths, commands, and examples; skip Go test suites when no executable behavior or examples changed. Before finishing, check the diff for unintended edits and whitespace issues with `git diff --check`.

## Working boundaries and handoff

- Preserve unrelated working-tree changes. Keep patches focused on the requested behavior and update README examples when public usage changes.
- Use fake SIM and HTTP behavior for routine verification. Perform live carrier transfers, profile downloads, activation, or deletion only when the task explicitly calls for those operations.
- Keep real subscriber identifiers, tokens, AKA material, and activation codes out of fixtures and shared output. Existing HTTP debug logs contain sensitive data; redact them before including logs in reports.
- Explain the outcome and material design choices in plain language. Report the checks actually run, skipped checks, and unresolved limitations. Do not claim tests or live-carrier compatibility without evidence.
- Keep this file concise and update it when a documented command or contract changes. Leave task-specific plans, progress notes, and exhaustive API details out of repository instructions.

Carrier catalogs are supplied by the integrating application. Keep synthetic parser and matching tests independent of private files; owner-only catalog regressions may use `TS43_CARRIER_CONFIG_DIR`. Do not restore carrier JSON embeds.
