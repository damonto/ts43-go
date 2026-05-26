package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/damonto/ts43-go/ts43"
)

func ts43Websheet(reader *bufio.Reader, w io.Writer, event ts43.WebsheetEvent) (*ts43.WebsheetResult, error) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Carrier requires a websheet before transfer can continue.")
	if event.Websheet.ContentsType == "" {
		fmt.Fprintln(w, "method=GET")
	} else {
		fmt.Fprintln(w, "method=POST")
		fmt.Fprintf(w, "content_type=%s\n", event.Websheet.ContentsType)
	}
	fmt.Fprintf(w, "url=%s\n", event.Websheet.URL)
	if event.Websheet.UserData != "" {
		fmt.Fprintf(w, "user_data=%s\n", event.Websheet.UserData)
	}
	fmt.Fprintln(w, "Open the URL in a WebView/browser and return with one callback.")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  DONE [AcquireConfiguration|ManageSubscription]")
	fmt.Fprintln(w, "  ACTIVATION <activation-code> [iccid] [imei]")
	fmt.Fprintln(w, "  DEFAULT_SMDP <smdp-fqdn> <iccid> [imei]")
	fmt.Fprintln(w, "  SELECT <old-iccid> [old-imei]")
	fmt.Fprintln(w, "  STATUS")
	fmt.Fprintln(w, "  DELETE_TOKEN")
	fmt.Fprintln(w, "  DELETE_PROFILE <iccid>")
	fmt.Fprintln(w, "  DISMISS")
	fmt.Fprintln(w, "The injected JS adapter is available at examples/odsa_service_flow.js.")
	fmt.Fprintln(w, "Paste the JSON message emitted by that adapter, a JS ODSAServiceFlow call,")
	fmt.Fprintln(w, "or a callback URL containing /_callback or /_callback/esim.")

	for {
		line, err := prompt(reader, w, "websheet> ")
		if err != nil {
			return nil, err
		}
		callback, err := parseWebsheetCallback(line)
		if err != nil {
			fmt.Fprintf(w, "Invalid callback: %v\n", err)
			continue
		}
		return callback, nil
	}
}

func parseWebsheetCallback(line string) (*ts43.WebsheetResult, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("websheet callback is required")
	}
	line = trimCallbackLogPrefix(line, "TS43_ODSA_CALLBACK")
	if strings.HasPrefix(line, "{") {
		return parseWebsheetJSONCallback(line)
	}
	if strings.Contains(line, "ODSAServiceFlow.") {
		return parseWebsheetJSCallback(line)
	}
	if strings.Contains(line, "/_callback") {
		return parseWebsheetCallbackURL(line)
	}
	return parseWebsheetCommand(line)
}

func trimCallbackLogPrefix(line, prefix string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return line
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix))
}

type websheetCallbackPayload struct {
	Event              string `json:"event"`
	ActivationCode     string `json:"activationCode"`
	DefaultSmdpAddress string `json:"defaultSmdpAddress"`
	SMDPFQDN           string `json:"smdpFqdn"`
	ICCID              string `json:"iccid"`
	IMEI               string `json:"imei"`
	NextAction         string `json:"nextAction"`
}

func parseWebsheetJSONCallback(line string) (*ts43.WebsheetResult, error) {
	var payload websheetCallbackPayload
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil, fmt.Errorf("parse websheet JSON callback: %w", err)
	}
	values := url.Values{}
	values.Set("activationCode", payload.ActivationCode)
	values.Set("defaultSmdpAddress", firstNonEmpty(payload.DefaultSmdpAddress, payload.SMDPFQDN))
	values.Set("iccid", payload.ICCID)
	values.Set("imei", payload.IMEI)
	values.Set("nextAction", payload.NextAction)
	return websheetResultFromName(payload.Event, nil, values)
}

