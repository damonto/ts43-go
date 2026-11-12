package ts43

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryRetainsPollingDefaultsAndCarrierRetryLimit(t *testing.T) {
	for _, retry := range []int{0, 2} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			configs := &EntitlementConfigs{records: []entitlementRecord{{MCCMNC: "23420", EntitlementServer: "https://carrier.example", SupportEsimToEsimTransfer: true, TransferTypeEsim: 2, AutoRetry511Limit: &retry}}}
			client := newClient(Config{Entitlements: configs})
			if err := client.discoverEntitlement(Identity{MCC: "234", MNC: "20"}); err != nil {
				t.Fatal(err)
			}
			ent := client.transfer.entitlement
			if ent.PollTimeout != 10*time.Minute || ent.PollInterval != time.Minute || ent.AutoRetry511Limit != retry {
				t.Fatalf("discovered defaults: interval=%s timeout=%s retries=%d", ent.PollInterval, ent.PollTimeout, ent.AutoRetry511Limit)
			}
		})
	}
}

func TestConfigCodecs(t *testing.T) {
	for _, tt := range []struct {
		name  string
		data  string
		value interface {
			encoding.BinaryMarshaler
			encoding.BinaryUnmarshaler
		}
	}{
		{"entitlement", `[{"mccmnc":"00101","carrierName":"test","entitlementServer":"https://one.example","supportEsimToEsimTransfer":true,"transferTypeEsim":7}]`, new(EntitlementConfigs)},
		{"wfc", `[{"mccmnc":"00101","carrierName":"test","entitlementServer":"https://wfc.example"}]`, new(WFCConfigs)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			zero, err := json.Marshal(tt.value)
			if err != nil || string(zero) != "[]" {
				t.Fatalf("zero = %s, %v", zero, err)
			}
			input := []byte(tt.data)
			if err := tt.value.UnmarshalBinary(input); err != nil {
				t.Fatal(err)
			}
			clear(input)
			original, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			binary, err := tt.value.MarshalBinary()
			if err != nil || !bytes.Equal(binary, original) {
				t.Fatalf("binary = %s, %v", binary, err)
			}
			clear(binary)
			for _, data := range []string{"", "null", "{}", "[] garbage", "[", `[1]`} {
				if err := tt.value.UnmarshalBinary([]byte(data)); err == nil {
					t.Errorf("accepted %q", data)
				}
				got, err := json.Marshal(tt.value)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("invalid decode changed config: %s, %v", got, err)
				}
			}
			if err := json.Unmarshal(original, tt.value); err != nil {
				t.Fatal(err)
			}
			got, err := tt.value.MarshalBinary()
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("roundtrip = %s, %v", got, err)
			}
			identity := Identity{MCC: "001", MNC: "01"}
			switch config := tt.value.(type) {
			case *EntitlementConfigs:
				found, err := config.Discover(identity, SIMTypeESIM)
				if err != nil || found.Server != "https://one.example" {
					t.Fatalf("entitlement = %+v, %v", found, err)
				}
			case *WFCConfigs:
				found, err := config.Discover(identity)
				if err != nil || found.Server != "https://wfc.example" {
					t.Fatalf("WFC = %+v, %v", found, err)
				}
			}
		})
	}
}

func TestMissingConfig(t *testing.T) {
	var entitlements *EntitlementConfigs
	if _, err := entitlements.Discover(Identity{MCC: "001", MNC: "01"}, SIMTypeESIM); !errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("entitlement = %v", err)
	}
	var wfc *WFCConfigs
	if _, err := wfc.Discover(Identity{MCC: "001", MNC: "01"}); !errors.Is(err, ErrWFCConfigNotFound) {
		t.Fatalf("WFC = %v", err)
	}
}

