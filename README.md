# ts43-go

Private Go module for TS.43 eSIM Transfer flows. This repository contains the TS.43 client and the source-SIM driver pieces it needs; LPA/profile download code is intentionally left to the integrating application.

## Features

- TS.43 entitlement and carrier-managed eSIM transfer state machine.
- EAP-AKA relay using source SIM identity and AKA authentication.
- User-input, websheet, delayed-download, SM-DS discovery, and activation-polling states.
- Minimal APDU request/response helpers, SIM identity, EAP-AKA, TS.43 HTTP, HTTP logging, and source-SIM drivers used by the TS.43 flow.

## Packages

- `ts43`: TS.43 client, transfer state machine, request/response models, and websheet callback types.
- `sim`: SIM identity and EAP-AKA helpers needed by TS.43 authentication.
- `apdu`: minimal APDU request/response helpers used by source-SIM file reads.
- `http`: TS.43 HTTP client, raw response handling, and HTTP status errors.
- `driver`: minimal HTTP logging transport used by `http`.
- `driver/at`, `driver/ccid`, `driver/mbim`, `driver/qmi`: source-SIM drivers for identity lookup and AKA authentication.

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
