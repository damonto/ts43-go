package ts43

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type ODSAResponse struct {
	OperationResult       OperationResult
	PrimaryAppEligibility AppEligibility
	SubscriptionResult    SubscriptionResult
	ServiceStatus         ServiceStatus
	PollingInterval       int
	PollingIntervalSet    bool
	PollingIntervalUnit   int
	TemporaryToken        string
	TemporaryTokenExpiry  string
	OperationTargets      []Operation
	RegisterNotifStatus   int
	GeneralErrorText      string
	AuthenticationToken   string
	AuthTokenValidity     int
	Download              DownloadConfig
	// DownloadInfoPresent reports whether the response contained a DownloadInfo
	// characteristic, including one whose fields were empty or incomplete.
	DownloadInfoPresent bool
	Message             Message
	Websheet            Websheet
	Raw                 []byte
	cookies             []string
}

// UnmarshalBinary decodes an XML or JSON response. A successful decode replaces
// the previous response; on error the receiver remains unchanged.
func (r *ODSAResponse) UnmarshalBinary(data []byte) error {
	var next ODSAResponse
	var err error
	if json.Valid(data) {
		err = next.unmarshalJSON(data)
	} else {
		err = next.unmarshalXML(data)
	}
	if err != nil {
		return err
	}
	next.Raw = append(r.Raw[:0], data...)
	*r = next
	return nil
}

func (r *ODSAResponse) unmarshalJSON(data []byte) error {
	var doc odsaPayload
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	return r.apply(doc)
}

func (r *ODSAResponse) unmarshalXML(data []byte) error {
	var doc odsaPayload
	if err := xml.Unmarshal(data, &doc); err != nil {
		// Pixel normalizes bare ampersands in carrier XML before parsing.
		escaped, ok := escapeBareXMLAmpersands(data)
		if !ok {
			return err
		}
		doc = odsaPayload{}
		if retryErr := xml.Unmarshal(escaped, &doc); retryErr != nil {
			return err
		}
	}
	return r.apply(doc)
}

type odsaPayload struct {
	Token       *tokenFields       `json:"TOKEN"`
	Application *applicationFields `json:"APPLICATION"`
	Root        applicationFields
}

func (p *odsaPayload) UnmarshalJSON(data []byte) error {
	var named struct {
		Token       *tokenFields    `json:"TOKEN"`
		Application json.RawMessage `json:"APPLICATION"`
		Primary     json.RawMessage `json:"ap2009"`
	}
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	// TS.43 JSON names application objects by AppID. Decode the chosen
	// application only; unused legacy containers must not override it.
	var application *applicationFields
	if named.Primary != nil {
		if err := json.Unmarshal(named.Primary, &application); err != nil {
			return err
		}
	}
	if application != nil {
		if application.AppID != "" && !strings.EqualFold(application.AppID.String(), AppODSAPrimary) {
			return fmt.Errorf("ODSA application ID %q does not match %s", application.AppID, AppODSAPrimary)
		}
	} else if named.Application != nil {
		if err := json.Unmarshal(named.Application, &application); err != nil {
			return err
		}
		if application != nil && application.AppID != "" && !strings.EqualFold(application.AppID.String(), AppODSAPrimary) {
			application = nil
		}
	}
	var root applicationFields
	if application == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return err
		}
		if root.AppID != "" && !strings.EqualFold(root.AppID.String(), AppODSAPrimary) {
			root = applicationFields{}
		}
	}
	*p = odsaPayload{Token: named.Token, Application: application, Root: root}
	return nil
}

