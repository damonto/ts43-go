# ts43-go

A Go library for GSMA TS.43 service entitlement, carrier-managed eSIM transfers, and Wi-Fi Calling provisioning.

- pSIM-to-eSIM and eSIM-to-eSIM transfers with Google and Samsung carrier configurations.
- SIM authentication through [`wwan-go`](https://github.com/damonto/wwan-go) or your own backend.
- Wi-Fi Calling entitlement and carrier portal support.

Your application provides the SIM connection, user interface, and Local Profile Assistant (LPA). The [example CLI](examples) combines these to run a complete transfer.

## Installation

Requires **Go 1.26.3 or newer**.

```sh
go get github.com/damonto/ts43-go
```

The package name is `ts43`.

## Run the example

The example requires a PC/SC runtime, two distinct CCID readers, an accessible source SIM, and a target eUICC. It uses [`euicc-go`](https://github.com/damonto/euicc-go) to download and install the target profile.

```sh
git clone https://github.com/damonto/ts43-go.git
cd ts43-go/examples
go mod tidy
go run . -h
go run . -list-readers
```

Set `SOURCE_IMEI` and `TARGET_IMEI` to your devices' IMEIs, then run:

```sh
go run . \
  -carrier-config-dir /path/to/carrier-files \
  -source-reader "Source Reader" \
  -target-reader "Target Reader" \
  -source-imei "$SOURCE_IMEI" \
  -target-imei "$TARGET_IMEI"
```

The directory must contain `entitlement.json`. Add `-source-psim` for a physical source SIM. The example reads the target EID from the eUICC and prompts for any required user actions, including profile deletion.

Carrier portals need a browser or WebView supplied by your integration. The CLI prints the portal request and waits for its callback; [odsa_service_flow.js](examples/odsa_service_flow.js) provides the JavaScript bridge.

## Carrier configuration

Carrier records are supplied by your application; no catalog is bundled or downloaded by the library.

Decode your `entitlement.json` into `ts43.EntitlementConfigs` using `json.Unmarshal`, then pass it through `Config.Entitlements`. The client discovers carrier settings from the source SIM. You can also set `Config.Entitlement` to configure a carrier directly.

For Wi-Fi Calling, decode `wfc.json` into `ts43.WFCConfigs` and use `Discover` to select a configuration. Carrier availability depends on the supplied records and the carrier's authorization.

## Use the library

Create a source channel with `ts43.NewSource` from a `wwan-go` SIM card, or implement `ts43.Channel` for another SIM backend. See [examples/main.go](examples/main.go) for connection setup and cleanup.

With a loaded `catalog` (`*ts43.EntitlementConfigs`) and connected `sourceChannel`:

```go
client, err := ts43.New(&ts43.Config{
	Entitlements: catalog,
	Source: ts43.Endpoint{
		Channel: sourceChannel,
		Device:  ts43.Device{IMEI: sourceIMEI},
	},
	Target: ts43.Endpoint{
		Device: ts43.Device{IMEI: targetIMEI, EID: targetEID},
	},
})
if err != nil {
	return err
}
defer client.CloseIdleConnections()

result, err := client.Transfer(ctx)
```

Set `Entitlement.SourceSIMType` to `ts43.SIMTypePSIM` for a physical source SIM; the default is eSIM.

Handle `result.Event` to collect user input, open a portal, or install a profile. Resume with `client.Continue`, and call `client.CompleteActivation` after installation. [examples/transfer.go](examples/transfer.go) shows the complete event loop.

Keep the result in memory until the flow finishes. If a call returns both a result and an error, retain the result when handling a retry. Provide `Target.Channel` if authentication needs to be refreshed using the installed target profile.

Only call `CompleteActivation` after a requested download or when resuming pending activation. Invalid completion calls leave the transfer unchanged. Late profile-selection callbacks cannot restart an installed transfer.

Google download configurations with `ServiceStatus=1` carry an already-activated credential. The client waits for `CompleteActivation` to confirm installation, then finishes without another subscription update or activation query. Reusable downloads with `ServiceStatus=3` still require activation reporting; Samsung retains its own activation flow.

Google activation requests omit the transfer-only `app_name` and `target_terminal_*` fields. If an activation update is required and rejected, including `OperationResult=103`, the client returns `CarrierDeniedError` and retains the installed profile in its pending result.

Use `Config.HTTPClient` and `Config.Logger` to customize HTTP requests and logging. Debug logs can contain subscriber credentials and activation codes; redact them before sharing.

## Wi-Fi Calling

`CheckWFCEntitlement` checks Wi-Fi Calling status without requiring a transfer or target eUICC. Your application handles any carrier portal and the device's Wi-Fi Calling settings.

See [wfc.go](wfc.go) for the API and [vowifi_service_flow.js](examples/vowifi_service_flow.js) for the portal bridge.

## Development

Run these commands from the repository root and separately from `examples`:

```sh
go test -mod=readonly ./...
go vet -mod=readonly ./...
```

Tests use simulated SIM and HTTP behavior. Optional private-catalog checks use `TS43_CARRIER_CONFIG_DIR`.

## License

Licensed under the [GNU General Public License v3.0](LICENSE).
