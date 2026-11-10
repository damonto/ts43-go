package ts43

import (
	"fmt"
	"net/http"
	"testing"
)

func TestGoogleOldTerminalFieldsByPhase(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, restricted := range []bool{false, true} {
			for _, tt := range []struct {
				transferType TransferType
				transferOld  bool
				updateOld    bool
			}{
				{TransferTypeTemporaryToken, false, false},
				{TransferTypeReusableActivationCode, true, false},
				{TransferTypeTS43V8, true, false},
				{TransferTypeOldTerminalIDs, true, true},
				{TransferTypeTemporaryTokenOldTerminalIDs, true, true},
				{TransferTypeTemporaryTokenVariant11, false, false},
			} {
				t.Run(fmt.Sprintf("post_%v/restricted_%v/type_%d", post, restricted, tt.transferType), func(t *testing.T) {
					offer := alignmentOffer()
					calls := 0
					client := newClient(Config{
						Entitlement: Entitlement{
							Server:                    "https://carrier.example",
							TransferType:              tt.transferType,
							UsePost:                   post,
							RestrictOldTerminalFields: restricted,
						},
						HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
							fields := alignmentRequestFields(t, req)
							wantOld := !restricted || tt.transferOld
							wantOperation := OperationTypeTransferSubscription
							if calls == 1 {
								wantOld = !restricted || tt.updateOld
								wantOperation = OperationTypeUpdateSubscription
							}
							if got := fields.Get("operation_type"); got != wantOperation.String() {
								t.Errorf("operation_type=%q, want %s", got, wantOperation)
							}
							for key, value := range map[string]string{
								"old_terminal_id":    offer.SourceDevice.IMEI,
								"old_terminal_iccid": offer.SourceDevice.ICCID,
							} {
								if !wantOld {
									value = ""
								}
								if got := fields.Get(key); got != value {
									t.Errorf("operation %s: %s=%q, want %q", wantOperation, key, got, value)
								}
							}
							if got := fields.Get("old_terminal_entitlement_protocol"); got != "1" {
								t.Errorf("old_terminal_entitlement_protocol=%q, want 1", got)
							}
							calls++
							return response(http.StatusOK, `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="AppID" value="ap2009"/><parm name="OperationResult" value="1"/><parm name="SubscriptionResult" value="3"/></characteristic></wap-provisioningdoc>`), nil
						}),
					})
					credential := authCredential(offer.AuthToken)
					if transferTypeUsesTemporaryToken(tt.transferType) {
						credential = temporaryCredential(TemporaryToken{Value: "temporary"})
					}
					if _, err := client.manageSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential, UserInputResult{Button: MessageButtonUnknown}); err != nil {
						t.Fatal(err)
					}
					if _, err := client.updateSubscription(t.Context(), offer.SourceIdentity, offer.SourceDevice, offer.TargetDevice, credential); err != nil {
						t.Fatal(err)
					}
					if calls != 2 {
						t.Fatalf("requests=%d, want 2", calls)
					}
				})
			}
		}
	}
}
