package ts43

import "testing"

func TestODSAResponsePrimaryConfigurations(t *testing.T) {
	for _, tt := range []struct {
		name, body   string
		wantStatus   ServiceStatus
		wantICCID    string
		wantDownload bool
	}{
		{
			name:       "nested XML terminal response",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><parm name="OperationResult" value="1"/><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ICCID" value="8900000000000000000"/><parm name="ServiceStatus" value="4"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "JSON collection",
			body:       `{"APPLICATION":{"OperationResult":1,"PrimaryConfigurations":[{"PrimaryConfiguration":{"ICCID":"8900000000000000000","ServiceStatus":4}}]}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "JSON object container",
			body:       `{"APPLICATION":{"OperationResult":1,"PrimaryConfigurations":{"PrimaryConfiguration":{"ICCID":"8900000000000000000","ServiceStatus":4}}}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse, wantICCID: "8900000000000000000",
		},
		{
			name:       "XML first configuration wins",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="4"/></characteristic><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusDeactivatedNoReuse,
		},
		{
			name:       "JSON first configuration wins",
			body:       `{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":4}},{"PrimaryConfiguration":{"ServiceStatus":1}}]}}`,
			wantStatus: ServiceStatusDeactivatedNoReuse,
		},
		{
			name:       "standalone XML overrides collection",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="1"/></characteristic><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ServiceStatus" value="4"/></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusActivated,
		},
		{
			name:       "standalone JSON overrides collection",
			body:       `{"APPLICATION":{"PrimaryConfiguration":{"ServiceStatus":1},"PrimaryConfigurations":[{"PrimaryConfiguration":{"ServiceStatus":4}}]}}`,
			wantStatus: ServiceStatusActivated,
		},
		{
			name:       "nested download info",
			body:       `<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"><parm name="ICCID" value="8900000000000000000"/><parm name="ServiceStatus" value="1"/><characteristic type="DownloadInfo"><parm name="ProfileSmdpAddress" value="smdp.example"/></characteristic></characteristic></characteristic></characteristic></wap-provisioningdoc>`,
			wantStatus: ServiceStatusActivated, wantICCID: "8900000000000000000", wantDownload: true,
		},
		{
			name:       "download ICCID takes precedence",
			body:       `{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":{"ICCID":"old","ServiceStatus":1,"DownloadInfo":{"ProfileIccid":"new","ProfileSmdpAddress":"smdp.example"}}}]}}`,
			wantStatus: ServiceStatusActivated, wantICCID: "new", wantDownload: true,
		},
		{name: "empty collection", body: `{"APPLICATION":{"PrimaryConfigurations":[]}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got ODSAResponse
			if err := got.UnmarshalBinary([]byte(tt.body)); err != nil {
				t.Fatal(err)
			}
			if got.ServiceStatus != tt.wantStatus || got.Download.ProfileICCID != tt.wantICCID || got.DownloadInfoPresent != tt.wantDownload {
				t.Fatalf("status=%v ICCID=%q downloadInfo=%t, want %v %q %t", got.ServiceStatus, got.Download.ProfileICCID, got.DownloadInfoPresent, tt.wantStatus, tt.wantICCID, tt.wantDownload)
			}
			if tt.wantDownload && got.Download.SMDPFQDN != "smdp.example" {
				t.Errorf("SM-DP+ = %q", got.Download.SMDPFQDN)
			}
		})
	}
}

func TestODSAResponsePrimaryConfigurationsRejectsMalformedInput(t *testing.T) {
	for _, body := range []string{
		`{"APPLICATION":{"PrimaryConfigurations":[{"PrimaryConfiguration":1}]}}`,
		`{"APPLICATION":{"PrimaryConfigurations":{"PrimaryConfiguration":{"ServiceStatus":"invalid"}}}}`,
		`<wap-provisioningdoc><characteristic type="APPLICATION"><characteristic type="PrimaryConfigurations"><characteristic type="PrimaryConfiguration"></wap-provisioningdoc>`,
	} {
		var got ODSAResponse
		if err := got.UnmarshalBinary([]byte(body)); err == nil {
			t.Fatalf("accepted malformed response %s", body)
		}
	}
}
