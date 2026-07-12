# ts43-go

Private Go module for TS.43 eSIM Transfer flows. This repository contains the TS.43 client and the source-SIM adapter it needs; UICC transports and LPA/profile download code are intentionally left to integrating modules.

## Features

- TS.43 entitlement and carrier-managed eSIM transfer state machine.
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
