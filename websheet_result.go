package ts43

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type websheetCallbackPayload struct {
	Source             string `json:"source"`
	Event              string `json:"event"`
	ActivationCode     string `json:"activationCode"`
	DefaultSMDPAddress string `json:"defaultSmdpAddress"`
	SMDPFQDN           string `json:"smdpFqdn"`
	ICCID              string `json:"iccid"`
	IMEI               string `json:"imei"`
	NextAction         string `json:"nextAction"`
}

func (r *WebsheetResult) UnmarshalText(text []byte) error {
	line := strings.TrimSpace(string(text))
	if line == "" {
		return errors.New("websheet callback is required")
	}
	vowifiCallback := strings.HasPrefix(line, "TS43_VOWIFI_CALLBACK")
	line = trimCallbackLogPrefix(line, "TS43_ODSA_CALLBACK")
	line = trimCallbackLogPrefix(line, "TS43_VOWIFI_CALLBACK")

	var next WebsheetResult
	var err error
	switch {
	case strings.HasPrefix(line, "{"):
		next, err = websheetResultFromJSON(line, vowifiCallback)
	case strings.Contains(line, "ODSAServiceFlow."):
		next, err = websheetResultFromJS(line)
	case strings.Contains(line, "VoWiFiWebServiceFlow."):
		next, err = websheetResultFromVoWiFiJS(line)
	case strings.Contains(line, "/_callback"):
		next, err = websheetResultFromURL(line)
	default:
		return fmt.Errorf("unknown websheet callback %q", line)
	}
	if err != nil {
		return err
	}
	*r = next
	return nil
}

func trimCallbackLogPrefix(line, prefix string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return line
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix))
}

func websheetResultFromJSON(line string, vowifiCallback bool) (WebsheetResult, error) {
	var payload websheetCallbackPayload
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return WebsheetResult{}, fmt.Errorf("parse websheet JSON callback: %w", err)
	}
	if vowifiCallback || strings.EqualFold(payload.Source, "vowifi") {
		return websheetResultFromVoWiFiName(payload.Event)
	}
	values := url.Values{}
	values.Set("activationCode", payload.ActivationCode)
	values.Set("defaultSmdpAddress", firstNonEmpty(payload.DefaultSMDPAddress, payload.SMDPFQDN))
	values.Set("iccid", payload.ICCID)
	values.Set("imei", payload.IMEI)
	values.Set("nextAction", payload.NextAction)
	return websheetResultFromName(payload.Event, nil, values)
}

func websheetResultFromJS(line string) (WebsheetResult, error) {
	_, after, ok := strings.Cut(line, "ODSAServiceFlow.")
	if !ok {
		return WebsheetResult{}, errors.New("ODSAServiceFlow callback is required")
	}
	name, rest, ok := strings.Cut(after, "(")
	if !ok {
		return WebsheetResult{}, errors.New("callback argument list is required")
	}
	argsText, _, ok := strings.Cut(rest, ")")
	if !ok {
		return WebsheetResult{}, errors.New("callback argument list is not closed")
	}
	return websheetResultFromName(name, splitJSArgs(argsText), nil)
}

func websheetResultFromVoWiFiJS(line string) (WebsheetResult, error) {
	_, after, ok := strings.Cut(line, "VoWiFiWebServiceFlow.")
	if !ok {
		return WebsheetResult{}, errors.New("VoWiFiWebServiceFlow callback is required")
	}
	name, _, ok := strings.Cut(after, "(")
	if !ok {
		return WebsheetResult{}, errors.New("callback argument list is required")
	}
	return websheetResultFromVoWiFiName(name)
}

func websheetResultFromVoWiFiName(name string) (WebsheetResult, error) {
	switch normalizeCallbackName(name) {
	case "entitlementchanged":
		return WebsheetResult{Event: WebsheetEventVoWiFiEntitlementChanged}, nil
	case "dismissflow":
		return WebsheetResult{Event: WebsheetEventVoWiFiDismissFlow}, nil
	default:
		return WebsheetResult{}, fmt.Errorf("unknown VoWiFi websheet callback %q", name)
	}
}

func websheetResultFromURL(raw string) (WebsheetResult, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return WebsheetResult{}, fmt.Errorf("parse callback URL: %w", err)
	}
	query := u.Query()
	name := firstValue(query, "event", "callback", "action", "method")
	if name == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
	}
	if normalized := normalizeCallbackName(name); normalized == "callback" || normalized == "esim" {
		name = callbackNameFromQuery(query)
	}
	return websheetResultFromName(name, nil, query)
}

