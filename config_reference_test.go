package ts43

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestWFCReferenceConfiguration(t *testing.T) {
	var rows []wfcRecord
	if err := json.Unmarshal(privateCarrierJSON(t, "wfc.json"), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		t.Run(row.CarrierName+"/"+row.MCCMNC, func(t *testing.T) {
			data := referenceFile(t, filepath.Join("refs/adevtool/vendor-skels/google_devices/tokay/proprietary/product/etc/CarrierSettings", row.CarrierName+".textproto"))
			for _, field := range []struct{ name, want string }{
				{"entitlement_server_url_string", strconv.Quote(row.EntitlementServer)},
				{"default_service_entitlement_status_bool", strconv.FormatBool(row.DefaultServiceEntitlementEnabled)},
				{"skip_wfc_activation_bool", strconv.FormatBool(row.SkipActivation)},
				{"show_vowifi_webview_bool", strconv.FormatBool(row.ShowVoWiFiWebview)},
			} {
				pattern := regexp.MustCompile(`key: "imsserviceentitlement\.` + regexp.QuoteMeta(field.name) + `"\s+(?:text|bool)_value: ("(?:[^"\\]|\\.)*"|true|false)`)
				match := pattern.FindSubmatch(data)
				got := "false"
				if len(match) > 1 {
					got = string(match[1])
				}
				if got != field.want {
					t.Errorf("%s=%s, want reference %s", field.name, field.want, got)
				}
			}
		})
	}
}

type googleReferenceRecord struct {
	MCCMNC, CarrierName string
	MatchRules          []map[string][]string
	Entitlement         map[string]json.RawMessage
}

func TestGoogleReferenceConfiguration(t *testing.T) {
	var source []googleReferenceRecord
	if err := json.Unmarshal(referenceFile(t, "refs/entitlement.json"), &source); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(privateCarrierJSON(t, "entitlement.json"), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		var id struct {
			MCCMNC, CarrierName                               string
			GID1, GID2, SPN, IMSIPrefixXPattern, PreferredAPN *string
		}
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &id); err != nil {
			t.Fatal(err)
		}
		t.Run(id.MCCMNC+"/"+id.CarrierName, func(t *testing.T) {
			index := slices.IndexFunc(source, func(s googleReferenceRecord) bool {
				return s.MCCMNC == id.MCCMNC && s.CarrierName == id.CarrierName
			})
			if index < 0 {
				// Free 20815 is a deliberate supplemental record from carrier-entitlement.json.
				if id.MCCMNC == "20815" && id.CarrierName == "Free" {
					return
				}
				t.Fatal("Google reference record missing")
			}
			ref := source[index]
			for _, key := range []string{"entitlementServer", "appName", "entitlementVersion", "transferTypePsim", "transferTypeEsim", "useHttpPost", "useSmdsDelayedDownload", "useTempTokenConversion", "setTargetTerminalFields", "acquireConfigurationPollingIntervalMinutes", "acquireConfigurationPollingTimeoutMinutes"} {
				if string(row[key]) != string(ref.Entitlement[key]) {
					t.Errorf("%s=%s, want %s", key, row[key], ref.Entitlement[key])
				}
			}
			matched := len(ref.MatchRules) == 0
			for _, rule := range ref.MatchRules {
				ok := true
				for key, value := range map[string]*string{"gid1": id.GID1, "gid2": id.GID2, "spn": id.SPN, "imsiPrefixXpattern": id.IMSIPrefixXPattern, "preferredApn": id.PreferredAPN} {
					alternatives := rule[key]
					if value == nil {
						ok = ok && len(alternatives) == 0
					} else {
						ok = ok && slices.ContainsFunc(alternatives, func(candidate string) bool {
							if key == "gid1" || key == "gid2" {
								return strings.EqualFold(candidate, *value)
							}
							return candidate == *value
						})
					}
				}
				matched = matched || ok
			}
			if !matched {
				t.Errorf("identity is not an expansion of reference rules: %s", data)
			}
		})
	}
}

func referenceFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("local reference corpus absent: %s", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}
