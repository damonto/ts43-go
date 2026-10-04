package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/damonto/euicc-go/lpa"
	sgp22 "github.com/damonto/euicc-go/v2"
	"github.com/damonto/ts43-go"
)

func runTransfer(ctx context.Context, reader *bufio.Reader, w io.Writer, client *ts43.Client, lpaClient *lpa.Client, sourceLPA func() (*lpa.Client, error)) error {
	result, err := client.Transfer(ctx)
	if err != nil {
		return err
	}
	for {
		switch event := result.Event.(type) {
		case ts43.UserInputEvent:
			answer, err := ts43UserInput(reader, w, event)
			if err != nil {
				return err
			}
			result, err = client.Continue(ctx, result, ts43.ContinueRequest{UserInput: answer})
		case ts43.WebsheetEvent:
			callback, err := ts43Websheet(reader, w, event)
			if err != nil {
				return err
			}
			result, err = client.Continue(ctx, result, ts43.ContinueRequest{Websheet: callback})
		case ts43.SourceProfileDeletionEvent:
			if sourceLPA == nil {
				return errors.New("source LPA client factory is required")
			}
			sourceClient, err := sourceLPA()
			if err != nil {
				return fmt.Errorf("create source LPA client: %w", err)
			}
			if err := deleteSourceProfile(reader, w, sourceClient, event); err != nil {
				return err
			}
			result, err = client.Continue(ctx, result, ts43.ContinueRequest{SourceProfileDeleted: true})
		case ts43.DownloadReadyEvent:
			iccid, err := downloadTS43Profile(ctx, reader, w, lpaClient, event.Config)
			if err != nil {
				return err
			}
			result, err = client.CompleteActivation(ctx, result, ts43.ActivationResult{ICCID: iccid})
		case ts43.SMDSDiscoveryEvent:
			iccid, err := downloadTS43DiscoveredProfile(ctx, reader, w, lpaClient, event)
			if err != nil {
				return err
			}
			result, err = client.CompleteActivation(ctx, result, ts43.ActivationResult{ICCID: iccid})
		case ts43.DelayedDownloadEvent:
			iccid, err := ts43DelayedDownloadICCID(reader, w, event)
			if err != nil {
				return err
			}
			result, err = client.CompleteActivation(ctx, result, ts43.ActivationResult{ICCID: iccid})
		case ts43.ConfigurationPendingEvent:
			fmt.Fprintln(w, "Waiting for the carrier to prepare download configuration.")
			if _, err := prompt(reader, w, "After the carrier signals readiness, press Enter to check again: "); err != nil {
				return err
			}
			result, err = client.Continue(ctx, result, ts43.ContinueRequest{})
		case ts43.ActivationPendingEvent:
			fmt.Fprintf(w, "TS.43 activation is pending for ICCID %s.\n", event.ICCID)
			if _, err := prompt(reader, w, "After the carrier signals readiness, press Enter to check again: "); err != nil {
				return err
			}
			result, err = client.Continue(ctx, result, ts43.ContinueRequest{})
		case ts43.DismissEvent:
			fmt.Fprintf(w, "TS.43 transfer dismissed by carrier; subscription_result=%d\n", event.SubscriptionResult)
			return nil
		case ts43.DoneEvent:
			fmt.Fprintln(w, "TS.43 transfer completed")
			return nil
		default:
			return fmt.Errorf("unexpected TS.43 event %T", result.Event)
		}
		if err != nil {
			return err
		}
	}
}

func downloadTS43DiscoveredProfile(ctx context.Context, reader *bufio.Reader, w io.Writer, client *lpa.Client, event ts43.SMDSDiscoveryEvent) (string, error) {
	cfg, err := ts43SMDSDownloadConfig(reader, w, client, event)
	if err != nil {
		return "", err
	}
	return downloadTS43Profile(ctx, reader, w, client, cfg)
}

func ts43DelayedDownloadICCID(reader *bufio.Reader, w io.Writer, event ts43.DelayedDownloadEvent) (string, error) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Carrier delayed the profile download.")
	fmt.Fprintf(w, "source_iccid=%s\n", event.SourceICCID)
	fmt.Fprintf(w, "target_eid=%s\n", event.TargetEID)
	fmt.Fprintf(w, "target_imei=%s\n", event.TargetIMEI)
	fmt.Fprintln(w, "Download/install the profile through the carrier-provided path, then handle install and enable notifications.")
	return prompt(reader, w, "Installed ICCID: ")
}