func parseWebsheetCommand(line string) (*ts43.WebsheetResult, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil, errors.New("websheet command is required")
	}
	switch strings.ToUpper(fields[0]) {
	case "DONE":
		callback := &ts43.WebsheetResult{Event: ts43.WebsheetEventFinishFlow}
		if len(fields) > 1 {
			callback.NextAction = operationFromText(fields[1])
		}
		return callback, nil
	case "ACTIVATION":
		if len(fields) < 2 {
			return nil, errors.New("ACTIVATION requires an activation code")
		}
		callback := &ts43.WebsheetResult{
			Event:          ts43.WebsheetEventProfileReadyWithActivationCode,
			ActivationCode: fields[1],
		}
		if len(fields) > 2 {
			callback.ICCID = fields[2]
		}
		if len(fields) > 3 {
			callback.IMEI = fields[3]
		}
		return callback, nil
	case "DEFAULT_SMDP":
		if len(fields) < 3 {
			return nil, errors.New("DEFAULT_SMDP requires an SM-DP+ FQDN and ICCID")
		}
		callback := &ts43.WebsheetResult{
			Event:    ts43.WebsheetEventProfileReadyWithDefaultSMDP,
			SMDPFQDN: fields[1],
			ICCID:    fields[2],
		}
		if len(fields) > 3 {
			callback.IMEI = fields[3]
		}
		return callback, nil
	case "SELECT":
		if len(fields) < 2 {
			return nil, errors.New("SELECT requires an ICCID or IMEI")
		}
		callback := &ts43.WebsheetResult{
			Event: ts43.WebsheetEventSelectionCompleted,
			ICCID: fields[1],
		}
		if len(fields) > 2 {
			callback.IMEI = fields[2]
		}
		return callback, nil
	case "STATUS":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventCheckProfileServiceStatus}, nil
	case "DELETE_TOKEN":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventDeleteToken}, nil
	case "DELETE_PROFILE":
		if len(fields) < 2 {
			return nil, errors.New("DELETE_PROFILE requires an ICCID")
		}
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventDeleteProfileInUse, ICCID: fields[1]}, nil
	case "DISMISS":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventDismissFlow}, nil
	default:
		return nil, fmt.Errorf("unknown websheet command %q", fields[0])
	}
}

func parseWebsheetJSCallback(line string) (*ts43.WebsheetResult, error) {
	_, after, ok := strings.Cut(line, "ODSAServiceFlow.")
	if !ok {
		return nil, errors.New("ODSAServiceFlow callback is required")
	}
	name, rest, ok := strings.Cut(after, "(")
	if !ok {
		return nil, errors.New("callback argument list is required")
	}
	argsText, _, ok := strings.Cut(rest, ")")
	if !ok {
		return nil, errors.New("callback argument list is not closed")
	}
	return websheetResultFromName(name, splitJSArgs(argsText), nil)
}

func parseWebsheetCallbackURL(raw string) (*ts43.WebsheetResult, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse callback URL: %w", err)
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

func websheetResultFromName(name string, args []string, query url.Values) (*ts43.WebsheetResult, error) {
	switch normalizeCallbackName(name) {
	case "profilereadywithactivationcode", "activation":
		activationCode := argOrQuery(args, query, 0, "activationCode", "activation_code")
		if activationCode == "" {
			return nil, errors.New("activation code is required")
		}
		return &ts43.WebsheetResult{
			Event:          ts43.WebsheetEventProfileReadyWithActivationCode,
			ActivationCode: activationCode,
			ICCID:          argOrQuery(args, query, 1, "iccid", "ICCID"),
			IMEI:           argOrQuery(args, query, 2, "imei", "IMEI"),
		}, nil
	case "profilereadywithdefaultsmdp", "defaultsmdp":
		smdp := argOrQuery(args, query, 0, "defaultSmdpAddress", "smdp", "smdpFqdn")
		iccid := argOrQuery(args, query, 1, "iccid", "ICCID")
		if smdp == "" || iccid == "" {
			return nil, errors.New("default SM-DP+ and ICCID are required")
		}
		return &ts43.WebsheetResult{
			Event:    ts43.WebsheetEventProfileReadyWithDefaultSMDP,
			SMDPFQDN: smdp,
			ICCID:    iccid,
			IMEI:     argOrQuery(args, query, 2, "imei", "IMEI"),
		}, nil
	case "selectioncompleted", "select":
		iccid := argOrQuery(args, query, 0, "iccid", "ICCID")
		imei := argOrQuery(args, query, 1, "imei", "IMEI")
		if iccid == "" && imei == "" {
			return nil, errors.New("selected ICCID or IMEI is required")
		}
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventSelectionCompleted, ICCID: iccid, IMEI: imei}, nil
	case "finishflow", "done":
		return &ts43.WebsheetResult{
			Event:      ts43.WebsheetEventFinishFlow,
			NextAction: operationFromText(argOrQuery(args, query, 0, "next_action", "nextAction")),
		}, nil
	case "dismissflow", "dismiss":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventDismissFlow}, nil
	case "deletetoken":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventDeleteToken}, nil
	case "checkprofileservicestatus", "status":
		return &ts43.WebsheetResult{Event: ts43.WebsheetEventCheckProfileServiceStatus}, nil
	case "deleteprofileinuse", "deleteprofile":
		return &ts43.WebsheetResult{
			Event: ts43.WebsheetEventDeleteProfileInUse,
			ICCID: argOrQuery(args, query, 0, "iccid", "ICCID"),
		}, nil
	default:
		return nil, fmt.Errorf("unknown websheet callback %q", name)
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

func operationFromText(s string) ts43.Operation {
	switch normalizeCallbackName(s) {
	case "":
		return ""
	case "acquireconfiguration", "acquireconf", "configuration":
		return ts43.OperationAcquireConf
	case "managesubscription", "manage":
		return ts43.OperationManage
	default:
		return ts43.Operation(s)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
