package ts43

import (
	"fmt"
	"net/http"
	"testing"
)

func TestTransferResponseNegotiation(t *testing.T) {
	for _, post := range []bool{false, true} {
		for _, tt := range []struct {
			name                string
			channel             EntitlementChannel
			token, accept, want string
		}{
			{name: "Google authentication", want: EAPRelayContentType + ", " + ODSAResponseAccept},
			{name: "Google operation", token: "auth", want: ODSAResponseAccept},
			{name: "Google explicit JSON", token: "auth", accept: "application/json", want: "application/json"},
			{name: "Samsung authentication", channel: EntitlementChannelSamsung, want: EAPRelayContentType},
			{name: "Samsung operation", channel: EntitlementChannelSamsung, token: "auth", want: SamsungResponseAccept},
		} {
			t.Run(fmt.Sprintf("post_%v/%s", post, tt.name), func(t *testing.T) {
				client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example", Channel: tt.channel, UsePost: post}})
				offer := alignmentOffer()
				request := client.baseRequest(offer.SourceDevice, tt.token, "")
				if tt.accept != "" {
					request.Accept = tt.accept
				}
				built, err := buildHTTPRequest(t.Context(), client.transfer.entitlement, offer.SourceIdentity, request, ODSAOperation{}, client.clientTS43(), client.realm())
				if err != nil {
					t.Fatal(err)
				}
				if got := built.Header.Get("Accept"); got != tt.want {
					t.Fatalf("Accept=%q, want %q", got, tt.want)
				}
			})
		}
	}
}

func TestGoogleTransferRelayNegotiatesXML(t *testing.T) {
	client := newClient(Config{Entitlement: Entitlement{Server: "https://carrier.example"}, HTTPClient: fakeDoer(func(req *http.Request) (*http.Response, error) {
		if got, want := req.Header.Get("Accept"), EAPRelayContentType+", "+ODSAResponseAccept; got != want {
			t.Fatalf("Accept=%q, want %q", got, want)
		}
		return response(200, `<wap-provisioningdoc/>`), nil
	})})
	if _, _, err := client.postRelay(t.Context(), "synthetic-eap-packet", ServiceEntitlementRequest{App: AppODSAPrimary}, nil); err != nil {
		t.Fatal(err)
	}
}
