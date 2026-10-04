# ts43-go

Private Go module for TS.43 eSIM Transfer flows. This repository contains the TS.43 client and the source-SIM adapter it needs; UICC transports and LPA/profile download code are intentionally left to integrating modules.

## Features

- TS.43 entitlement and carrier-managed eSIM transfer state machine.
- Wi-Fi Calling entitlement, provisioning status, VERS resets, and portal callbacks.
- EAP-AKA relay using source SIM identity and AKA authentication.
- User-input, websheet, delayed-download, SM-DS discovery, and activation-polling states.
- TS.43 EAP-AKA relay helpers, HTTP client behavior, and a small source-SIM adapter over `github.com/damonto/wwan-go`.

## Packages

- root package: TS.43 client, transfer state machine, request/response models, websheet callback types, EAP-AKA relay helpers, HTTP client behavior, and the `wwan-go/sim.Card` source adapter.

Source-SIM transports live in `github.com/damonto/wwan-go`. Open the reader there, load a `sim.Card`, then adapt it for TS.43:

```go
tx, err := ccid.Open(ctx, readerName)
reader, err := sim.NewReader(tx)
card, err := sim.New(ctx, reader, logger)
channel, err := ts43.NewSource(card)
```

## Example

The transfer example lives in `examples` as a separate module. It depends on the full `github.com/damonto/euicc-go` module so it can reuse the complete eSIM transfer CLI, including target LPA download and source profile deletion.

```sh
cd examples
go run . -h
go run . -source-reader "Source Reader" -target-reader "Target Reader" ...
```

## Verification

```sh
go test ./...
(cd examples && go test ./...)
```

## Carrier configuration and continuation

`entitlement.json` uses Google records wherever a Google MCCMNC exists. Samsung
records only fill missing MCCMNCs. Explicit Samsung discovery therefore returns
`ErrEntitlementNotFound` for a Google-only MCCMNC, even if `refs` also contains a
Samsung configuration. Identity qualifiers remain mandatory; missing Google
SPN/GID/IMSI qualifiers do not enable a Samsung fallback. The embedded database
contains 520 Google rows and 18 Samsung rows.

`wfc.json` follows the tokay CarrierSettings snapshot in `refs`. Other device
snapshots can differ. `WFCConfig.NeedsIMSProvisioning` explicitly enables VERS
reset behavior; the default setup-query mode requires WFC application data.
Empty HTTP bodies and responses containing only another service are errors.
Use `ShouldShowAddressUpdate` for emergency-address updates: `SkipActivation`
applies to initial setup. `Client.ContinueWFC` rechecks entitlement after a
completion callback and preserves the prior status on cancellation. The adapter
in `examples/vowifi_service_flow.js` accepts `window.TS43_WFC_CLIENT_NAME` and
installs the standard Google and TS.43 bridge aliases.

Keep the returned transfer `Result` and call `Client.Continue` when the carrier
signals readiness. `StatePendingConfiguration` / `ConfigurationPendingEvent`
means download data is still being prepared. `StatePendingActivation` /
`ActivationPendingEvent` means installation has finished and activation is being
confirmed. Both states support `Continue`, including after a polling error;
activation confirmation never resubmits a successful subscription update. `Target.Channel` is needed to
refresh expired or rejected credentials after installing the target profile.
Results retain an in-memory session; serializing `Result` does not persist it.

Samsung extra request fields and portal fields have separate capability flags.
Provide `Device.IMEIs`, `SerialNumber`, `Model`, and, for LG U+, `BarcodeNumber`
and `ActivatedID`. Samsung configurations routed by the reference client to
Android Ts43Operation (Ice Norway and Verizon Wireless in the supplemental list)
return `ErrAndroidEntitlementRequired` before making an incompatible HTTP request.
LPA operations, Android backends, notifications, and persistent provisioning
scheduling remain integration responsibilities. HTTP errors retain their response
headers, including Retry-After, for that scheduler.

The invalid-input result 104 is named `OperationResultInvalidMessageResponse`
and returns `CarrierDeniedError`, preserving the server response and error text.
The previous misleading `OperationResultGoogleDelayedDownload` name was removed.
An explicit empty carrier `appName` is preserved; custom configurations can set
`OmitAppName` to request the same behavior.

`config_reference_test.go` cross-checks the embedded records against the local
reference corpus when it is present. The channel-deduplication test always runs.
