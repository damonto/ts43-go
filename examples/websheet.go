package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/damonto/ts43-go"
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
	fmt.Fprintln(w, "The injected JS adapters are available at examples/odsa_service_flow.js and examples/vowifi_service_flow.js.")
	fmt.Fprintln(w, "Paste the JSON message emitted by an adapter, a JS ODSAServiceFlow/VoWiFiWebServiceFlow call,")
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
	if isWebsheetCallbackText(line) {
		var callback ts43.WebsheetResult
		if err := callback.UnmarshalText([]byte(line)); err != nil {
			return nil, err
		}
		return &callback, nil
	}
	return parseWebsheetCommand(line)
}

func isWebsheetCallbackText(line string) bool {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "TS43_ODSA_CALLBACK") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "TS43_ODSA_CALLBACK"))
	}
	if strings.HasPrefix(line, "TS43_VOWIFI_CALLBACK") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "TS43_VOWIFI_CALLBACK"))
	}
	return strings.HasPrefix(line, "{") ||
		strings.Contains(line, "ODSAServiceFlow.") ||
		strings.Contains(line, "VoWiFiWebServiceFlow.") ||
		strings.Contains(line, "/_callback")
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
