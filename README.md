# ts43-go

A Go library for GSMA TS.43 service entitlement, carrier-managed eSIM transfers, and Wi-Fi Calling provisioning.

The library authenticates with a source SIM through EAP-AKA and coordinates the carrier's transfer flow. Your application supplies the SIM connection, user interface, and Local Profile Assistant (LPA) for downloading and installing the target profile. A complete transfer CLI is included in [examples](examples).

## Features

- Carrier configuration discovery from caller-supplied Google and Samsung records.
- pSIM-to-eSIM and eSIM-to-eSIM transfer flows, including temporary tokens and carrier-specific request formats.
- Transfer events for user input, carrier websheets, source profile deletion, profile download, SM-DS discovery, and activation confirmation.
- Wi-Fi Calling entitlement, terms and conditions, emergency-address status, provisioning status, and portal callbacks.
- EAP-AKA relay, JSON and XML response decoding, and configurable HTTP transport and logging.
- A source-SIM adapter for [`wwan-go`](https://github.com/damonto/wwan-go).

Carrier configuration records describe supported flows; a matching record does not guarantee that a carrier will authorize a particular subscription or device.

## Installation

Requires **Go 1.26.3 or newer**, as declared in [go.mod](go.mod).

From your application's Go module:

```sh
go get github.com/damonto/ts43-go
```

Import the package as `github.com/damonto/ts43-go`; its package name is `ts43`. After adding imports, run `go mod tidy` to update your module's dependencies.

## Run the transfer example

The [examples](examples) directory is a separate Go module. It uses `wwan-go` for source-SIM access and [`euicc-go`](https://github.com/damonto/euicc-go) for target profile download, installation, and notifications.

You need a PC/SC runtime, two distinct CCID readers, an accessible source SIM that supports AKA authentication, and a target eUICC. Use the source and target devices' IMEIs. The example reads the target EID from the eUICC unless you supply `-target-eid`.

```sh
git clone https://github.com/damonto/ts43-go.git
cd ts43-go/examples
go mod tidy
go run . -h
go run . -list-readers
```

Set `SOURCE_IMEI` and `TARGET_IMEI` to your devices' IMEIs, then start an eSIM-to-eSIM transfer using the reader names from `-list-readers`:

```sh
go run . \
  -carrier-config-dir /path/to/private-files \
  -source-reader "Source Reader" \
  -target-reader "Target Reader" \
  -source-imei "$SOURCE_IMEI" \
  -target-imei "$TARGET_IMEI"
```

For a physical source SIM, add `-source-psim`. If you omit reader names, the example prompts you to select them.

By default, the example discovers the entitlement configuration from the source SIM. Supplying `-server` bypasses discovery; use the other flags shown by `-h` to provide the carrier's settings when configuring a server manually.

The example prompts for carrier messages, websheet callbacks, download confirmation, and source profile deletion when required. A transfer can change the active subscription, and confirming source profile deletion removes that profile from the source eUICC.

Carrier websheets run in a browser or WebView that you provide. The CLI prints the URL, request content type, and user data, then waits for a callback. [odsa_service_flow.js](examples/odsa_service_flow.js) provides an ODSA JavaScript bridge whose emitted callback messages can be pasted into the CLI.

## Use the library

### Discover carrier configuration

Discovery matches a caller-supplied catalog without contacting a carrier. This example loads private files from the current directory:

```go
package main

import (
	"fmt"
	"encoding/json"
	"log"
	"os"

	"github.com/damonto/ts43-go"
)

func main() {
	entitlementJSON, err := os.ReadFile("entitlement.json")
	if err != nil {
		log.Fatal(err)
	}
 var entitlements ts43.EntitlementConfigs
 if err := json.Unmarshal(entitlementJSON, &entitlements); err != nil { log.Fatal(err) }
	identity := ts43.Identity{
		MCC: "262",
		MNC: "01",
		SPN: "Telekom.de",
	}
	entitlement, err := entitlements.Discover(identity, ts43.SIMTypeESIM)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Channel: %s\n", entitlement.Channel)
	fmt.Printf("Server: %s\n", entitlement.Server)
	fmt.Printf("Transfer type: %s\n", entitlement.TransferType)
}
```

For a real subscription, read the full identity from the source SIM. Carrier matching uses MCC/MNC and, where specified, IMSI, GID1, GID2, SPN, and preferred APN. Missing qualifiers can prevent a match. Use `SIMTypePSIM` for a physical source SIM.

### Connect a source SIM

`Config.Source.Channel` accepts a `ts43.Channel`, which supplies the SIM identity and performs AKA authentication. To use `wwan-go`, open a transport, create a `sim.Card`, and pass the card to `ts43.NewSource`. See [examples/main.go](examples/main.go) for the complete CCID setup and resource cleanup.

`Source.Close` closes the underlying card and its reader. Applications using another SIM backend can implement `Channel` directly.

### Drive the transfer

1. Call `ts43.New` with a `*ts43.Config`. Provide `Source.Channel`, the source and target `Device.IMEI` values, and the target `Device.EID`. Set `Entitlement.SourceSIMType` to match the source; its zero value is `SIMTypeESIM`.
2. Leave `Entitlement.Server` empty to discover carrier settings when `Client.Transfer(ctx)` reads the source identity. Alternatively, pass a discovered or manually configured `Entitlement` with a server URL.
3. Call `Client.Transfer(ctx)` and inspect the returned `*ts43.Result` and its `Event`.
4. Perform the requested action, then resume with `Client.Continue` or `Client.CompleteActivation` as shown below. Repeat until `DoneEvent` or `DismissEvent`.

| Event | Application action | Resume with |
| --- | --- | --- |
| `UserInputEvent` | Display the message and collect the requested response and button. | `Continue(ctx, result, ContinueRequest{UserInput: answer})` |
| `WebsheetEvent` | Open the carrier websheet with its user data and collect its callback. | `Continue(ctx, result, ContinueRequest{Websheet: callback})` |
| `SourceProfileDeletionEvent` | Confirm and delete the specified source profile through your LPA, including required notifications. | `Continue(ctx, result, ContinueRequest{SourceProfileDeleted: true})` |
| `DownloadReadyEvent` | Download, install, and enable the profile using `event.Config`; handle LPA notifications. | `CompleteActivation(ctx, result, ActivationResult{ICCID: installedICCID})` |
| `SMDSDiscoveryEvent` | Discover the profile through SM-DS, then download, install, and enable it. | `CompleteActivation(ctx, result, ActivationResult{ICCID: installedICCID})` |
| `DelayedDownloadEvent` | Complete installation through the carrier-provided download path. | `CompleteActivation(ctx, result, ActivationResult{ICCID: installedICCID})` |
| `ConfigurationPendingEvent` | Wait for the carrier to prepare download configuration. | `Continue(ctx, result, ContinueRequest{})` |
| `ActivationPendingEvent` | Wait for the carrier to confirm activation. | `Continue(ctx, result, ContinueRequest{})` |
| `DoneEvent` | The transfer is complete. | No further call. |
| `DismissEvent` | The carrier dismissed the flow. | No further call. |

The table omits the `ts43.` prefix for readability. `answer` and `callback` are pointers to `UserInputResult` and `WebsheetResult`, respectively. See [examples/transfer.go](examples/transfer.go) for a complete event loop.

Keep the returned result in memory throughout the flow. It contains an unexported session; JSON serialization does not preserve a resumable transfer. Configuration and activation polling can return both a result and an error. Retain the result so your application can retry an appropriate pending state after handling the error. Continuing activation confirmation does not resubmit an already accepted subscription update.

Provide `Target.Channel` when the flow needs to refresh expired or rejected credentials using the newly activated target profile. The example CLI does not configure this channel.

### HTTP and logging

`Config.HTTPClient` accepts an HTTP client; leaving it nil uses the library's default client. Standard `*http.Client` values with a nil or `*http.Transport` transport are cloned and configured for TS.43. Custom transports must provide compatible TLS settings themselves. Call `Client.CloseIdleConnections` when finished with the client.

Use `Config.Logger` to supply a `*slog.Logger`. Debug logging includes HTTP URLs, headers, and bodies, which can contain subscriber identifiers, authentication tokens, and activation codes. Redact these before sharing logs.

## Wi-Fi Calling

Wi-Fi Calling uses the supplied WFC records and does not require a target eUICC or a transfer.

1. Decode `wfc.json` into `ts43.WFCConfigs`, read the source `Identity`, and call `configs.Discover(identity)`.
2. Call the package-level `ts43.CheckWFCEntitlement(ctx, cfg, req)` with `cfg.Source.Channel` and a `WFCEntitlementRequest` containing the identity, device metadata, and discovered `WFCConfig`. This entry point does not require the transfer fields validated by `ts43.New`.
3. Inspect `WFCResult.Status`. Methods such as `Entitled`, `InProgress`, and `Incompatible` describe the provisioning state. Use `WFCConfig.ShouldShowWebFlow` to decide whether to open the activation portal, and `ShouldShowAddressUpdate` for emergency-address updates.
4. If your application already uses a `Client`, call its `CheckWFCEntitlement` method and pass the previous result and portal callback to `Client.ContinueWFC`. An entitlement-change callback rechecks the carrier; cancellation preserves the previous status and marks the result as cancelled. With the package-level API, issue another entitlement check after portal completion and pass the previous `AuthToken` when appropriate.

[vowifi_service_flow.js](examples/vowifi_service_flow.js) provides the portal bridge aliases. Set `window.TS43_WFC_CLIENT_NAME` to `WFCConfig.WebsheetClientName` before injecting it when the carrier specifies a custom bridge name. `WebsheetResult.UnmarshalText` parses the emitted callback messages.

Set `WFCConfig.NeedsIMSProvisioning` when your integration requires VERS-driven resets to carrier defaults. By default, entitlement checks require WFC application data. Your application remains responsible for changing the device's Wi-Fi Calling setting and implementing IMS service support.

## Carrier compatibility and limitations

- Carrier configuration is supplied by the integrating application. Decode entitlement arrays into `EntitlementConfigs` and WFC arrays into `WFCConfigs` with `json.Unmarshal` or `UnmarshalBinary`. Set `Config.Entitlements` for transfer discovery; WFC consumers receive their own `WFCConfigs`. Both types implement JSON and binary marshal/unmarshal interfaces; binary encoding is the same UTF-8 JSON array. No private dataset is embedded or downloaded by this library.
- Transfer discovery tries Google records before Samsung records. The supplied catalog can include Samsung entries only for MCC/MNC values absent from the Google list. Explicit Samsung discovery through `DiscoverEntitlementForChannel` can therefore return `ErrEntitlementNotFound` for an MCC/MNC covered by Google.
- Use `errors.Is` to distinguish `ErrEntitlementNotFound`, `ErrEntitlementAmbiguous`, and `ErrEntitlementUnsupported`. WFC discovery exposes `ErrWFCConfigNotFound` and `ErrWFCConfigAmbiguous`.
- Samsung flows can require `Device.IMEIs`, `SerialNumber`, and `Model`; LG U+ can additionally require `BarcodeNumber` and `ActivatedID`. Extra HTTP terminal parameters and websheet parameters have separate capability flags in `Entitlement`.
- Configurations that require Android's `Ts43Operation` backend return `ErrAndroidEntitlementRequired`. That backend is outside this library's scope.
- Transfer type 8 requires LPA metadata lookup and source profile deletion outside the TS.43 flow; `Transfer` returns `ErrSMDPTransferRequiresLPA` for this type.
- Carrier denials return `*CarrierDeniedError`, retaining the carrier response and error text. Non-2xx HTTP responses return `*HTTPError`, retaining response headers, including `Retry-After`, for your application's retry scheduler.
- LPA operations, browser/WebView hosting, persistent scheduling, and transfer recovery across process restarts are application responsibilities.

## Development

Run the library tests from the repository root:

```sh
go test ./...
```

Test the example module separately:

```sh
cd examples
go mod tidy
go test ./...
```

The unit tests use simulated SIM and HTTP behavior. Owner-only tests that compare private carrier records with a local `refs` corpus skip those comparisons when the corpus is absent; it is not required to build or use the library.

## Caller-supplied carrier files

The example accepts `-carrier-config-dir /path/to/private-files`. Carrier files are not included in this repository. The integrating application owns loading, authorization and any remote delivery. Public tests use synthetic inputs; owner-only catalog regression tests accept `TS43_CARRIER_CONFIG_DIR`.

## License

Licensed under the [GNU General Public License v3.0](LICENSE).