func (p *odsaPayload) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	var generic *applicationFields
	applications := 0
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "characteristic" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			switch characteristicType(t) {
			case "TOKEN":
				var token tokenFields
				if err := decoder.DecodeElement(&token, &t); err != nil {
					return err
				}
				p.Token = &token
			case "APPLICATION":
				var app applicationFields
				if err := decoder.DecodeElement(&app, &t); err != nil {
					return err
				}
				applications++
				switch {
				case strings.EqualFold(app.AppID.String(), AppODSAPrimary):
					p.Application = &app
				case app.AppID == "":
					generic = &app
				}
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
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

type tokenFields struct {
	Token    stringField `json:"token"`
	Validity stringField `json:"Validity"`
}

func (f *tokenFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parm", "param":
				var param xmlParam
				if err := decoder.DecodeElement(&param, &t); err != nil {
					return err
				}
				f.set(param.Name, param.Value)
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *tokenFields) set(name, value string) {
	switch strings.ToLower(name) {
	case "token":
		f.Token = stringField(value)
	case "validity":
		f.Validity = stringField(value)
	}
}

type applicationFields struct {
	AppID                           stringField            `json:"AppID"`
	OperationResult                 stringField            `json:"OperationResult"`
	PrimaryAppEligibility           stringField            `json:"PrimaryAppEligibility"`
	SubscriptionResult              stringField            `json:"SubscriptionResult"`
	PollingInterval                 stringField            `json:"PollingInterval"`
	PollingIntervalUnit             stringField            `json:"PollingIntervalUnit"`
	TemporaryToken                  stringField            `json:"TemporaryToken"`
	TemporaryTokenExpiry            stringField            `json:"TemporaryTokenExpiry"`
	OperationTargets                stringField            `json:"OperationTargets"`
	RegisterNotifStatus             stringField            `json:"RegisterNotifStatus"`
	GeneralErrorText                stringField            `json:"GeneralErrorText"`
	SubscriptionServiceURL          stringField            `json:"SubscriptionServiceURL"`
	SubscriptionServiceUserData     stringField            `json:"SubscriptionServiceUserData"`
	SubscriptionServiceContentsType stringField            `json:"SubscriptionServiceContentsType"`
	PrimaryConfiguration            *primaryFields         `json:"PrimaryConfiguration"`
	PrimaryConfigurations           *primaryConfigurations `json:"PrimaryConfigurations"`
	Download                        *downloadFields        `json:"DownloadInfo"`
	Message                         *messageFields         `json:"MSG"`
}

func (f *applicationFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parm", "param":
				var param xmlParam
				if err := decoder.DecodeElement(&param, &t); err != nil {
					return err
				}
				f.set(param.Name, param.Value)
			case "characteristic":
				switch characteristicType(t) {
				case "PrimaryConfiguration":
					var primary primaryFields
					if err := decoder.DecodeElement(&primary, &t); err != nil {
						return err
					}
					f.PrimaryConfiguration = &primary
				case "PrimaryConfigurations":
					var primary primaryConfigurations
					if err := decoder.DecodeElement(&primary, &t); err != nil {
						return err
					}
					f.PrimaryConfigurations = &primary
				case "DownloadInfo":
					var download downloadFields
					if err := decoder.DecodeElement(&download, &t); err != nil {
						return err
					}
					f.Download = &download
				case "MSG":
					var message messageFields
					if err := decoder.DecodeElement(&message, &t); err != nil {
						return err
					}
					f.Message = &message
				default:
					if err := decoder.Skip(); err != nil {
						return err
					}
				}
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *applicationFields) set(name, value string) {
	switch name {
	case "AppID":
		f.AppID = stringField(value)
	case "OperationResult":
		f.OperationResult = stringField(value)
	case "PrimaryAppEligibility":
		f.PrimaryAppEligibility = stringField(value)
	case "SubscriptionResult":
		f.SubscriptionResult = stringField(value)
	case "PollingInterval":
		f.PollingInterval = stringField(value)
	case "PollingIntervalUnit":
		f.PollingIntervalUnit = stringField(value)
	case "TemporaryToken":
		f.TemporaryToken = stringField(value)
	case "TemporaryTokenExpiry":
		f.TemporaryTokenExpiry = stringField(value)
	case "OperationTargets":
		f.OperationTargets = stringField(value)
	case "RegisterNotifStatus":
		f.RegisterNotifStatus = stringField(value)
	case "GeneralErrorText":
		f.GeneralErrorText = stringField(value)
	case "SubscriptionServiceURL":
		f.SubscriptionServiceURL = stringField(value)
	case "SubscriptionServiceUserData":
		f.SubscriptionServiceUserData = stringField(value)
	case "SubscriptionServiceContentsType":
		f.SubscriptionServiceContentsType = stringField(value)
	}
}

// primaryConfigurations preserves the first entry, as the Android clients do.
// A standalone PrimaryConfiguration takes precedence over this fallback.
type primaryConfigurations struct {
	first *primaryFields
}

func (f *primaryConfigurations) UnmarshalJSON(data []byte) error {
	type entry struct {
		PrimaryConfiguration *primaryFields `json:"PrimaryConfiguration"`
	}
	var entries []entry
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &entries); err != nil {
			return err
		}
	} else {
		var single entry
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		entries = []entry{single}
	}
	f.first = nil
	for _, entry := range entries {
		if entry.PrimaryConfiguration != nil {
			f.first = entry.PrimaryConfiguration
			break
		}
	}
	return nil
}