func TestDiscoverRetryAfterDelayDefault(t *testing.T) {
	var configs EntitlementConfigs
	for _, tt := range []struct {
		name, field string
		want        bool
	}{
		{"explicit true", `,"useServerRetryAfterDelay":true`, true},
		{"explicit false", `,"useServerRetryAfterDelay":false`, false},
		{"omitted after false", "", true},
		{"null uses default", `,"useServerRetryAfterDelay":null`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := fmt.Appendf(nil, `[{"mccmnc":"00101","entitlementServer":"https://carrier.example","supportEsimToEsimTransfer":true,"transferTypeEsim":2,"supportRetryAfter":true%s}]`, tt.field)
			if err := json.Unmarshal(data, &configs); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				ent, err := configs.Discover(Identity{MCC: "001", MNC: "01"}, SIMTypeESIM)
				if err != nil {
					t.Fatal(err)
				}
				if !ent.SupportRetryAfter || ent.UseServerRetryAfterDelay != tt.want {
					t.Fatalf("retry support=%t server delay=%t, want true and %t", ent.SupportRetryAfter, ent.UseServerRetryAfterDelay, tt.want)
				}
				encoded, err := configs.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				if err := configs.UnmarshalBinary(encoded); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGoogleTransferCapabilitiesSurviveConfigRoundTrip(t *testing.T) {
	var configs EntitlementConfigs
	// Reuse the decoder so false values cannot retain an earlier capability.
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			data := fmt.Appendf(nil, `[{"mccmnc":"00101","carrierName":"test","entitlementServer":"https://carrier.example","supportEsimToEsimTransfer":true,"transferTypeEsim":2,"setCheckEligibilityTargetTerminalId":%[1]t,"supportRetryAfter":%[1]t,"useServerRetryAfterDelay":%[1]t,"enableDelayedEsimPushNotification":%[1]t,"supportsMultipleTransfer":%[1]t}]`, enabled)
			if err := json.Unmarshal(data, &configs); err != nil {
				t.Fatal(err)
			}
			encoded, err := configs.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &records); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"setCheckEligibilityTargetTerminalId", "supportRetryAfter", "useServerRetryAfterDelay", "enableDelayedEsimPushNotification", "supportsMultipleTransfer"} {
				if got := string(records[0][field]); got != fmt.Sprint(enabled) {
					t.Errorf("%s=%s, want %t", field, got, enabled)
				}
			}
			var restored EntitlementConfigs
			if err := restored.UnmarshalBinary(encoded); err != nil {
				t.Fatal(err)
			}
			for _, catalog := range []*EntitlementConfigs{&configs, &restored} {
				ent, err := catalog.Discover(Identity{MCC: "001", MNC: "01"}, SIMTypeESIM)
				if err != nil {
					t.Fatal(err)
				}
				if ent.SetCheckEligibilityTargetTerminalID != enabled || ent.SupportRetryAfter != enabled || ent.UseServerRetryAfterDelay != enabled || ent.EnableDelayedESIMPushNotification != enabled || ent.SupportsMultipleTransfer != enabled {
					t.Fatalf("discovery lost transfer capabilities: %+v", ent)
				}
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

type googleReferenceRecord struct {
	MatchRules  []map[string][]string
	Entitlement map[string]any
}

func TestGoogleReferenceConfiguration(t *testing.T) {
	snapshot := os.Getenv("TS43_GMS_SNAPSHOT")
	carrierList := os.Getenv("TS43_AOSP_CARRIER_LIST")
	if snapshot == "" || carrierList == "" {
		t.Skip("current Google reference requires TS43_GMS_SNAPSHOT and TS43_AOSP_CARRIER_LIST")
	}
	var rows []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(privateCarrierJSON(t, "entitlement.json")))
	decoder.UseNumber()
	if err := decoder.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected data after carrier catalog: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Google reference comparison requires a nonempty catalog")
	}
	source := currentGoogleReference(t, snapshot, carrierList)
	defaults := googleReferenceEntitlement(t, nil)
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var identity entitlementRecord
		if err := json.Unmarshal(data, &identity); err != nil {
			t.Fatal(err)
		}
		// An empty display name is valid in Google's catalog; absence is not.
		if identity.MCCMNC == "" || row["carrierName"] == nil {
			t.Fatalf("catalog identity missing: %s", data)
		}
		t.Run(identity.MCCMNC+"/"+identity.CarrierName, func(t *testing.T) {
			var differences []string
			found := false
			for _, ref := range source[identity.CarrierName] {
				if !ref.matchesIdentity(identity.match()) {
					continue
				}
				found = true
				differences = nil
				for key, want := range ref.Entitlement {
					got := row[key]
					if got == nil {
						got = defaults[key]
					}
					if !reflect.DeepEqual(got, want) {
						differences = append(differences, fmt.Sprintf("%s=%v, want %v", key, got, want))
					}
				}
				if len(differences) == 0 {
					return
				}
			}
			if !found {
				t.Fatalf("identity is not an expansion of the current Google/AOSP records: %s", data)
			}
			slices.Sort(differences)
			t.Error(strings.Join(differences, "; "))
		})
	}
}

