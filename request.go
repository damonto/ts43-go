package ts43

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type ServiceEntitlementRequest struct {
	EntitlementVersion  string
	RequestVersion      string
	AuthenticationToken string
	TemporaryToken      string
	App                 string
	TerminalID          string
	TerminalVendor      string
	TerminalModel       string
	TerminalSoftware    string
	AppName             string
	AppVersion          string
	ProvisioningVersion string
	Accept              string
	GID1                string
	ClientTS43          string
	OmitEAPID           bool
	Cookies             []string
}

var errAppRequired = errors.New("app is required")

// ErrAndroidEntitlementRequired reports a carrier routed by Samsung through
// Android Ts43Operation, which this direct HTTP client cannot substitute.
var ErrAndroidEntitlementRequired = errors.New("carrier requires Android Ts43Operation entitlement backend")

type ODSAOperation struct {
	Operation                      Operation
	OperationType                  OperationType
	OperationTargets               []Operation
	TerminalICCID                  string
	TerminalEID                    string
	TargetTerminalID               string
	TargetTerminalICCID            string
	TargetTerminalEID              string
	TargetTerminalModel            string
	TargetTerminalSN               string
	TargetTerminalIMEIs            []string
	TargetTerminalEntitlementProto string
	CompanionTerminalID            string
	CompanionTerminalVendor        string
	CompanionTerminalModel         string
	CompanionTerminalSoftware      string
	CompanionTerminalFriendlyName  string
	CompanionTerminalService       string
	CompanionTerminalICCID         string
	CompanionTerminalEID           string
	OldTerminalID                  string
	OldTerminalICCID               string
	OldTerminalEntitlementProto    string
	MessageResponse                string
	MessageButton                  MessageButton
	MessageButtonSet               bool
}

func buildHTTPRequest(ctx context.Context, entitlement Entitlement, identity Identity, req ServiceEntitlementRequest, op ODSAOperation, clientTS43, realm string) (*http.Request, error) {
	if entitlement.SamsungAndroidEntitlement {
		return nil, ErrAndroidEntitlementRequired
	}
	req = shapeSamsungRequest(entitlement, req)
	if req.App == "" {
		return nil, errAppRequired
	}
	params := make(url.Values)
	if err := addAuthIdentity(params, identity, req, realm); err != nil {
		return nil, err
	}
	addCommon(params, entitlement, req)
	if op.Operation != "" {
		addOperation(params, op)
	}
	if err := addSamsungOperationFields(params, entitlement, identity, op, realm); err != nil {
		return nil, err
	}
	server, err := url.Parse(entitlement.Server)
	if err != nil {
		return nil, fmt.Errorf("parse entitlement server: %w", err)
	}
	accept := requestAccept(entitlement, req)
	var httpReq *http.Request
	if entitlement.UsePost {
		data, err := marshalRequestJSON(entitlement, params)
		if err != nil {
			return nil, fmt.Errorf("marshal TS.43 request: %w", err)
		}
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, server.String(), bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
	} else {
		query := server.Query()
		for key, values := range params {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		server.RawQuery = query.Encode()
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodGet, server.String(), nil)
		if err != nil {
			return nil, err
		}
	}
	if req.ClientTS43 != "" {
		clientTS43 = req.ClientTS43
	}
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("User-Agent", userAgent(entitlement, req, clientTS43))
	if entitlement.Channel == EntitlementChannelSamsung {
		httpReq.Header.Set("Accept-Language", "en-US,en;q=0.5")
	}
	for _, cookie := range req.Cookies {
		if cookie != "" {
			httpReq.Header.Add("Cookie", cookie)
		}
	}
	return httpReq, nil
}

func requestAccept(entitlement Entitlement, req ServiceEntitlementRequest) string {
	accept := responseAccept(entitlement, req)
	if initialEAPRequest(req) {
		if entitlement.Channel == EntitlementChannelSamsung {
			return EAPRelayContentType
		}
		return eapRelayAccept(accept)
	}
	return accept
}

func responseAccept(entitlement Entitlement, req ServiceEntitlementRequest) string {
	if req.Accept != "" {
		return req.Accept
	}
	if entitlement.Channel == EntitlementChannelSamsung {
		return SamsungResponseAccept
	}
	return DefaultResponseAccept
}

func initialEAPRequest(req ServiceEntitlementRequest) bool {
	return req.AuthenticationToken == "" && req.TemporaryToken == "" && !req.OmitEAPID
}