func (f *primaryConfigurations) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	f.first = nil
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "characteristic" || characteristicType(t) != "PrimaryConfiguration" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var primary primaryFields
			if err := decoder.DecodeElement(&primary, &t); err != nil {
				return err
			}
			if f.first == nil {
				f.first = &primary
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

type primaryFields struct {
	ICCID               stringField     `json:"ICCID"`
	ServiceStatus       stringField     `json:"ServiceStatus"`
	PollingInterval     stringField     `json:"PollingInterval"`
	PollingIntervalUnit stringField     `json:"PollingIntervalUnit"`
	Download            *downloadFields `json:"DownloadInfo"`
	Message             *messageFields  `json:"MSG"`
}

func (f *primaryFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parm", "param":
				var param xmlParam
				if err := decoder.DecodeElement(&param, &t); err != nil {
					return err
				}
				f.set(param.Name, param.Value)
			case "characteristic":
				switch characteristicType(t) {
				case "DownloadInfo":
					var download downloadFields
					if err := decoder.DecodeElement(&download, &t); err != nil {
						return err
					}
					f.Download = &download
				case "MSG":
					var message messageFields
					if err := decoder.DecodeElement(&message, &t); err != nil {
						return err
					}
					f.Message = &message
				default:
					if err := decoder.Skip(); err != nil {
						return err
					}
				}
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *primaryFields) set(name, value string) {
	switch name {
	case "ICCID":
		f.ICCID = stringField(value)
	case "ServiceStatus":
		f.ServiceStatus = stringField(value)
	case "PollingInterval":
		f.PollingInterval = stringField(value)
	case "PollingIntervalUnit":
		f.PollingIntervalUnit = stringField(value)
	}
}

type downloadFields struct {
	ProfileActivationCode stringField `json:"ProfileActivationCode"`
	ProfileSMDPAddress    stringField `json:"ProfileSmdpAddress"`
	ProfileICCID          stringField `json:"ProfileIccid"`
	SubscriptionService   stringField `json:"SubscriptionServiceURL"`
	MatchingID            stringField `json:"MatchingID"`
}

func (f *downloadFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parm", "param":
				var param xmlParam
				if err := decoder.DecodeElement(&param, &t); err != nil {
					return err
				}
				f.set(param.Name, param.Value)
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *downloadFields) set(name, value string) {
	switch name {
	case "ProfileActivationCode":
		f.ProfileActivationCode = stringField(value)
	case "ProfileSmdpAddress":
		f.ProfileSMDPAddress = stringField(value)
	case "ProfileIccid":
		f.ProfileICCID = stringField(value)
	case "SubscriptionServiceURL":
		f.SubscriptionService = stringField(value)
	case "MatchingID":
		f.MatchingID = stringField(value)
	}
}

type messageFields struct {
	Title                              stringField `json:"Title"`
	Text                               stringField `json:"Message"`
	AcceptButton                       stringField `json:"Accept_btn"`
	AcceptButtonLabel                  stringField `json:"Accept_btn_label"`
	RejectButton                       stringField `json:"Reject_btn"`
	RejectButtonLabel                  stringField `json:"Reject_btn_label"`
	AcceptFreeText                     stringField `json:"Accept_freetext"`
	AcceptFreeTextHint                 stringField `json:"Accept_freetext_hint"`
	AcceptFreeTextValidation           stringField `json:"Accept_freetext_validation"`
	AcceptFreeTextValidationFailedText stringField `json:"Accept_freetext_validation_failed_error_text"`
}

func (f *messageFields) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "parm", "param":
				var param xmlParam
				if err := decoder.DecodeElement(&param, &t); err != nil {
					return err
				}
				f.set(param.Name, param.Value)
			default:
				if err := decoder.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return nil
			}
		}
	}
}