func (r googleReferenceRecord) matchesIdentity(identity carrierMatch) bool {
	qualifiers := map[string]*string{
		"gid1": identity.GID1, "gid2": identity.GID2, "spn": identity.SPN,
		"imsiPrefixXpattern": identity.IMSIPrefixXPattern, "preferredApn": identity.PreferredAPN,
	}
	for _, rule := range r.MatchRules {
		if !slices.Contains(rule["mccmnc"], identity.MCCMNC) {
			continue
		}
		matched := true
		for key, value := range qualifiers {
			alternatives := rule[key]
			if value == nil {
				matched = matched && len(alternatives) == 0
				continue
			}
			matched = matched && slices.ContainsFunc(alternatives, func(candidate string) bool {
				if key == "gid1" || key == "gid2" {
					return strings.EqualFold(candidate, *value)
				}
				return candidate == *value
			})
		}
		if matched {
			return true
		}
	}
	return false
}

func currentGoogleReference(t *testing.T, snapshot, carrierList string) map[string][]googleReferenceRecord {
	t.Helper()
	identities := googleReferenceIdentities(t, readGoogleReferenceProto(t, carrierList))
	carriers := googleReferenceCarriers(t, readGoogleReferenceProto(t, snapshot))
	source := make(map[string][]googleReferenceRecord)
	for _, id := range slices.Sorted(maps.Keys(carriers)) {
		carrier := carriers[id]
		name := carrier.text(2, "")
		source[name] = append(source[name], googleReferenceRecord{
			MatchRules: identities[id], Entitlement: googleReferenceEntitlement(t, carrier),
		})
	}
	return source
}

func readGoogleReferenceProto(t *testing.T, path string) googleReferenceProto {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return decodeGoogleReferenceProto(t, data)
}

func googleReferenceIdentities(t *testing.T, data googleReferenceProto) map[uint64][]map[string][]string {
	t.Helper()
	matchFields := map[uint64]string{1: "mccmnc", 2: "imsiPrefixXpattern", 3: "spn", 5: "gid1", 6: "gid2", 7: "preferredApn"}
	identities := make(map[uint64][]map[string][]string)
	for _, raw := range data[1] {
		carrier := decodeGoogleReferenceProto(t, raw)
		id := carrier.number(1, 0)
		for _, rawRule := range carrier[3] {
			fields := decodeGoogleReferenceProto(t, rawRule)
			supported := true
			for number := range fields {
				if _, ok := matchFields[number]; !ok {
					supported = false
				}
			}
			if !supported {
				continue
			}
			rule := make(map[string][]string)
			for number, name := range matchFields {
				for _, value := range fields[number] {
					rule[name] = append(rule[name], string(value))
				}
			}
			identities[id] = append(identities[id], rule)
		}
	}
	return identities
}

func googleReferenceCarriers(t *testing.T, data googleReferenceProto) map[uint64]googleReferenceProto {
	t.Helper()
	flags := make(map[string]googleReferenceProto)
	for _, raw := range data[5] {
		flag := decodeGoogleReferenceProto(t, raw)
		flags[flag.text(1, "")] = flag
	}
	if len(flags["45373869"][6]) == 0 {
		t.Fatal("GMS snapshot has no Google carrier map")
	}
	carriers := make(map[uint64]googleReferenceProto)
	for _, flagID := range []string{"45373869", "45714915"} {
		for _, entry := range decodeGoogleReferenceProto(t, flags[flagID].last(6))[1] {
			item := decodeGoogleReferenceProto(t, entry)
			id := item.number(1, 0)
			carrier := decodeGoogleReferenceProto(t, item.last(2))
			if carrier.number(1, 0) != id {
				t.Fatal("Google carrier ID differs from its map key")
			}
			carriers[id] = carrier
		}
	}
	return carriers
}