func eapRelayAccept(accept string) string {
	if accept == "" {
		accept = DefaultResponseAccept
	}
	if acceptHasMediaType(accept, EAPRelayContentType) {
		return accept
	}
	return EAPRelayContentType + ", " + accept
}

func acceptHasMediaType(accept, mediaType string) bool {
	for part := range strings.SplitSeq(accept, ",") {
		value, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(value, mediaType) {
			return true
		}
	}
	return false
}

func shapeSamsungRequest(entitlement Entitlement, req ServiceEntitlementRequest) ServiceEntitlementRequest {
	if entitlement.Channel != EntitlementChannelSamsung {
		return req
	}
	req.App = AppODSAPrimary
	return req
}

func addSamsungOperationFields(params url.Values, entitlement Entitlement, identity Identity, op ODSAOperation, realm string) error {
	if entitlement.Channel != EntitlementChannelSamsung || op.Operation == "" {
		return nil
	}
	if op.Operation == OperationManage && samsungDigitalizedTempToken4FF(entitlement) {
		params.Del("IMSI")
		return nil
	}
	if !samsungOperationUsesEAPIdentity(op.Operation) {
		params.Del("IMSI")
		return nil
	}
	params.Del("IMSI")
	eapIDBase64, err := terminalIMSIEAPBase64(identity, realm)
	if err != nil {
		return fmt.Errorf("build Samsung IMSI EAP identity: %w", err)
	}
	params.Set("IMSI", eapIDBase64)
	return nil
}

func samsungOperationUsesEAPIdentity(operation Operation) bool {
	return operation == OperationCheck || operation == OperationManage || operation == OperationAcquireConf
}

func terminalIMSIEAPBase64(identity Identity, realm string) (string, error) {
	eapID, err := IMSIEAPIdentity(mccMNC(identity), identity.IMSI, realm)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(eapID)), nil
}

func marshalRequestJSON(entitlement Entitlement, params url.Values) ([]byte, error) {
	if entitlement.Channel != EntitlementChannelSamsung {
		return json.Marshal(jsonBodyFields(params))
	}
	body := make(map[string]json.RawMessage, len(params))
	for name, values := range params {
		var data []byte
		var err error
		if name == "target_terminal_imeis" {
			data, err = json.Marshal(values)
		} else {
			data, err = json.Marshal(strings.Join(values, ","))
		}
		if err != nil {
			return nil, fmt.Errorf("marshal request field %s: %w", name, err)
		}
		body[name] = data
	}
	return json.Marshal(body)
}

func jsonBodyFields(params url.Values) map[string]string {
	body := make(map[string]string, len(params))
	for key, values := range params {
		if key == "target_terminal_imeis" {
			// Repeated JSON object keys keep the last value.
			for _, value := range values {
				body[key] = value
			}
			continue
		}
		body[key] = strings.Join(values, ",")
	}
	return body
}

func addAuthIdentity(params url.Values, identity Identity, req ServiceEntitlementRequest, realm string) error {
	if req.AuthenticationToken != "" {
		setFields(params,
			formField{"IMSI", identity.IMSI},
			formField{"token", req.AuthenticationToken},
		)
		return nil
	}
	if req.TemporaryToken != "" {
		setFields(params, formField{"temporary_token", req.TemporaryToken})
		return nil
	}
	if req.OmitEAPID {
		return nil
	}
	eapID, err := IMSIEAPIdentity(mccMNC(identity), identity.IMSI, realm)
	if err != nil {
		return err
	}
	setFields(params, formField{"EAP_ID", eapID})
	return nil
}

func addCommon(params url.Values, entitlement Entitlement, req ServiceEntitlementRequest) {
	version := req.RequestVersion
	if version == "" {
		version = "0"
	}
	vendor := req.TerminalVendor
	model := req.TerminalModel
	software := req.TerminalSoftware
	if entitlement.Channel == EntitlementChannelSamsung {
		if samsungVendorTruncated(req.EntitlementVersion) {
			vendor = truncate(vendor, 4)
		}
	} else {
		vendor = truncate(vendor, 4)
		model = truncate(model, 10)
		software = truncate(software, 20)
	}
	setFields(params,
		formField{"terminal_id", req.TerminalID},
		formField{"app", req.App},
		formField{"terminal_vendor", vendor},
		formField{"terminal_model", model},
		formField{"terminal_sw_version", software},
		formField{"vers", version},
		formField{"entitlement_version", req.EntitlementVersion},
	)
	setOptional(params,
		formField{"GID1", req.GID1},
		formField{"app_version", req.AppVersion},
		formField{"app_name", req.AppName},
		formField{"provisioning_version", req.ProvisioningVersion},
	)
}

