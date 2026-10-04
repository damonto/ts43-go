package ts43

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *WFCStatus) UnmarshalBinary(data []byte) error {
	var payload wfcPayload
	if err := payload.UnmarshalBinary(data); err != nil {
		return err
	}
	status, err := payload.status()
	if err != nil {
		return err
	}
	*s = status
	return nil
}

func escapeBareXMLAmpersands(data []byte) ([]byte, bool) {
	var out []byte
	changed := false
	for i := range data {
		if data[i] != '&' {
			out = append(out, data[i])
			continue
		}
		if validXMLReference(data[i:]) {
			out = append(out, data[i])
			continue
		}
		out = append(out, '&', 'a', 'm', 'p', ';')
		changed = true
	}
	return out, changed
}

func validXMLReference(data []byte) bool {
	for _, ref := range [...]string{"&amp;", "&lt;", "&gt;", "&apos;", "&quot;"} {
		if bytes.HasPrefix(data, []byte(ref)) {
			return true
		}
	}
	if len(data) < 4 || data[1] != '#' {
		return false
	}
	i := 2
	if data[i] == 'x' || data[i] == 'X' {
		i++
		start := i
		for i < len(data) && isXMLHexDigit(data[i]) {
			i++
		}
		return i > start && i < len(data) && data[i] == ';'
	}
	start := i
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		i++
	}
	return i > start && i < len(data) && data[i] == ';'
}

func isXMLHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

type wfcPayload struct {
	Token         *tokenFields
	Application   *wfcFields
	Configuration WFCConfiguration
	Root          wfcFields
}

func (p *wfcPayload) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var next wfcPayload
	var generic *wfcFields
	for name, raw := range fields {
		switch strings.ToUpper(name) {
		case "VERS":
			if err := json.Unmarshal(raw, &next.Configuration); err != nil {
				return err
			}
		case "TOKEN":
			if err := json.Unmarshal(raw, &next.Token); err != nil {
				return err
			}
		default:
			appID, ok := wfcJSONAppID(name)
			if !ok {
				var value stringField
				if err := json.Unmarshal(raw, &value); err != nil {
					return err
				}
				next.Root.set(name, value.String())
				continue
			}
			app, err := decodeWFCJSONFields(raw, appID)
			if err != nil {
				return err
			}
			switch app.AppID.String() {
			case AppVoWiFi:
				next.Application = &app
			case "":
				generic = &app
			}
		}
	}
	if next.Application == nil {
		next.Application = generic
	}
	*p = next
	return nil
}

func wfcJSONAppID(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, "APPLICATION"):
		return "", true
	case strings.EqualFold(name, AppVoWiFi):
		return AppVoWiFi, true
	case strings.EqualFold(name, appVoLTE):
		return appVoLTE, true
	case strings.EqualFold(name, appSMSOIP):
		return appSMSOIP, true
	default:
		return "", false
	}
}

func decodeWFCJSONFields(data []byte, appID string) (wfcFields, error) {
	var fields wfcFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return wfcFields{}, err
	}
	if fields.AppID == "" && appID != "" {
		fields.AppID = stringField(appID)
	}
	return fields, nil
}

func (p *wfcPayload) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	var generic *wfcFields
	applications := 0
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "characteristic" && strings.EqualFold(characteristicType(t), "TOKEN") {
				var token tokenFields
				if err := decoder.DecodeElement(&token, &t); err != nil {
					return err
				}
				p.Token = &token
				continue
			}
			if t.Name.Local == "characteristic" && strings.EqualFold(characteristicType(t), "VERS") {
				if err := decoder.DecodeElement(&p.Configuration, &t); err != nil {
					return err
				}
				continue
			}
			if t.Name.Local != "characteristic" || !strings.EqualFold(characteristicType(t), "APPLICATION") {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var app wfcFields
			if err := decoder.DecodeElement(&app, &t); err != nil {
				return err
			}
			applications++
			if app.AppID == "" {
				generic = &app
			}
			if app.AppID.String() == AppVoWiFi {
				p.Application = &app
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if p.Application == nil && applications == 1 {
					p.Application = generic
				}
				return nil
			}
		}
	}
}

func (p wfcPayload) status() (WFCStatus, error) {
	fields := p.Application
	if fields == nil {
		fields = &p.Root
	}
	if fields.empty() || (fields.AppID != "" && fields.AppID.String() != AppVoWiFi) {
		return WFCStatus{}, errors.New("WFC application ap2004 is missing")
	}
	return fields.status()
}

type wfcFields struct {
	AppID                  stringField `json:"AppID"`
	EntitlementStatus      stringField `json:"EntitlementStatus"`
	TCStatus               stringField `json:"TC_Status"`
	AddrStatus             stringField `json:"AddrStatus"`
	ProvStatus             stringField `json:"ProvStatus"`
	ServiceFlowURL         stringField `json:"ServiceFlow_URL"`
	ServiceFlowUserData    stringField `json:"ServiceFlow_UserData"`
	MessageForIncompatible stringField `json:"MessageForIncompatible"`
	AddrExpiry             stringField `json:"AddrExpiry"`
}

func (f *wfcFields) UnmarshalJSON(data []byte) error {
	var fields map[string]stringField
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		f.set(name, value.String())
	}
	return nil
}