func googleReferenceEntitlement(t *testing.T, carrier googleReferenceProto) map[string]any {
	t.Helper()
	transferType := func(number uint64) uint64 {
		value := carrier.number(number, 0)
		switch value {
		case 4, 5:
			return 0
		case 0, 1, 2, 3, 6, 7, 8, 9, 10, 11:
			return value
		default:
			t.Fatalf("unsupported GMS transfer enum %d; review the matching APK", value)
			return 0
		}
	}
	psim, esim := transferType(19), transferType(20)
	number := func(value uint64) json.Number { return json.Number(strconv.FormatUint(value, 10)) }
	// Wire numbers and defaults are checked against cenh/cewh in GMS 26.44.63,
	// independently of the Python updater and Go catalog conversion.
	return map[string]any{
		"entitlementServer":  carrier.text(10, ""),
		"appName":            carrier.text(22, "androidTransfer"),
		"entitlementVersion": carrier.text(23, ""),
		"acquireConfigurationPollingIntervalMinutes": number(carrier.number(13, 0)),
		"acquireConfigurationPollingTimeoutMinutes":  number(carrier.number(14, 0)),
		"autoRetry511Limit":                          number(max(carrier.number(15, 1), 1)),
		"transferTypePsim":                           number(psim),
		"transferTypeEsim":                           number(esim),
		"supportPsimToEsimTransfer":                  carrier.number(17, 0) != 0 && psim != 0,
		"supportEsimToEsimTransfer":                  esim != 0,
		"useHttpPost":                                carrier.number(16, 0) != 0,
		"supportRetryAfter":                          carrier.number(24, 0) != 0,
		"useServerRetryAfterDelay":                   carrier.number(25, 1) != 0,
		"useSmdsDelayedDownload":                     carrier.number(30, 0) != 0,
		"useTempTokenConversion":                     carrier.number(31, 0) != 0,
		"setTargetTerminalFields":                    carrier.number(34, 0) != 0,
		"enableDelayedEsimPushNotification":          carrier.number(36, 0) != 0,
		"supportsMultipleTransfer":                   carrier.number(39, 0) != 0,
		"setCheckEligibilityTargetTerminalId":        carrier.number(40, 0) != 0,
	}
}

// googleReferenceProto is a test-only wire decoder for optional owner-supplied
// snapshots. It does not add protobuf or the private corpus to the library.
type googleReferenceProto map[uint64][][]byte

func decodeGoogleReferenceProto(t *testing.T, data []byte) googleReferenceProto {
	t.Helper()
	fields := make(googleReferenceProto)
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 {
			t.Fatal("invalid reference protobuf tag")
		}
		data = data[n:]
		size := 0
		switch tag & 7 {
		case 0:
			_, size = binary.Uvarint(data)
			if size <= 0 {
				t.Fatal("invalid reference protobuf varint")
			}
		case 1:
			size = 8
		case 2:
			length, n := binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				t.Fatal("invalid reference protobuf length")
			}
			data, size = data[n:], int(length)
		case 5:
			size = 4
		default:
			t.Fatal("unsupported reference protobuf wire type")
		}
		if size > len(data) {
			t.Fatal("truncated reference protobuf field")
		}
		fields[tag>>3] = append(fields[tag>>3], data[:size])
		data = data[size:]
	}
	return fields
}

func (m googleReferenceProto) last(number uint64) []byte {
	values := m[number]
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1]
}

func (m googleReferenceProto) number(number, fallback uint64) uint64 {
	if _, ok := m[number]; !ok {
		return fallback
	}
	value, _ := binary.Uvarint(m.last(number))
	return value
}