func samsungVendorTruncated(version string) bool {
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= 5
}

func addOperation(params url.Values, op ODSAOperation) {
	params.Set("operation", op.Operation.String())
	if op.OperationType != OperationTypeOmit {
		params.Set("operation_type", op.OperationType.String())
	}
	setOptional(params,
		formField{"operation_targets", operationTargets(op.OperationTargets)},
		formField{"terminal_iccid", op.TerminalICCID},
		formField{"terminal_eid", op.TerminalEID},
		formField{"target_terminal_id", op.TargetTerminalID},
		formField{"target_terminal_iccid", op.TargetTerminalICCID},
		formField{"target_terminal_eid", op.TargetTerminalEID},
		formField{"target_terminal_model", op.TargetTerminalModel},
		formField{"target_terminal_sn", op.TargetTerminalSN},
		formField{"target_terminal_entitlement_protocol", op.TargetTerminalEntitlementProto},
		formField{"companion_terminal_id", op.CompanionTerminalID},
		formField{"companion_terminal_vendor", op.CompanionTerminalVendor},
		formField{"companion_terminal_model", op.CompanionTerminalModel},
		formField{"companion_terminal_sw_version", op.CompanionTerminalSoftware},
		formField{"companion_terminal_friendly_name", op.CompanionTerminalFriendlyName},
		formField{"companion_terminal_service", op.CompanionTerminalService},
		formField{"companion_terminal_iccid", op.CompanionTerminalICCID},
		formField{"companion_terminal_eid", op.CompanionTerminalEID},
		formField{"old_terminal_id", op.OldTerminalID},
		formField{"old_terminal_iccid", op.OldTerminalICCID},
		formField{"old_terminal_entitlement_protocol", op.OldTerminalEntitlementProto},
		formField{"MSG_response", op.MessageResponse},
	)
	if op.MessageButtonSet {
		params.Set("MSG_btn", op.MessageButton.String())
	}
	setOptionalValues(params, "target_terminal_imeis", op.TargetTerminalIMEIs)
}

type formField struct {
	name  string
	value string
}

func setFields(values url.Values, fields ...formField) {
	for _, field := range fields {
		values.Set(field.name, field.value)
	}
}

func setOptional(values url.Values, fields ...formField) {
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		values.Set(field.name, field.value)
	}
}

func setOptionalValues(values url.Values, name string, fields []string) {
	for _, value := range fields {
		if value == "" {
			continue
		}
		values.Add(name, value)
	}
}

func operationTargets(ops []Operation) string {
	return operationList(ops).String()
}

func userAgent(entitlement Entitlement, req ServiceEntitlementRequest, clientTS43 string) string {
	if clientTS43 == "" {
		clientTS43 = DefaultODSAClientTS43
	}
	if entitlement.Channel == EntitlementChannelSamsung {
		if entitlement.SamsungLegacyUserAgent {
			return req.TerminalModel + " Android" + req.TerminalSoftware
		}
		if !strings.HasPrefix(clientTS43, "client-") {
			clientTS43 = "client-" + clientTS43
		}
		return fmt.Sprintf("PRD-TS43 %s %s OS-Android/%s",
			req.TerminalModel,
			clientTS43,
			req.TerminalSoftware,
		)
	}
	return fmt.Sprintf("PRD-TS43 term-%s/%s %s/%s OS-Android/%s",
		truncate(req.TerminalVendor, 4),
		truncate(req.TerminalModel, 10),
		clientTS43,
		req.AppVersion,
		truncate(req.TerminalSoftware, 20),
	)
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func entitlementProtocol(protocol int) string {
	if protocol == 0 {
		return ""
	}
	return strconv.Itoa(protocol)
}

func cookieHeaders(cookies []*http.Cookie) []string {
	headers := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name != "" {
			headers = append(headers, cookie.Name+"="+cookie.Value)
		}
	}
	return headers
}

func mergeCookieHeaders(current, next []string) []string {
	if len(next) == 0 {
		return append([]string(nil), current...)
	}
	merged := append([]string(nil), current...)
	for _, cookie := range next {
		if cookie == "" {
			continue
		}
		name := cookie
		if before, _, ok := strings.Cut(cookie, "="); ok {
			name = before
		}
		replaced := false
		for i, existing := range merged {
			existingName := existing
			if before, _, ok := strings.Cut(existing, "="); ok {
				existingName = before
			}
			if existingName == name {
				merged[i] = cookie
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, cookie)
		}
	}
	return merged
}