func (f *wfcFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "parm" && t.Name.Local != "param" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var param xmlParam
			if err := decoder.DecodeElement(&param, &t); err != nil {
				return err
			}
			f.set(param.Name, param.Value)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *wfcFields) set(name, value string) {
	switch normalizeWFCFieldName(name) {
	case "messageforincompatible":
		f.MessageForIncompatible = stringField(value)
	case "addrexpiry":
		f.AddrExpiry = stringField(value)
	case "appid":
		f.AppID = stringField(value)
	case "entitlementstatus":
		f.EntitlementStatus = stringField(value)
	case "tcstatus":
		f.TCStatus = stringField(value)
	case "addrstatus":
		f.AddrStatus = stringField(value)
	case "provstatus":
		f.ProvStatus = stringField(value)
	case "serviceflowurl":
		f.ServiceFlowURL = stringField(value)
	case "serviceflowuserdata":
		f.ServiceFlowUserData = stringField(value)
	}
}

func normalizeWFCFieldName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

func (f wfcFields) empty() bool {
	return f.AppID == "" &&
		f.EntitlementStatus == "" &&
		f.TCStatus == "" &&
		f.AddrStatus == "" &&
		f.ProvStatus == "" &&
		f.ServiceFlowURL == "" &&
		f.ServiceFlowUserData == ""
}

func (f wfcFields) status() (WFCStatus, error) {
	if err := f.requireStatusFields(); err != nil {
		return WFCStatus{}, err
	}
	var entitlementStatus WFCEntitlementStatus
	if err := entitlementStatus.UnmarshalText([]byte(f.EntitlementStatus)); err != nil {
		return WFCStatus{}, err
	}
	var tcStatus WFCTCStatus
	if err := tcStatus.UnmarshalText([]byte(f.TCStatus)); err != nil {
		return WFCStatus{}, err
	}
	var addrStatus WFCAddressStatus
	if err := addrStatus.UnmarshalText([]byte(f.AddrStatus)); err != nil {
		return WFCStatus{}, err
	}
	var provStatus WFCProvisionStatus
	if err := provStatus.UnmarshalText([]byte(f.ProvStatus)); err != nil {
		return WFCStatus{}, err
	}
	return WFCStatus{
		EntitlementStatus:      entitlementStatus,
		TCStatus:               tcStatus,
		AddrStatus:             addrStatus,
		ProvStatus:             provStatus,
		ServiceFlowURL:         f.ServiceFlowURL.String(),
		ServiceFlowUserData:    f.ServiceFlowUserData.String(),
		MessageForIncompatible: f.MessageForIncompatible.String(),
		AddrExpiry:             f.AddrExpiry.String(),
	}, nil
}

func (f wfcFields) requireStatusFields() error {
	var missing []string
	if f.EntitlementStatus.String() == "" {
		missing = append(missing, "EntitlementStatus")
	}
	if f.TCStatus.String() == "" {
		missing = append(missing, "TC_Status")
	}
	if f.AddrStatus.String() == "" {
		missing = append(missing, "AddrStatus")
	}
	if f.ProvStatus.String() == "" {
		missing = append(missing, "ProvStatus")
	}
	if len(missing) > 0 {
		return fmt.Errorf("wfc status missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (p wfcPayload) authToken(current AuthToken, now time.Time) (AuthToken, error) {
	if p.Token == nil || p.Token.Token == "" {
		return current, nil
	}
	validity, err := wfcAuthTokenValidity(p.Token.Validity.String())
	if err != nil {
		return current, err
	}
	current.Value = p.Token.Token.String()
	current.ExpiresAt = now.Add(time.Duration(validity) * time.Second)
	return current, nil
}

func wfcAuthTokenValidity(value string) (int, error) {
	validity, err := parseOptionalInt(value)
	if err != nil {
		return 0, fmt.Errorf("parse WFC authentication token validity: %w", err)
	}
	if validity.value > 0 {
		return validity.value, nil
	}
	return 3600, nil
}

// WFCConfiguration contains the server's VERS values. Validity is in seconds;
// negative sentinel pairs retain TS.43's stop-until-setting-change semantics.
type WFCConfiguration struct {
	Version  int
	Validity int
	Present  bool
}

func (v WFCConfiguration) resetToDefault() bool {
	return v.Version <= 0 || v.Validity < 0
}

func (v *WFCConfiguration) UnmarshalJSON(data []byte) error {
	var fields map[string]stringField
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	return v.apply(fields)
}

func (v *WFCConfiguration) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var doc struct {
		Params []xmlParam `xml:"parm"`
	}
	if err := d.DecodeElement(&doc, &start); err != nil {
		return err
	}
	fields := make(map[string]stringField)
	for _, param := range doc.Params {
		fields[param.Name] = stringField(param.Value)
	}
	return v.apply(fields)
}

func (v *WFCConfiguration) apply(fields map[string]stringField) error {
	next := WFCConfiguration{Present: true}
	for name, value := range fields {
		var dest *int
		switch strings.ToLower(name) {
		case "version":
			dest = &next.Version
		case "validity":
			dest = &next.Validity
		default:
			continue
		}
		n, err := strconv.Atoi(value.String())
		if err != nil {
			return fmt.Errorf("parse VERS %s: %w", name, err)
		}
		*dest = n
	}
	*v = next
	return nil
}

func (p *wfcPayload) UnmarshalBinary(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("WFC response is empty")
	}
	var next wfcPayload
	if json.Valid(data) {
		if err := json.Unmarshal(data, &next); err != nil {
			return err
		}
	} else if err := xml.Unmarshal(data, &next); err != nil {
		escaped, ok := escapeBareXMLAmpersands(data)
		if !ok {
			return err
		}
		next = wfcPayload{}
		if retryErr := xml.Unmarshal(escaped, &next); retryErr != nil {
			return err
		}
	}
	*p = next
	return nil
}