func (m googleReferenceProto) text(number uint64, fallback string) string {
	if _, ok := m[number]; !ok {
		return fallback
	}
	return string(m.last(number))
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

func TestDiscoverTransferConfiguration(t *testing.T) {
	var configs EntitlementConfigs
	if err := json.Unmarshal([]byte(`[
		{"mccmnc":"00101","carrierName":"generic","entitlementServer":"https://generic.example","supportPsimToEsimTransfer":true,"supportEsimToEsimTransfer":true,"transferTypePsim":9,"transferTypeEsim":2},
		{"mccmnc":"00101","gid1":"AA","carrierName":"specific","entitlementServer":"https://specific.example","supportPsimToEsimTransfer":true,"transferTypePsim":7}
	]`), &configs); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name          string
		identity      Identity
		sourceSIMType SIMType
		wantServer    string
		wantType      TransferType
		wantErr       error
	}{
		{
			name: "eSIM transfer", identity: Identity{MCC: "001", MNC: "01"},
			wantServer: "https://generic.example", wantType: TransferTypeTemporaryToken,
		},
		{
			name: "pSIM conversion", identity: Identity{MCC: "001", MNC: "01"}, sourceSIMType: SIMTypePSIM,
			wantServer: "https://generic.example", wantType: TransferTypeTemporaryTokenOldTerminalIDs,
		},
		{
			name: "GID prefix selects specific carrier", identity: Identity{MCC: "001", MNC: "01", GID1: "aa1234"}, sourceSIMType: SIMTypePSIM,
			wantServer: "https://specific.example", wantType: TransferTypeOldTerminalIDs,
		},
		{
			name: "unsupported specific carrier does not use generic record", identity: Identity{MCC: "001", MNC: "01", GID1: "aa1234"},
			wantErr: ErrEntitlementUnsupported,
		},
		{
			name: "unknown carrier", identity: Identity{MCC: "001", MNC: "02"},
			wantErr: ErrEntitlementNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configs.Discover(tt.identity, tt.sourceSIMType)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Discover() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Server != tt.wantServer || got.TransferType != tt.wantType || got.SourceSIMType != tt.sourceSIMType {
				t.Fatalf("Discover() = %+v, want server %q, type %v, source SIM %v", got, tt.wantServer, tt.wantType, tt.sourceSIMType)
			}
		})
	}
}

