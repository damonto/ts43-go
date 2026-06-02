package ts43

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
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
	Message               Message
	Websheet              Websheet
	Raw                   []byte
}

func (r *ODSAResponse) UnmarshalBinary(data []byte) error {
	r.Raw = append(r.Raw[:0], data...)
	if json.Valid(data) {
		return r.unmarshalJSON(data)
	}
	return r.unmarshalXML(data)
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
		return err
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
		Token       *tokenFields       `json:"TOKEN"`
		Application *applicationFields `json:"APPLICATION"`
	}
	if err := json.Unmarshal(data, &named); err != nil {
		return err
	}
	var root applicationFields
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	p.Token = named.Token
	p.Application = named.Application
	p.Root = root
	return nil
}

func (p *odsaPayload) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
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
				p.Application = &app
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
	OperationResult                 stringField     `json:"OperationResult"`
	PrimaryAppEligibility           stringField     `json:"PrimaryAppEligibility"`
	SubscriptionResult              stringField     `json:"SubscriptionResult"`
	PollingInterval                 stringField     `json:"PollingInterval"`
	PollingIntervalUnit             stringField     `json:"PollingIntervalUnit"`
	TemporaryToken                  stringField     `json:"TemporaryToken"`
	TemporaryTokenExpiry            stringField     `json:"TemporaryTokenExpiry"`
	OperationTargets                stringField     `json:"OperationTargets"`
	RegisterNotifStatus             stringField     `json:"RegisterNotifStatus"`
	GeneralErrorText                stringField     `json:"GeneralErrorText"`
	SubscriptionServiceURL          stringField     `json:"SubscriptionServiceURL"`
	SubscriptionServiceUserData     stringField     `json:"SubscriptionServiceUserData"`
	SubscriptionServiceContentsType stringField     `json:"SubscriptionServiceContentsType"`
	PrimaryConfiguration            *primaryFields  `json:"PrimaryConfiguration"`
	Download                        *downloadFields `json:"DownloadInfo"`
	Message                         *messageFields  `json:"MSG"`
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

type primaryFields struct {
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
	ProfileSmdpAddress    stringField `json:"ProfileSmdpAddress"`
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
		f.ProfileSmdpAddress = stringField(value)
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
	}
	var download downloadFields
	switch {
	case app.Download != nil:
		download = *app.Download
	case primary.Download != nil:
		download = *primary.Download
	}
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
	r.PollingIntervalSet = strings.TrimSpace(pollingInterval) != ""
	r.PollingInterval = atoi(pollingInterval)
	r.PollingIntervalUnit = atoi(pollingIntervalUnit)
	r.TemporaryToken = app.TemporaryToken.String()
	r.TemporaryTokenExpiry = app.TemporaryTokenExpiry.String()
	r.OperationTargets = splitOperations(app.OperationTargets.String())
	r.RegisterNotifStatus = atoi(app.RegisterNotifStatus.String())
	r.GeneralErrorText = app.GeneralErrorText.String()
	r.AuthenticationToken = token.Token.String()
	r.AuthTokenValidity = atoi(token.Validity.String())
	r.Message = message.message()
	if r.SubscriptionResult == SubscriptionResultContinueToWebsheet {
		r.Websheet = Websheet{
			URL:          app.SubscriptionServiceURL.String(),
			UserData:     encodedPrefixedField(app.SubscriptionServiceUserData.String()),
			ContentsType: app.SubscriptionServiceContentsType.String(),
		}
	}
	r.Download.ActivationCode = activationCodeField(download.ProfileActivationCode.String())
	smdp := download.ProfileSmdpAddress.String()
	if smdp == "" {
		smdp = download.SubscriptionService.String()
	}
	r.Download.SMDPFQDN = smdpFQDN(smdp)
	r.Download.ProfileICCID = download.ProfileICCID.String()
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

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
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

func splitOperations(s string) []Operation {
	parts := splitList(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]Operation, 0, len(parts))
	for _, part := range parts {
		out = append(out, Operation(part))
	}
	return out
}

func encodedPrefixedField(value string) string {
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
	value = encodedPrefixedField(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "LPA:") {
		return value
	}
	return "LPA:" + value
}

func smdpFQDN(value string) string {
	values := splitList(encodedPrefixedField(value))
	if len(values) == 0 {
		return ""
	}
	fqdn := values[0]
	if u, err := url.Parse(fqdn); err == nil && u.Host != "" {
		return u.Host
	}
	return fqdn
}
