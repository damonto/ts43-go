# ts43-go

A Go library for GSMA TS.43 eSIM transfers and Wi-Fi Calling provisioning.

Supports pSIM-to-eSIM and eSIM-to-eSIM transfers with Google carrier configurations. Your application supplies the SIM connection, carrier configuration, user interface, and Local Profile Assistant (LPA).

## Installation

Requires **Go 1.26.3 or newer**.

```sh
go get github.com/damonto/ts43-go
```

The package name is `ts43`.

## Run the example

The [example CLI](examples) requires PC/SC, two distinct CCID readers, a source SIM, and a target eUICC.

```sh
git clone https://github.com/damonto/ts43-go.git
cd ts43-go/examples
go mod tidy
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

The directory must contain `entitlement.json`. Add `-source-psim` for a physical source SIM. Use `-h` to see all options.

For carrier portals, provide a browser or WebView using the [JavaScript bridge](examples/odsa_service_flow.js).

## Carrier configuration

Your application supplies `entitlement.json` for eSIM transfers and `wfc.json` for Wi-Fi Calling. Load them with `json.Unmarshal` into `ts43.EntitlementConfigs` or `ts43.WFCConfigs`.

## Use the library

Create a source channel with `ts43.NewSource` from a [`wwan-go`](https://github.com/damonto/wwan-go) SIM card, or implement `ts43.Channel` for your backend.

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

Handle `result.Event`, resume with `client.Continue`, and call `client.CompleteActivation` after installation. See [examples/transfer.go](examples/transfer.go) for the complete flow, including user input and carrier portals.

## Wi-Fi Calling

`CheckWFCEntitlement` checks Wi-Fi Calling status without a target eUICC. Your application handles the carrier portal and device settings.

See [wfc.go](wfc.go) for the API and [vowifi_service_flow.js](examples/vowifi_service_flow.js) for the portal bridge.

## Development

Run these commands from the repository root and separately from `examples`:

```sh
go test -mod=readonly ./...
go vet -mod=readonly ./...
```

## License

Licensed under the [GNU General Public License v3.0](LICENSE).
