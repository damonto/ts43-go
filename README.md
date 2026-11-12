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

No carrier catalog is bundled. Decode your Google transfer records from `entitlement.json` into `ts43.EntitlementConfigs` using `json.Unmarshal`, then pass them through `Config.Entitlements`. For direct configuration, set `Config.Entitlement` instead.

Transfer requests follow the inspected GMS 26.44.63 implementation. Carrier records supply `autoRetry511Limit`, `setCheckEligibilityTargetTerminalId`, `supportRetryAfter`, and `useServerRetryAfterDelay`. An omitted `useServerRetryAfterDelay` defaults to true in catalog records; explicit false selects the carrier interval. With retry support enabled, download-configuration polling retries HTTP 503 using the server's `Retry-After` seconds when configured, or the carrier polling interval when the header is missing or invalid. Waiting stays within `PollTimeout`; keep a non-nil result returned with an error to resume using `Continue`.

For entitlement version 14.0 or later, set `Device.IMEIs` to the target's full IMEI list. Requests join these values with commas in `target_terminal_id`, falling back to `Device.IMEI` when the list is empty. `CheckEligibility` includes this identifier when the version enables it or the carrier requests it. Transfer types 2, 9, and 11 still follow `setTargetTerminalFields` for download requests. The request's `terminal_id` remains the device's primary `IMEI`.

Transfer operations populate only `target_terminal_id` and `target_terminal_eid` as target fields. Types 3, 6, and 7 place the EID in `target_terminal_eid`; types 2, 9, and 11 use `terminal_eid` and may also send the target fields when configured. The installed profile's ICCID is sent with the post-installation subscription update.

Both request parameters and `User-Agent` truncate the device vendor, model, and software version to 4, 10, and 20 UTF-16 code units respectively, matching GMS. For example, `Google` becomes `Goog` and `Pixel 10 Pro XL` becomes `Pixel 10 P`.

`enableDelayedEsimPushNotification` and `supportsMultipleTransfer` are preserved as carrier capabilities. Push registration and delivery are not implemented, and each `Transfer` handles one subscription.

For Wi-Fi Calling, decode `wfc.json` into `ts43.WFCConfigs` and select a configuration with `Discover`.

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

Owner-only carrier regressions use `TS43_CARRIER_CONFIG_DIR`. For a full Google
comparison against the current device snapshot, also set `TS43_GMS_SNAPSHOT`
to its Phenotype `.pb` and `TS43_AOSP_CARRIER_LIST` to the binary AOSP
`CarrierList`. Ordinary tests skip those comparisons when the inputs are absent.

## License

Licensed under the [GNU General Public License v3.0](LICENSE).