func ts43SMDSDownloadConfig(reader *bufio.Reader, w io.Writer, client *lpa.Client, event ts43.SMDSDiscoveryEvent) (ts43.DownloadConfig, error) {
	if event.TargetIMEI == "" {
		return ts43.DownloadConfig{}, errors.New("target IMEI is required for SM-DS discovery")
	}
	imei, err := sgp22.NewIMEI(event.TargetIMEI)
	if err != nil {
		return ts43.DownloadConfig{}, fmt.Errorf("encode target IMEI: %w", err)
	}
	address, err := ts43SMDSAddress(reader, w, client)
	if err != nil {
		return ts43.DownloadConfig{}, err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Carrier requested delayed download through SM-DS discovery.")
	fmt.Fprintf(w, "source_iccid=%s\n", event.SourceICCID)
	fmt.Fprintf(w, "target_eid=%s\n", event.TargetEID)
	fmt.Fprintf(w, "target_imei=%s\n", event.TargetIMEI)
	fmt.Fprintf(w, "smds=%s\n", address.Host)

	entries, err := client.Discovery(address, imei)
	if err != nil {
		return ts43.DownloadConfig{}, fmt.Errorf("discover SM-DS profiles: %w", err)
	}
	if len(entries) == 0 {
		return ts43.DownloadConfig{}, errors.New("SM-DS returned no profiles")
	}
	entry, err := selectSMDSProfile(reader, w, entries)
	if err != nil {
		return ts43.DownloadConfig{}, err
	}
	if entry.Address == "" {
		return ts43.DownloadConfig{}, errors.New("discovered profile does not contain an SM-DP+ address")
	}
	if entry.EventID == "" {
		return ts43.DownloadConfig{}, errors.New("discovered profile does not contain an event ID")
	}
	return ts43.DownloadConfig{
		SMDPFQDN:   entry.Address,
		MatchingID: entry.EventID,
		IMEI:       event.TargetIMEI,
	}, nil
}

func ts43SMDSAddress(reader *bufio.Reader, w io.Writer, client *lpa.Client) (*url.URL, error) {
	addresses, err := client.EUICCConfiguredAddresses()
	if err != nil {
		return nil, fmt.Errorf("read eUICC configured addresses: %w", err)
	}
	address := addresses.RootSMDSAddress
	if address == "" {
		address, err = prompt(reader, w, "SM-DS address [lpa.ds.gsma.com]: ")
		if err != nil {
			return nil, err
		}
		if address == "" {
			address = "lpa.ds.gsma.com"
		}
	}
	return httpsURL(address)
}

func httpsURL(address string) (*url.URL, error) {
	if strings.Contains(address, "://") {
		u, err := url.Parse(address)
		if err != nil {
			return nil, fmt.Errorf("parse address: %w", err)
		}
		if u.Scheme == "" {
			u.Scheme = "https"
		}
		if u.Host == "" {
			return nil, errors.New("address host is required")
		}
		return u, nil
	}
	return &url.URL{Scheme: "https", Host: address}, nil
}

func selectSMDSProfile(reader *bufio.Reader, w io.Writer, entries []*sgp22.EventEntry) (*sgp22.EventEntry, error) {
	if len(entries) == 1 {
		entry := entries[0]
		fmt.Fprintf(w, "Discovered profile: %s, URL: %s\n", entry.EventID, entry.Address)
		return entry, nil
	}
	for i := range len(entries) {
		entry := entries[i]
		fmt.Fprintf(w, "%d. EventID: %s, URL: %s\n", i+1, entry.EventID, entry.Address)
	}
	for {
		answer, err := prompt(reader, w, "Profile number: ")
		if err != nil {
			return nil, err
		}
		index, err := strconv.Atoi(answer)
		if err != nil || index < 1 || index > len(entries) {
			fmt.Fprintln(w, "Choose one of the discovered profiles.")
			continue
		}
		return entries[index-1], nil
	}
}

func ts43UserInput(reader *bufio.Reader, w io.Writer, event ts43.UserInputEvent) (*ts43.UserInputResult, error) {
	if event.Message.Title != "" {
		fmt.Fprintln(w, event.Message.Title)
	}
	if event.Message.Text != "" {
		fmt.Fprintln(w, event.Message.Text)
	}
	answer := ts43.UserInputResult{Button: ts43.MessageButtonAccepted}
	if event.Message.AcceptFreeText {
		label := event.Message.AcceptFreeTextHint
		if label == "" {
			label = "Response"
		}
		response, err := prompt(reader, w, label+": ")
		if err != nil {
			return nil, err
		}
		answer.Response = response
	}
	if event.Message.RejectButton {
		button, err := prompt(reader, w, "Accept? [Y/n] ")
		if err != nil {
			return nil, err
		}
		button = strings.ToLower(button)
		if button == "n" || button == "no" {
			answer.Button = ts43.MessageButtonRejected
		}
	}
	return &answer, nil
}
