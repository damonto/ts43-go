package ts43

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/damonto/ts43-go/sim"
)

type ServiceEntitlementRequest struct {
	EntitlementVersion  string
	AuthenticationToken string
	TemporaryToken      string
	App                 string
	TerminalID          string
	TerminalVendor      string
	TerminalModel       string
	TerminalSoftware    string
	AppName             string
	AppVersion          string
	Accept              string
	GID1                string
	ClientTS43          string
	OmitEAPID           bool
	Cookies             []string
}

var errAppRequired = errors.New("app is required")

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

func buildHTTPRequest(ctx context.Context, entitlement Entitlement, identity sim.Identity, req ServiceEntitlementRequest, op ODSAOperation, clientTS43, realm string) (*http.Request, error) {
	if req.App == "" {
		return nil, errAppRequired
	}
	params := make(url.Values)
	if err := addAuthIdentity(params, identity, req, realm); err != nil {
		return nil, err
	}
	addCommon(params, req)
	if op.Operation != "" {
		addOperation(params, op)
	}
	server, err := url.Parse(entitlement.Server)
	if err != nil {
		return nil, fmt.Errorf("parse entitlement server: %w", err)
	}
	accept := req.Accept
	if accept == "" {
		accept = DefaultAccept
	}
	var httpReq *http.Request
	if entitlement.UsePost {
		data, err := json.Marshal(jsonBodyFields(params))
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
	httpReq.Header.Set("User-Agent", userAgent(req, clientTS43))
	for _, cookie := range req.Cookies {
		if cookie != "" {
			httpReq.Header.Add("Cookie", cookie)
		}
	}
	return httpReq, nil
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

func addAuthIdentity(params url.Values, identity sim.Identity, req ServiceEntitlementRequest, realm string) error {
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
	eapID, err := sim.IMSIEAPIdentity(identity.MCCMNC(), identity.IMSI, realm)
	if err != nil {
		return err
	}
	setFields(params, formField{"EAP_ID", eapID})
	return nil
}

func addCommon(params url.Values, req ServiceEntitlementRequest) {
	setFields(params,
		formField{"terminal_id", req.TerminalID},
		formField{"app", req.App},
		formField{"terminal_vendor", truncate(req.TerminalVendor, 4)},
		formField{"terminal_model", truncate(req.TerminalModel, 10)},
		formField{"terminal_sw_version", truncate(req.TerminalSoftware, 20)},
		formField{"vers", "0"},
		formField{"entitlement_version", req.EntitlementVersion},
	)
	setOptional(params,
		formField{"GID1", req.GID1},
		formField{"app_version", req.AppVersion},
		formField{"app_name", req.AppName},
	)
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
	if len(ops) == 0 {
		return ""
	}
	targets := make([]string, 0, len(ops))
	for _, op := range ops {
		targets = append(targets, op.String())
	}
	return strings.Join(targets, ",")
}

func userAgent(req ServiceEntitlementRequest, clientTS43 string) string {
	if clientTS43 == "" {
		clientTS43 = DefaultODSAClientTS43
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
