package ts43

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func privateCarrierJSON(t *testing.T, name string) []byte {
	t.Helper()
	dir := os.Getenv("SIGMO_CARRIER_CONFIG_DIR")
	if dir == "" {
		t.Skip("private carrier catalog regression requires SIGMO_CARRIER_CONFIG_DIR")
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func privateEntitlementConfigs(t *testing.T) *EntitlementConfigs {
	t.Helper()
	var configs EntitlementConfigs
	if err := json.Unmarshal(privateCarrierJSON(t, "entitlement.json"), &configs); err != nil {
		t.Fatal(err)
	}
	return &configs
}

func privateWFCConfigs(t *testing.T) *WFCConfigs {
	t.Helper()
	var configs WFCConfigs
	if err := json.Unmarshal(privateCarrierJSON(t, "wfc.json"), &configs); err != nil {
		t.Fatal(err)
	}
	return &configs
}
