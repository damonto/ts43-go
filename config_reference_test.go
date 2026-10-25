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

func TestEntitlementChannelPolicy(t *testing.T) {
	var rows []entitlementRecord
	if err := json.Unmarshal(privateCarrierJSON(t, "entitlement.json"), &rows); err != nil {
		t.Fatal(err)
	}
	google := make(map[string]bool)
	for _, row := range rows {
		if row.Channel == "google" {
			google[row.MCCMNC] = true
		}
	}
	for _, row := range rows {
		t.Run(row.Channel+"/"+row.MCCMNC+"/"+row.CarrierName, func(t *testing.T) {
			if row.Channel == "samsung" && google[row.MCCMNC] {
				t.Fatalf("Samsung duplicates Google MCCMNC %s", row.MCCMNC)
			}
		})
	}
}

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

type samsungReference struct {
	Versions []struct {
		Data struct {
			Countries []struct {
				Operators []samsungOperator `json:"operatorList"`
			} `json:"countryDataList"`
		} `json:"configData"`
	} `json:"odaConfigVersionList"`
}

type samsungOperator struct {
	Name     string
	Vendor   int              `json:"esVendor"`
	Networks []samsungNetwork `json:"nwInfo"`
	Settings []samsungSetting `json:"esSettings"`
}

type samsungNetwork struct{ MCC, MNC, GID1, GID2, SPN, Subset string }

type samsungSetting struct {
	Type, EsURL, EsVersion string
	Params                 []struct {
		Name  string
		Value stringField
	} `json:"esOptionalParamList"`
	Subscriptions []struct {
		Name  string
		Value bool
	} `json:"subscriptionType"`
}

func samsungReferenceSetting(operators []samsungOperator, row entitlementRecord) (samsungOperator, samsungSetting, bool) {
	for _, op := range operators {
		if op.Name != row.CarrierName {
			continue
		}
		if !slices.ContainsFunc(op.Networks, func(network samsungNetwork) bool {
			return network.MCC+network.MNC == row.MCCMNC && network.GID1 == optionalString(row.GID1) && network.GID2 == optionalString(row.GID2) && network.SPN == optionalString(row.SPN) && network.Subset == optionalString(row.IMSIPrefixXPattern)
		}) {
			continue
		}
		for _, setting := range op.Settings {
			if setting.Type == "PROD" && setting.EsURL == row.EntitlementServer {
				return op, setting, true
			}
		}
	}
	return samsungOperator{}, samsungSetting{}, false
}

func TestSamsungReferenceConfiguration(t *testing.T) {
	var source samsungReference
	if err := json.Unmarshal(referenceFile(t, "refs/com.samsung.oda.service/assets/prod/170000/oda_operator_list_default.json"), &source); err != nil {
		t.Fatal(err)
	}
	var operators []samsungOperator
	for _, version := range source.Versions {
		for _, country := range version.Data.Countries {
			operators = append(operators, country.Operators...)
		}
	}

	var rows []entitlementRecord
	if err := json.Unmarshal(privateCarrierJSON(t, "entitlement.json"), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Channel != "samsung" {
			continue
		}
		t.Run(row.MCCMNC+"/"+row.CarrierName, func(t *testing.T) {

			op, setting, found := samsungReferenceSetting(operators, row)
			if !found {
				t.Fatal("no matching reference operator/network/PROD setting")
			}
			params := make(map[string]string)
			for _, v := range setting.Params {
				params[v.Name] = v.Value.String()
			}
			subscriptions := make(map[string]bool)
			for _, v := range setting.Subscriptions {
				subscriptions[v.Name] = v.Value
			}
			for _, field := range []struct {
				name      string
				got, want bool
			}{
				{"http post", row.UseHTTPPost, params["USE_POST_HTTP_API"] == "true"},
				{"extra terminal", row.SamsungExtraTerminalParams, params["NEED_EXTRA_TERMINAL_PARAMS"] == "true"},
				{"websheet injection", row.SamsungInjectWebsheetParams, params["NEED_INJECT_EXTRA_PARAMS"] == "true"},
				{"LGU injection", row.SamsungInjectWebsheetParamsLGU, params["NEED_INJECT_EXTRA_PARAMS_LGU"] == "true"},
				{"legacy UA", row.SamsungLegacyUserAgent, params["TS43_USERAGENT_NOTSUPPORT"] == "true"},
				{"Android backend", row.SamsungAndroidEntitlement, op.Vendor == 4 || op.Vendor == 8},
				{"pSIM support", row.SupportPsimToEsimTransfer, subscriptions["DIGITALIZED_SIM"]},
				{"eSIM support", row.SupportEsimToEsimTransfer, subscriptions["D2D_TRANSFER_AKA"] || subscriptions["D2D_TRANSFER_TEMP_TOKEN"]},
				{"4FF token", row.UseTempTokenConversion, params["4FF_TEMP_TOKEN"] == "true"},
			} {
				if field.got != field.want {
					t.Errorf("%s=%v, want %v", field.name, field.got, field.want)
				}
			}
			if value := params["CUSTOM_READ_TIMEOUT"]; value != "" {
				want, err := strconv.Atoi(value)
				if err != nil {
					t.Fatal(err)
				}
				if row.TimeoutSeconds != want {
					t.Errorf("timeout=%d, want %d", row.TimeoutSeconds, want)
				}
			}
			if row.EntitlementVersion != setting.EsVersion || row.ProvisioningVersion != params["TS43_PROVISIONING_VERSION"] {
				t.Errorf("version=%q/%q, want %q/%q", row.EntitlementVersion, row.ProvisioningVersion, setting.EsVersion, params["TS43_PROVISIONING_VERSION"])
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
			MCCMNC, Channel, CarrierName                      string
			GID1, GID2, SPN, IMSIPrefixXPattern, PreferredAPN *string
		}
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &id); err != nil {
			t.Fatal(err)
		}
		if id.Channel != "google" {
			continue
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

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestSamsungAndroidBackendGuard(t *testing.T) {
	for _, mccmnc := range []string{"24214", "312012"} {
		t.Run(mccmnc, func(t *testing.T) {
			ent, err := privateEntitlementConfigs(t).DiscoverForChannel(Identity{MCC: mccmnc[:3], MNC: mccmnc[3:]}, SIMTypeESIM, EntitlementChannelSamsung)
			if err != nil {
				t.Fatal(err)
			}
			if !ent.SamsungAndroidEntitlement {
				t.Fatal("Android backend choice lost")
			}
			if _, err := buildHTTPRequest(t.Context(), ent, Identity{}, ServiceEntitlementRequest{}, ODSAOperation{}, "", ""); !errors.Is(err, ErrAndroidEntitlementRequired) {
				t.Fatalf("backend error = %v", err)
			}
		})
	}
}
