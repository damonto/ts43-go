package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/damonto/ts43-go"
)

func TestRunTransferPropagatesContinuationErrors(t *testing.T) {
	transportErr := errors.New("carrier connection interrupted")
	tests := []struct {
		name       string
		manageBody string
		input      string
		wantErr    error
		wantCalls  []ts43.Operation
	}{
		{
			name:       "user input continuation",
			manageBody: `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="8"/><characteristic type="MSG"><parm name="Message" value="Confirm transfer"/></characteristic></characteristic></wap-provisioningdoc>`,
			wantErr:    transportErr,
			wantCalls:  []ts43.Operation{"", ts43.OperationCheck, ts43.OperationManage, ts43.OperationManage},
		},
		{
			name:       "websheet continuation",
			manageBody: `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="1"/><parm name="SubscriptionServiceURL" value="https://example.test/websheet"/></characteristic></wap-provisioningdoc>`,
			input:      "DONE AcquireConfiguration\n",
			wantErr:    transportErr,
			wantCalls:  []ts43.Operation{"", ts43.OperationCheck, ts43.OperationManage, ts43.OperationAcquireConf},
		},
		{
			name:       "websheet dismissal",
			manageBody: `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="1"/><parm name="SubscriptionServiceURL" value="https://example.test/websheet"/></characteristic></wap-provisioningdoc>`,
			input:      "DISMISS\n",
			wantErr:    ts43.ErrWebsheetDismissed,
			wantCalls:  []ts43.Operation{"", ts43.OperationCheck, ts43.OperationManage},
		},
		{
			name:       "delayed download activation",
			manageBody: `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="4"/></characteristic></wap-provisioningdoc>`,
			input:      "8900000000000000000\n",
			wantErr:    transportErr,
			wantCalls:  []ts43.Operation{"", ts43.OperationCheck, ts43.OperationManage, ts43.OperationManage},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodies := []string{
				`<wap-provisioningdoc><characteristic type="TOKEN"><parm name="token" value="auth"/></characteristic></wap-provisioningdoc>`,
				`<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><parm name="PrimaryAppEligibility" value="1"/></characteristic></wap-provisioningdoc>`,
				tt.manageBody,
			}
			var calls []ts43.Operation
			client, err := ts43.New(&ts43.Config{
				HTTPClient: transferTestDoer(func(req *http.Request) (*http.Response, error) {
					calls = append(calls, ts43.Operation(req.URL.Query().Get("operation")))
					if len(calls) > len(bodies) {
						return nil, transportErr
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(bodies[len(calls)-1])),
					}, nil
				}),
				Entitlement: ts43.Entitlement{
					Server:  "https://example.test/ts43",
					Channel: ts43.EntitlementChannelSamsung,
				},
				Source: ts43.Endpoint{Channel: transferTestChannel{}, Device: ts43.Device{IMEI: "111111111111111"}},
				Target: ts43.Endpoint{Device: ts43.Device{IMEI: "222222222222222", EID: "89049032000001000000000000000000"}},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			reader := bufio.NewReader(strings.NewReader(tt.input))
			err = runTransfer(t.Context(), reader, io.Discard, client, nil, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("runTransfer() error = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("HTTP operations = %v, want %v", calls, tt.wantCalls)
			}
		})
	}
}

type transferTestDoer func(*http.Request) (*http.Response, error)

func (f transferTestDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type transferTestChannel struct{}

func (transferTestChannel) Identity(context.Context) (*ts43.Identity, error) {
	return &ts43.Identity{ICCID: "8910000000000000000", IMSI: "310260123456789", MCC: "310", MNC: "260"}, nil
}

func (transferTestChannel) AuthenticateAKA(context.Context, *ts43.AKARequest) (*ts43.AKAResponse, error) {
	return nil, errors.New("unexpected AKA challenge")
}