func TestDiscoverEntitlement(t *testing.T) {
	tests := []struct {
		name          string
		identity      Identity
		sourceSIMType SIMType
		want          Entitlement
		wantErr       error
	}{
		{
			name:     "GID1 match wins over MCCMNC fallback",
			identity: Identity{MCC: "204", MNC: "04", GID1: "5A"},
			want: Entitlement{
				Server:       "https://aes.mnc780.mcc302.pub.3gppnetwork.org",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "GID1 match is case insensitive",
			identity: Identity{MCC: "204", MNC: "04", GID1: "5a"},
			want: Entitlement{
				Server:       "https://aes.mnc780.mcc302.pub.3gppnetwork.org",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "GID1 and GID2 match wins over MCCMNC fallback",
			identity: Identity{MCC: "311", MNC: "480", GID1: "ba01570000000000", GID2: "b100000000000000"},
			want: Entitlement{
				Server:       "https://spc-ecs.myvzw.com:443/",
				TransferType: TransferTypeTS43V8,
				PollInterval: 2 * time.Minute,
				PollTimeout:  10 * time.Minute,
				Version:      "8.0",
			},
		},
		{
			name:     "unique MCCMNC fallback",
			identity: Identity{MCC: "310", MNC: "260"},
			want: Entitlement{
				Server:       "https://ts43.eas3.msg.t-mobile.com/google/",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "Austrian two-digit MNC carrier key",
			identity: Identity{MCC: "232", MNC: "05"},
			want: Entitlement{
				Server:       "https://esim-es-open-prod-3at.esimcloud.net/3at/prod/executeGSMAActions",
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  10 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "11.0",
			},
		},
		{
			name:          "pSIM transfer type selected by SPN",
			identity:      Identity{MCC: "230", MNC: "03", SPN: "Vodafone CZ"},
			sourceSIMType: SIMTypePSIM,
			want: Entitlement{
				Server:       "https://es-cz.multiopco.entitlement.vodafone.com:5452/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeTemporaryToken,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "eSIM transfer type selected by SPN",
			identity: Identity{MCC: "230", MNC: "03", SPN: "Vodafone CZ"},
			want: Entitlement{
				Server:       "https://es-cz.multiopco.entitlement.vodafone.com:5452/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeReusableActivationCode,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "IMSI prefix wildcard selects carrier",
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262032931234567"},
			want: Entitlement{
				Server:                 "https://aes.mnc023.mcc262.pub.3gppnetwork.org/",
				TransferType:           TransferTypeTemporaryToken,
				PollInterval:           2 * time.Minute,
				PollTimeout:            10 * time.Minute,
				UseTempTokenConversion: true,
				AppName:                "androidTransfer",
				Version:                "9.0",
			},
		},
		{
			name:     "IMSI and SPN conditions match together",
			identity: Identity{MCC: "262", MNC: "02", IMSI: "262021910123456", SPN: "Vodafone.de"},
			want: Entitlement{
				Server:       "https://es-de.vodafone.com:5445/DEGDROIDInterfaceTS43",
				UsePost:      true,
				TransferType: TransferTypeReusableActivationCode,
				PollInterval: time.Minute,
				PollTimeout:  5 * time.Minute,
				AppName:      "androidTransfer",
				Version:      "9.0",
			},
		},
		{
			name:     "SPN without required IMSI does not match",
			identity: Identity{MCC: "262", MNC: "02", SPN: "Vodafone.de"},
			wantErr:  ErrEntitlementNotFound,
		},
		{
			name:     "missing MCCMNC",
			identity: Identity{MCC: "999", MNC: "99"},
			wantErr:  ErrEntitlementNotFound,
		},
		{
			name:          "unsupported pSIM transfer",
			identity:      Identity{MCC: "310", MNC: "012"},
			sourceSIMType: SIMTypePSIM,
			wantErr:       ErrEntitlementUnsupported,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateEntitlementConfigs(t).Discover(tt.identity, tt.sourceSIMType)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("privateEntitlementConfigs(t).Discover() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("privateEntitlementConfigs(t).Discover() error = %v", err)
			}
			if got.Server != tt.want.Server {
				t.Errorf("Server = %q, want %q", got.Server, tt.want.Server)
			}
			if got.TransferType != tt.want.TransferType {
				t.Errorf("TransferType = %v, want %v", got.TransferType, tt.want.TransferType)
			}
			if got.UsePost != tt.want.UsePost {
				t.Errorf("UsePost = %t, want %t", got.UsePost, tt.want.UsePost)
			}
			if got.UseTempTokenConversion != tt.want.UseTempTokenConversion {
				t.Errorf("UseTempTokenConversion = %t, want %t", got.UseTempTokenConversion, tt.want.UseTempTokenConversion)
			}
			if got.PollInterval != tt.want.PollInterval {
				t.Errorf("PollInterval = %s, want %s", got.PollInterval, tt.want.PollInterval)
			}
			if got.PollTimeout != tt.want.PollTimeout {
				t.Errorf("PollTimeout = %s, want %s", got.PollTimeout, tt.want.PollTimeout)
			}
			if got.AppName != tt.want.AppName {
				t.Errorf("AppName = %q, want %q", got.AppName, tt.want.AppName)
			}
			if got.Version != tt.want.Version {
				t.Errorf("Version = %q, want %q", got.Version, tt.want.Version)
			}
		})
	}
}

func TestSelectEntitlementRecordIdentityMatching(t *testing.T) {
	gid1 := "AA"
	gid2 := "BB"
	spn := "Example"
	records := []entitlementRecord{
		{MCCMNC: "00101", CarrierName: "fallback", EntitlementServer: "fallback"},
		{MCCMNC: "00101", GID1: &gid1, CarrierName: "gid1", EntitlementServer: "gid1"},
		{MCCMNC: "00101", GID1: &gid1, GID2: &gid2, CarrierName: "gid12", EntitlementServer: "gid12"},
		{MCCMNC: "00101", SPN: &spn, CarrierName: "spn", EntitlementServer: "spn"},
	}
	tests := []struct {
		name     string
		identity Identity
		want     string
		wantErr  error
	}{
		{
			name:     "most specific GID1 and GID2 match wins",
			identity: Identity{MCC: "001", MNC: "01", GID1: "aa", GID2: "bb"},
			want:     "gid12",
		},
		{
			name:     "SPN match wins over MCCMNC fallback",
			identity: Identity{MCC: "001", MNC: "01", SPN: "Example"},
			want:     "spn",
		},
		{
			name:     "mismatched specific fields use fallback",
			identity: Identity{MCC: "001", MNC: "01", GID1: "CC"},
			want:     "fallback",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectEntitlementRecord(records, tt.identity)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("selectEntitlementRecord() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectEntitlementRecord() error = %v", err)
			}
			if got.CarrierName != tt.want {
				t.Errorf("CarrierName = %q, want %q", got.CarrierName, tt.want)
			}
		})
	}
}

func TestSelectEntitlementRecordEqualSpecificityIsAmbiguous(t *testing.T) {
	first := "AA"
	second := "AA"
	records := []entitlementRecord{
		{MCCMNC: "00101", GID1: &first, CarrierName: "first", EntitlementServer: "first"},
		{MCCMNC: "00101", GID1: &second, CarrierName: "second", EntitlementServer: "second"},
	}
	_, err := selectEntitlementRecord(records, Identity{MCC: "001", MNC: "01", GID1: "aa"})
	if !errors.Is(err, ErrEntitlementAmbiguous) {
		t.Errorf("selectEntitlementRecord() error = %v, want %v", err, ErrEntitlementAmbiguous)
	}
}

func TestCarrierMatchScore(t *testing.T) {
	pattern := "26203293x"
	empty := ""
	tests := []struct {
		name     string
		match    carrierMatch
		identity Identity
		want     int
		ok       bool
	}{
		{
			name:     "missing IMSI condition is a wildcard",
			match:    carrierMatch{MCCMNC: "00101"},
			identity: Identity{MCC: "001", MNC: "01", IMSI: "001010123456789"},
			want:     0,
			ok:       true,
		},
		{
			name:     "IMSI prefix with wildcard matches",
			match:    carrierMatch{MCCMNC: "26203", IMSIPrefixXPattern: &pattern},
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262032931234567"},
			want:     1,
			ok:       true,
		},
		{
			name:     "IMSI prefix mismatch rejects",
			match:    carrierMatch{MCCMNC: "26203", IMSIPrefixXPattern: &pattern},
			identity: Identity{MCC: "262", MNC: "03", IMSI: "262033301234567"},
			ok:       false,
		},
		{
			name:     "explicit empty IMSI pattern matches empty IMSI",
			match:    carrierMatch{MCCMNC: "00101", IMSIPrefixXPattern: &empty},
			identity: Identity{MCC: "001", MNC: "01"},
			want:     1,
			ok:       true,
		},
		{
			name:     "explicit empty IMSI pattern rejects non-empty IMSI",
			match:    carrierMatch{MCCMNC: "00101", IMSIPrefixXPattern: &empty},
			identity: Identity{MCC: "001", MNC: "01", IMSI: "001010123456789"},
			ok:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.match.score(tt.identity)
			if ok != tt.ok {
				t.Errorf("score() matched = %t, want %t", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("score() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEmbeddedCarrierConfigsIncludeIdentityFields(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "WFC", data: privateCarrierJSON(t, "wfc.json")},
		{name: "entitlement", data: privateCarrierJSON(t, "entitlement.json")},
	}
	wantFields := []string{"gid1", "gid2", "spn", "imsiPrefixXpattern"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(tt.data, &records); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			for i, record := range records {
				for _, field := range wantFields {
					if _, ok := record[field]; !ok {
						t.Errorf("record %d is missing %q", i, field)
					}
				}
			}
		})
	}
}

func privateCarrierJSON(t *testing.T, name string) []byte {
	t.Helper()
	dir := os.Getenv("TS43_CARRIER_CONFIG_DIR")
	if dir == "" {
		t.Skip("private carrier catalog regression requires TS43_CARRIER_CONFIG_DIR")
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

func TestRegressionGoogleCarrierMatching(t *testing.T) {
	tests := []struct {
		name     string
		identity Identity
		server   string
	}{
		{"GID prefix", Identity{MCC: "204", MNC: "04", GID1: "5AFF"}, "https://aes.mnc780.mcc302.pub.3gppnetwork.org"},
		{"GID outranks SPN", Identity{MCC: "204", MNC: "04", GID1: "5A", SPN: "U.S. Cellular"}, "https://aes.mnc780.mcc302.pub.3gppnetwork.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privateEntitlementConfigs(t).Discover(tt.identity, SIMTypeESIM)
			if err != nil {
				t.Fatalf("discovery error = %v", err)
			}
			if got.Server != tt.server {
				t.Errorf("server=%q want %q", got.Server, tt.server)
			}
		})
	}
}
