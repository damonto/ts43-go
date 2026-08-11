package main

import (
	"bufio"
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	sgp22 "github.com/damonto/euicc-go/v2"
	"github.com/damonto/ts43-go"
)

func TestChooseReader(t *testing.T) {
	tests := []struct {
		name      string
		readers   []string
		requested string
		input     string
		want      string
		wantErr   bool
	}{
		{
			name:      "requested reader exists",
			readers:   []string{"Source", "Target"},
			requested: "Target",
			want:      "Target",
		},
		{
			name:      "requested reader missing",
			readers:   []string{"Source"},
			requested: "Missing",
			wantErr:   true,
		},
		{
			name:    "empty prompt selects first reader",
			readers: []string{"Source", "Target"},
			input:   "\n",
			want:    "Source",
		},
		{
			name:    "invalid prompt retries",
			readers: []string{"Source", "Target"},
			input:   "x\n2\n",
			want:    "Target",
		},
		{
			name:    "no readers",
			readers: nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(bytes.NewBufferString(tt.input))
			var out bytes.Buffer
			got, err := chooseReader(reader, &out, tt.readers, tt.requested, "source")
			if tt.wantErr {
				if err == nil {
					t.Fatal("chooseReader error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("chooseReader error = %v", err)
			}
			if got != tt.want {
				t.Errorf("chooseReader = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReaderDisplayName(t *testing.T) {
	if got := readerDisplayName(1, "ACS ACR39U ICC Reader 00 00"); got != "2. ACS ACR39U ICC Reader 00 00" {
		t.Errorf("readerDisplayName = %q", got)
	}
}

func TestParseFlagsTransferType(t *testing.T) {
	args := []string{
		"-server", "https://example.test/ts43",
		"-source-imei", "111111111111111",
		"-target-imei", "222222222222222",
		"-source-psim",
		"-transfer-type", "2",
	}
	got, err := parseFlags(args, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags error = %v", err)
	}
	if got.SourceSIMType != ts43.SIMTypePSIM {
		t.Errorf("SourceSIMType = %v, want %v", got.SourceSIMType, ts43.SIMTypePSIM)
	}
	if got.TransferType != 2 {
		t.Errorf("TransferType = %d, want 2", got.TransferType)
	}
}

func TestParseFlagsConversion(t *testing.T) {
	args := []string{
		"-server", "https://example.test/ts43",
		"-source-imei", "111111111111111",
		"-target-imei", "222222222222222",
		"-source-psim",
		"-use-temp-token-conversion",
		"-set-target-terminal-fields",
		"-restrict-old-terminal-fields",
	}
	got, err := parseFlags(args, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseFlags error = %v", err)
	}
	if got.SourceSIMType != ts43.SIMTypePSIM {
		t.Errorf("SourceSIMType = %v, want %v", got.SourceSIMType, ts43.SIMTypePSIM)
	}
	if !got.UseTempTokenConversion {
		t.Error("UseTempTokenConversion = false, want true")
	}
	if !got.SetTargetTerminalFields {
		t.Error("SetTargetTerminalFields = false, want true")
	}
	if !got.RestrictOldTerminalFields {
		t.Error("RestrictOldTerminalFields = false, want true")
	}
}

func TestValidateAllowsDiscoveryWithoutServer(t *testing.T) {
	opts := cliOptions{
		SourceDevice: ts43.Device{IMEI: "111111111111111"},
		TargetDevice: ts43.Device{IMEI: "222222222222222"},
	}
	if err := opts.validate(); err != nil {
		t.Errorf("validate() error = %v", err)
	}
}

func TestValidateAllowsExplicitServer(t *testing.T) {
	opts := cliOptions{
		Server:       "https://example.test/ts43",
		SourceDevice: ts43.Device{IMEI: "111111111111111"},
		TargetDevice: ts43.Device{IMEI: "222222222222222"},
	}
	if err := opts.validate(); err != nil {
		t.Errorf("validate() error = %v", err)
	}
}

func TestChooseReaderWithDefaultRejectsUnavailableReader(t *testing.T) {
	tests := []struct {
		name         string
		readers      []string
		requested    string
		input        string
		defaultIndex int
		unavailable  string
		want         string
		wantErr      bool
	}{
		{
			name:         "defaults to second reader when first is source",
			readers:      []string{"Source", "Target"},
			input:        "\n",
			defaultIndex: 1,
			unavailable:  "Source",
			want:         "Target",
		},
		{
			name:        "requested source reader is rejected",
			readers:     []string{"Source", "Target"},
			requested:   "Source",
			unavailable: "Source",
			wantErr:     true,
		},
		{
			name:        "interactive source reader selection retries",
			readers:     []string{"Source", "Target"},
			input:       "1\n2\n",
			unavailable: "Source",
			want:        "Target",
		},
		{
			name:        "only source reader available",
			readers:     []string{"Source"},
			unavailable: "Source",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(bytes.NewBufferString(tt.input))
			var out bytes.Buffer
			got, err := chooseReaderWithDefault(reader, &out, tt.readers, tt.requested, "target", tt.defaultIndex, tt.unavailable)
			if tt.wantErr {
				if err == nil {
					t.Fatal("chooseReaderWithDefault error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("chooseReaderWithDefault error = %v", err)
			}
			if got != tt.want {
				t.Errorf("chooseReaderWithDefault = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultReaderIndex(t *testing.T) {
	tests := []struct {
		name        string
		readers     []string
		unavailable string
		want        int
	}{
		{
			name:        "second reader when first is unavailable",
			readers:     []string{"Source", "Target"},
			unavailable: "Source",
			want:        1,
		},
		{
			name:        "first reader when different",
			readers:     []string{"Source", "Target"},
			unavailable: "Other",
			want:        0,
		},
		{
			name:        "first reader for single reader",
			readers:     []string{"Source"},
			unavailable: "Source",
			want:        0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultReaderIndex(tt.readers, tt.unavailable); got != tt.want {
				t.Errorf("defaultReaderIndex = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTS43ActivationCode(t *testing.T) {
	tests := []struct {
		name         string
		cfg          ts43.DownloadConfig
		wantSMDP     string
		wantMatching string
		wantIMEI     string
		wantErr      bool
	}{
		{
			name: "carrier activation code",
			cfg: ts43.DownloadConfig{
				ActivationCode: "LPA:1$smdp.example$matching",
				IMEI:           "123456789012345",
			},
			wantSMDP:     "smdp.example",
			wantMatching: "matching",
			wantIMEI:     "123456789012345",
		},
		{
			name: "smdp and matching id",
			cfg: ts43.DownloadConfig{
				SMDPFQDN:   "https://smdp.example",
				MatchingID: "event-id",
				IMEI:       "123456789012345",
			},
			wantSMDP:     "smdp.example",
			wantMatching: "event-id",
			wantIMEI:     "123456789012345",
		},
		{
			name:    "missing download address",
			cfg:     ts43.DownloadConfig{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ts43ActivationCode(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ts43ActivationCode error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ts43ActivationCode error = %v", err)
			}
			if got.SMDP.Host != tt.wantSMDP {
				t.Errorf("SMDP host = %q, want %q", got.SMDP.Host, tt.wantSMDP)
			}
			if got.MatchingID != tt.wantMatching {
				t.Errorf("MatchingID = %q, want %q", got.MatchingID, tt.wantMatching)
			}
			if got.IMEI != tt.wantIMEI {
				t.Errorf("IMEI = %q, want %q", got.IMEI, tt.wantIMEI)
			}
		})
	}
}

func TestNotificationSequenceHelpers(t *testing.T) {
	notifications := []*sgp22.NotificationMetadata{
		{SequenceNumber: 5},
		nil,
		{SequenceNumber: 2},
		{SequenceNumber: 9},
		{SequenceNumber: 7},
	}

	if got := maxNotificationSequence(notifications); got != 9 {
		t.Errorf("maxNotificationSequence = %d, want 9", got)
	}
	after := notificationsAfter(notifications, 5)
	if len(after) != 2 {
		t.Fatalf("notificationsAfter length = %d, want 2", len(after))
	}
	if after[0].SequenceNumber != 7 || after[1].SequenceNumber != 9 {
		t.Errorf("notificationsAfter sequence = [%d %d], want [7 9]", after[0].SequenceNumber, after[1].SequenceNumber)
	}
}

func TestConfirmSourceProfileDeletion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes short", input: "y\n", want: true},
		{name: "yes long", input: "yes\n", want: true},
		{name: "empty defaults no", input: "\n"},
		{name: "no short", input: "n\n"},
		{name: "no long", input: "no\n"},
		{name: "invalid then yes", input: "later\ny\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(bytes.NewBufferString(tt.input))
			var out bytes.Buffer

			got, err := confirmSourceProfileDeletion(reader, &out, "8944476500001224158")
			if err != nil {
				t.Fatalf("confirmSourceProfileDeletion error = %v", err)
			}
			if got != tt.want {
				t.Errorf("confirmSourceProfileDeletion = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDeleteSourceProfile(t *testing.T) {
	sourceICCID := mustTestICCID(t, "8944476500001224158")
	otherICCID := mustTestICCID(t, "8944476500001224166")
	deleteErr := errors.New("delete failed")
	handleErr := errors.New("handle failed")

	tests := []struct {
		name         string
		input        string
		event        ts43.SourceProfileDeletionEvent
		lists        [][]*sgp22.NotificationMetadata
		disableErr   error
		deleteErr    error
		handleErr    error
		wantErr      string
		wantDecline  bool
		wantDisabled []string
		wantDeleted  []string
		wantHandled  []sgp22.SequenceNumber
	}{
		{
			name:  "deletes source profile and sends matching delete notification",
			input: "y\n",
			event: ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			lists: [][]*sgp22.NotificationMetadata{
				{
					{SequenceNumber: 3, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: sourceICCID},
				},
				{
					{SequenceNumber: 3, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: sourceICCID},
					{SequenceNumber: 4, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: otherICCID},
					{SequenceNumber: 5, ProfileManagementOperation: sgp22.NotificationEventEnable, ICCID: sourceICCID},
					{SequenceNumber: 6, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: sourceICCID},
				},
			},
			wantDisabled: []string{sourceICCID.String()},
			wantDeleted:  []string{sourceICCID.String()},
			wantHandled: []sgp22.SequenceNumber{
				6,
			},
		},
		{
			name:        "declined",
			input:       "\n",
			event:       ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			wantDecline: true,
		},
		{
			name:    "missing ICCID",
			input:   "y\n",
			event:   ts43.SourceProfileDeletionEvent{},
			wantErr: "source profile ICCID is required",
		},
		{
			name:    "invalid ICCID",
			input:   "y\n",
			event:   ts43.SourceProfileDeletionEvent{ICCID: "12x"},
			wantErr: "encode source ICCID",
		},
		{
			name:      "delete error",
			input:     "y\n",
			event:     ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			lists:     [][]*sgp22.NotificationMetadata{{}},
			deleteErr: deleteErr,
			wantErr:   "delete source profile",
		},
		{
			name:       "disable error",
			input:      "y\n",
			event:      ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			lists:      [][]*sgp22.NotificationMetadata{{}},
			disableErr: errors.New("disable failed"),
			wantErr:    "disable source profile",
		},
		{
			name:  "missing matching delete notification",
			input: "y\n",
			event: ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			lists: [][]*sgp22.NotificationMetadata{
				{},
				{
					{SequenceNumber: 4, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: otherICCID},
				},
			},
			wantErr: "delete notification for source ICCID",
		},
		{
			name:  "handle notification error",
			input: "yes\n",
			event: ts43.SourceProfileDeletionEvent{ICCID: sourceICCID.String()},
			lists: [][]*sgp22.NotificationMetadata{
				{},
				{
					{SequenceNumber: 4, ProfileManagementOperation: sgp22.NotificationEventDelete, ICCID: sourceICCID},
				},
			},
			handleErr: handleErr,
			wantErr:   "handle notification 4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeProfileDeletionClient{
				lists:      tt.lists,
				disableErr: tt.disableErr,
				deleteErr:  tt.deleteErr,
				handleErr:  tt.handleErr,
			}
			reader := bufio.NewReader(bytes.NewBufferString(tt.input))
			var out bytes.Buffer

			err := deleteSourceProfile(reader, &out, fake, tt.event)
			if tt.wantDecline {
				if !errors.Is(err, errSourceProfileDeletionDeclined) {
					t.Errorf("deleteSourceProfile error = %v, want errSourceProfileDeletionDeclined", err)
				}
				if len(fake.deleted) != 0 {
					t.Errorf("deleted profiles = %v, want none", fake.deleted)
				}
				return
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("deleteSourceProfile error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("deleteSourceProfile error = %v", err)
			}
			if !slices.Equal(fake.disabled, tt.wantDisabled) {
				t.Errorf("disabled profiles = %v, want %v", fake.disabled, tt.wantDisabled)
			}
			if !slices.Equal(fake.deleted, tt.wantDeleted) {
				t.Errorf("deleted profiles = %v, want %v", fake.deleted, tt.wantDeleted)
			}
			if !slices.Equal(fake.handled, tt.wantHandled) {
				t.Errorf("handled notifications = %v, want %v", fake.handled, tt.wantHandled)
			}
			for _, filters := range fake.listFilters {
				if !slices.Equal(filters, []sgp22.NotificationEvent{sgp22.NotificationEventDelete}) {
					t.Errorf("ListNotification filters = %v, want delete only", filters)
				}
			}
		})
	}
}

func TestHTTPSURL(t *testing.T) {
	tests := []struct {
		name     string
		address  string
		wantHost string
		wantErr  bool
	}{
		{
			name:     "fqdn",
			address:  "lpa.ds.gsma.com",
			wantHost: "lpa.ds.gsma.com",
		},
		{
			name:     "https URL",
			address:  "https://example.test/path",
			wantHost: "example.test",
		},
		{
			name:    "missing host",
			address: "https:///path",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := httpsURL(tt.address)
			if tt.wantErr {
				if err == nil {
					t.Fatal("httpsURL error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("httpsURL error = %v", err)
			}
			if got.Scheme != "https" {
				t.Errorf("scheme = %q, want https", got.Scheme)
			}
			if got.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", got.Host, tt.wantHost)
			}
		})
	}
}

type fakeProfileDeletionClient struct {
	lists       [][]*sgp22.NotificationMetadata
	listFilters [][]sgp22.NotificationEvent
	disableErr  error
	deleteErr   error
	handleErr   error
	disabled    []string
	deleted     []string
	handled     []sgp22.SequenceNumber
}

func (f *fakeProfileDeletionClient) DisableProfile(identifier any, refresh bool) error {
	iccid, ok := identifier.(sgp22.ICCID)
	if !ok {
		return errors.New("identifier is not ICCID")
	}
	f.disabled = append(f.disabled, iccid.String())
	if f.disableErr != nil {
		return f.disableErr
	}
	return nil
}

func (f *fakeProfileDeletionClient) DeleteProfile(identifier any) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	iccid, ok := identifier.(sgp22.ICCID)
	if !ok {
		return errors.New("identifier is not ICCID")
	}
	f.deleted = append(f.deleted, iccid.String())
	return nil
}

func (f *fakeProfileDeletionClient) ListNotification(filters ...sgp22.NotificationEvent) ([]*sgp22.NotificationMetadata, error) {
	f.listFilters = append(f.listFilters, slices.Clone(filters))
	if len(f.lists) == 0 {
		return nil, nil
	}
	notifications := f.lists[0]
	f.lists = f.lists[1:]
	return notifications, nil
}

func (f *fakeProfileDeletionClient) RetrieveNotificationList(searchCriteria any) ([]*sgp22.PendingNotification, error) {
	sequenceNumber, ok := searchCriteria.(sgp22.SequenceNumber)
	if !ok {
		return nil, errors.New("search criteria is not sequence number")
	}
	return []*sgp22.PendingNotification{
		{Notification: &sgp22.NotificationMetadata{SequenceNumber: sequenceNumber}},
	}, nil
}

func (f *fakeProfileDeletionClient) HandleNotification(pendingNotification *sgp22.PendingNotification) error {
	if f.handleErr != nil {
		return f.handleErr
	}
	f.handled = append(f.handled, pendingNotification.Notification.SequenceNumber)
	return nil
}

func mustTestICCID(t *testing.T, value string) sgp22.ICCID {
	t.Helper()
	iccid, err := sgp22.NewICCID(value)
	if err != nil {
		t.Fatalf("NewICCID(%q) error = %v", value, err)
	}
	return iccid
}

func TestParseWebsheetCallback(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		wantEvent      ts43.WebsheetCallbackEvent
		wantActivation string
		wantSMDP       string
		wantICCID      string
		wantIMEI       string
		wantNext       ts43.Operation
	}{
		{
			name:      "done command",
			line:      "DONE AcquireConfiguration",
			wantEvent: ts43.WebsheetEventFinishFlow,
			wantNext:  ts43.OperationAcquireConf,
		},
		{
			name:           "activation JS callback",
			line:           "ODSAServiceFlow.profileReadyWithActivationCode('LPA:1$smdp.example$matching','8900000000000000000','123456789012345')",
			wantEvent:      ts43.WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
			wantIMEI:       "123456789012345",
		},
		{
			name:      "default smdp JSON callback",
			line:      `{"event":"profileReadyWithDefaultSmdp","defaultSmdpAddress":"smdp.example","iccid":"8900000000000000000","imei":"123456789012345"}`,
			wantEvent: ts43.WebsheetEventProfileReadyWithDefaultSMDP,
			wantSMDP:  "smdp.example",
			wantICCID: "8900000000000000000",
			wantIMEI:  "123456789012345",
		},
		{
			name:      "prefixed adapter JSON callback",
			line:      `TS43_ODSA_CALLBACK {"event":"finishFlow","nextAction":"AcquireConfiguration","source":"odsa"}`,
			wantEvent: ts43.WebsheetEventFinishFlow,
			wantNext:  ts43.OperationAcquireConf,
		},
		{
			name:           "callback URL activation",
			line:           "https://carrier.test/_callback/esim?activationCode=LPA%3A1%24smdp.example%24matching&iccid=8900000000000000000",
			wantEvent:      ts43.WebsheetEventProfileReadyWithActivationCode,
			wantActivation: "LPA:1$smdp.example$matching",
			wantICCID:      "8900000000000000000",
		},
		{
			name:      "delete profile command",
			line:      "DELETE_PROFILE 8900000000000000000",
			wantEvent: ts43.WebsheetEventDeleteProfileInUse,
			wantICCID: "8900000000000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWebsheetCallback(tt.line)
			if err != nil {
				t.Fatalf("parseWebsheetCallback error = %v", err)
			}
			if got.Event != tt.wantEvent {
				t.Errorf("Event = %d, want %d", got.Event, tt.wantEvent)
			}
			if got.ActivationCode != tt.wantActivation {
				t.Errorf("ActivationCode = %q, want %q", got.ActivationCode, tt.wantActivation)
			}
			if got.SMDPFQDN != tt.wantSMDP {
				t.Errorf("SMDPFQDN = %q, want %q", got.SMDPFQDN, tt.wantSMDP)
			}
			if got.ICCID != tt.wantICCID {
				t.Errorf("ICCID = %q, want %q", got.ICCID, tt.wantICCID)
			}
			if got.IMEI != tt.wantIMEI {
				t.Errorf("IMEI = %q, want %q", got.IMEI, tt.wantIMEI)
			}
			if got.NextAction != tt.wantNext {
				t.Errorf("NextAction = %q, want %q", got.NextAction, tt.wantNext)
			}
		})
	}
}