func websheetResultFromName(name string, args []string, query url.Values) (WebsheetResult, error) {
	switch normalizeCallbackName(name) {
	case "profilereadywithactivationcode", "activation":
		activationCode := argOrQuery(args, query, 0, "activationCode", "activation_code")
		if activationCode == "" {
			return WebsheetResult{}, errors.New("activation code is required")
		}
		return WebsheetResult{
			Event:          WebsheetEventProfileReadyWithActivationCode,
			ActivationCode: activationCode,
			ICCID:          argOrQuery(args, query, 1, "iccid", "ICCID"),
			IMEI:           argOrQuery(args, query, 2, "imei", "IMEI"),
		}, nil
	case "profilereadywithdefaultsmdp", "defaultsmdp":
		smdp := argOrQuery(args, query, 0, "defaultSmdpAddress", "smdp", "smdpFqdn")
		iccid := argOrQuery(args, query, 1, "iccid", "ICCID")
		if smdp == "" || iccid == "" {
			return WebsheetResult{}, errors.New("default SM-DP+ and ICCID are required")
		}
		return WebsheetResult{
			Event:    WebsheetEventProfileReadyWithDefaultSMDP,
			SMDPFQDN: smdp,
			ICCID:    iccid,
			IMEI:     argOrQuery(args, query, 2, "imei", "IMEI"),
		}, nil
	case "selectioncompleted", "select":
		iccid := argOrQuery(args, query, 0, "iccid", "ICCID")
		imei := argOrQuery(args, query, 1, "imei", "IMEI")
		if iccid == "" && imei == "" {
			return WebsheetResult{}, errors.New("selected ICCID or IMEI is required")
		}
		return WebsheetResult{Event: WebsheetEventSelectionCompleted, ICCID: iccid, IMEI: imei}, nil
	case "finishflow", "done":
		return WebsheetResult{
			Event:      WebsheetEventFinishFlow,
			NextAction: operationFromCallbackText(argOrQuery(args, query, 0, "next_action", "nextAction")),
		}, nil
	case "dismissflow", "dismiss":
		return WebsheetResult{Event: WebsheetEventDismissFlow}, nil
	case "deletetoken":
		return WebsheetResult{Event: WebsheetEventDeleteToken}, nil
	case "checkprofileservicestatus", "status":
		return WebsheetResult{Event: WebsheetEventCheckProfileServiceStatus}, nil
	case "deleteprofileinuse", "deleteprofile":
		return WebsheetResult{
			Event: WebsheetEventDeleteProfileInUse,
			ICCID: argOrQuery(args, query, 0, "iccid", "ICCID"),
		}, nil
	case "entitlementchanged":
		return WebsheetResult{Event: WebsheetEventVoWiFiEntitlementChanged}, nil
	default:
		return WebsheetResult{}, fmt.Errorf("unknown websheet callback %q", name)
	}
}

func callbackNameFromQuery(query url.Values) string {
	switch {
	case firstValue(query, "activationCode", "activation_code") != "":
		return "profileReadyWithActivationCode"
	case firstValue(query, "defaultSmdpAddress", "smdp", "smdpFqdn") != "":
		return "profileReadyWithDefaultSmdp"
	case firstValue(query, "next_action", "nextAction") != "":
		return "finishFlow"
	default:
		return ""
	}
}

func operationFromCallbackText(s string) Operation {
	switch normalizeCallbackName(s) {
	case "":
		return ""
	case "acquireconfiguration", "acquireconf", "configuration":
		return OperationAcquireConf
	case "managesubscription", "manage":
		return OperationManage
	default:
		return Operation(s)
	}
}

func splitJSArgs(text string) []string {
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range text {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ',':
			args = append(args, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	last := strings.TrimSpace(b.String())
	if last != "" || text != "" {
		args = append(args, last)
	}
	return args
}

func normalizeCallbackName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "()")
	var b strings.Builder
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func argOrQuery(args []string, query url.Values, index int, keys ...string) string {
	if index < len(args) && args[index] != "" {
		return args[index]
	}
	return firstValue(query, keys...)
}

func firstValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			return value
		}
	}
	return ""
}