func (f *messageFields) set(name, value string) {
	switch name {
	case "Title":
		f.Title = stringField(value)
	case "Message":
		f.Text = stringField(value)
	case "Accept_btn":
		f.AcceptButton = stringField(value)
	case "Accept_btn_label":
		f.AcceptButtonLabel = stringField(value)
	case "Reject_btn":
		f.RejectButton = stringField(value)
	case "Reject_btn_label":
		f.RejectButtonLabel = stringField(value)
	case "Accept_freetext":
		f.AcceptFreeText = stringField(value)
	case "Accept_freetext_hint":
		f.AcceptFreeTextHint = stringField(value)
	case "Accept_freetext_validation":
		f.AcceptFreeTextValidation = stringField(value)
	case "Accept_freetext_validation_failed_error_text":
		f.AcceptFreeTextValidationFailedText = stringField(value)
	}
}

type stringField string

func (f *stringField) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if string(data) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = stringField(s)
		return nil
	}
	*f = stringField(data)
	return nil
}

func (f stringField) String() string {
	return string(f)
}

type optionalInt struct {
	value int
	set   bool
}

func (n optionalInt) MarshalText() ([]byte, error) { return n.AppendText(nil) }

func (n optionalInt) AppendText(dst []byte) ([]byte, error) {
	if !n.set {
		return dst, nil
	}
	return strconv.AppendInt(dst, int64(n.value), 10), nil
}

func (n *optionalInt) UnmarshalText(text []byte) error {
	value := strings.TrimSpace(string(text))
	if value == "" {
		*n = optionalInt{}
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	*n = optionalInt{value: parsed, set: true}
	return nil
}

func parseOptionalInt(value string) (optionalInt, error) {
	var parsed optionalInt
	if err := parsed.UnmarshalText([]byte(value)); err != nil {
		return optionalInt{}, err
	}
	return parsed, nil
}

type xmlParam struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func characteristicType(start xml.StartElement) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == "type" {
			return attr.Value
		}
	}
	return ""
}

func (r *ODSAResponse) apply(doc odsaPayload) error {
	app := doc.Root
	if doc.Application != nil {
		app = *doc.Application
	}
	var primary primaryFields
	if app.PrimaryConfiguration != nil {
		primary = *app.PrimaryConfiguration
	} else if app.PrimaryConfigurations != nil && app.PrimaryConfigurations.first != nil {
		primary = *app.PrimaryConfigurations.first
	}
	var download downloadFields
	switch {
	case app.Download != nil:
		r.DownloadInfoPresent = true
		download = *app.Download
	case primary.Download != nil:
		r.DownloadInfoPresent = true
		download = *primary.Download
	default:
		r.DownloadInfoPresent = false
	}
	r.Download = DownloadConfig{}
	var message messageFields
	switch {
	case app.Message != nil:
		message = *app.Message
	case primary.Message != nil:
		message = *primary.Message
	}
	var token tokenFields
	if doc.Token != nil {
		token = *doc.Token
	}

	if err := r.OperationResult.UnmarshalText([]byte(app.OperationResult)); err != nil {
		return err
	}
	if err := r.PrimaryAppEligibility.UnmarshalText([]byte(app.PrimaryAppEligibility)); err != nil {
		return err
	}
	if err := r.SubscriptionResult.UnmarshalText([]byte(app.SubscriptionResult)); err != nil {
		return err
	}
	if err := r.ServiceStatus.UnmarshalText([]byte(primary.ServiceStatus)); err != nil {
		return err
	}
	pollingInterval := primary.PollingInterval.String()
	pollingIntervalUnit := primary.PollingIntervalUnit.String()
	if strings.TrimSpace(pollingInterval) == "" {
		pollingInterval = app.PollingInterval.String()
		pollingIntervalUnit = app.PollingIntervalUnit.String()
	}
	// Android clients fall back when optional polling hints are malformed.
	pollingIntervalValue, _ := parseOptionalInt(pollingInterval)
	pollingIntervalUnitValue, _ := parseOptionalInt(pollingIntervalUnit)
	registerNotifStatus, err := parseOptionalInt(app.RegisterNotifStatus.String())
	if err != nil {
		return fmt.Errorf("parse register notification status: %w", err)
	}
	authTokenValidity, err := parseOptionalInt(token.Validity.String())
	if err != nil || !authTokenValidity.set || authTokenValidity.value < 0 {
		// GMS uses one hour when the authentication validity is unavailable.
		authTokenValidity = optionalInt{value: 3600, set: true}
	}
	r.PollingIntervalSet = pollingIntervalValue.set
	r.PollingInterval = pollingIntervalValue.value
	r.PollingIntervalUnit = pollingIntervalUnitValue.value
	r.TemporaryToken = app.TemporaryToken.String()
	r.TemporaryTokenExpiry = app.TemporaryTokenExpiry.String()
	var targets operationList
	if err := targets.UnmarshalText([]byte(app.OperationTargets)); err != nil {
		return fmt.Errorf("parse operation targets: %w", err)
	}
	r.OperationTargets = targets
	r.RegisterNotifStatus = registerNotifStatus.value
	r.GeneralErrorText = app.GeneralErrorText.String()
	r.AuthenticationToken = token.Token.String()
	r.AuthTokenValidity = authTokenValidity.value
	r.Message = message.message()
	if r.SubscriptionResult == SubscriptionResultContinueToWebsheet {
		r.Websheet = Websheet{
			URL:          app.SubscriptionServiceURL.String(),
			UserData:     encodedPrefixedField(app.SubscriptionServiceUserData.String()),
			ContentsType: app.SubscriptionServiceContentsType.String(),
		}
	}
	r.Download.ActivationCode = activationCodeField(download.ProfileActivationCode.String())
	smdp := download.ProfileSMDPAddress.String()
	if smdp == "" {
		smdp = download.SubscriptionService.String()
	}
	r.Download.SMDPFQDN = smdpFQDN(smdp)
	r.Download.ProfileICCID = firstNonEmpty(download.ProfileICCID.String(), primary.ICCID.String())
	r.Download.MatchingID = download.MatchingID.String()
	return nil
}

func (m messageFields) message() Message {
	return Message{
		Title:                                   m.Title.String(),
		Text:                                    m.Text.String(),
		AcceptButton:                            one(m.AcceptButton.String()),
		AcceptButtonLabel:                       m.AcceptButtonLabel.String(),
		RejectButton:                            one(m.RejectButton.String()),
		RejectButtonLabel:                       m.RejectButtonLabel.String(),
		AcceptFreeText:                          one(m.AcceptFreeText.String()),
		AcceptFreeTextHint:                      m.AcceptFreeTextHint.String(),
		AcceptFreeTextValidation:                encodedPrefixedField(m.AcceptFreeTextValidation.String()),
		AcceptFreeTextValidationFailedErrorText: m.AcceptFreeTextValidationFailedText.String(),
	}
}

func one(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "1")
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

type encodedText string

func (s encodedText) MarshalText() ([]byte, error) {
	return []byte("encodedValue=" + base64.StdEncoding.EncodeToString([]byte(s))), nil
}

func (s *encodedText) UnmarshalText(text []byte) error {
	value := string(text)
	if encoded, ok := strings.CutPrefix(value, "encodedValue="); ok {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode encoded text: %w", err)
		}
		value = string(decoded)
	}
	*s = encodedText(value)
	return nil
}

func encodedPrefixedField(value string) string {
	var text encodedText
	if err := text.UnmarshalText([]byte(value)); err != nil {
		// Preserve malformed carrier text rather than silently discarding it.
		return value
	}
	return string(text)
}

func encodedDownloadField(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "encodedValue=")
	if value == "" {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return value
	}
	return string(data)
}

func activationCodeField(value string) string {
	value = encodedDownloadField(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "LPA:") {
		return value
	}
	return "LPA:" + value
}

func smdpFQDN(value string) string {
	values := splitList(encodedDownloadField(value))
	if len(values) == 0 {
		return ""
	}
	fqdn := values[0]
	if u, err := url.Parse(fqdn); err == nil && u.Host != "" {
		return u.Host
	}
	return fqdn
}

type operationList []Operation

func (ops operationList) String() string {
	values := make([]string, 0, len(ops))
	for _, op := range ops {
		values = append(values, op.String())
	}
	return strings.Join(values, ",")
}

func (ops operationList) MarshalText() ([]byte, error) { return []byte(ops.String()), nil }

func (ops *operationList) UnmarshalText(text []byte) error {
	var next operationList
	for _, name := range splitList(string(text)) {
		next = append(next, Operation(name))
	}
	*ops = next
	return nil
}
