package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/damonto/euicc-go/lpa"
	"github.com/damonto/euicc-go/ts43"
	sgp22 "github.com/damonto/euicc-go/v2"
)

func downloadTS43Profile(ctx context.Context, reader *bufio.Reader, w io.Writer, client *lpa.Client, cfg ts43.DownloadConfig) (string, error) {
	ac, err := ts43ActivationCode(cfg)
	if err != nil {
		return "", err
	}
	lastSequence, err := lastNotificationSequence(client)
	if err != nil {
		return "", err
	}
	result, err := client.DownloadProfile(ctx, ac, &lpa.DownloadOptions{
		OnProgress: func(stage lpa.DownloadStage) {
			fmt.Fprintln(w, stage)
		},
		OnConfirm: func(metadata *sgp22.ProfileInfo) bool {
			fmt.Fprintf(w, "Confirm download of profile %s with ICCID %s\n", metadata.ProfileName, metadata.ICCID)
			answer, err := prompt(reader, w, "Download? [Y/n] ")
			if err != nil {
				fmt.Fprintf(w, "read confirmation: %v\n", err)
				return false
			}
			return strings.ToLower(answer) != "n"
		},
		OnEnterConfirmationCode: func() string {
			code, err := prompt(reader, w, "Confirmation code: ")
			if err != nil {
				fmt.Fprintf(w, "read confirmation code: %v\n", err)
				return ""
			}
			return code
		},
	})
	if err != nil {
		return "", err
	}

	iccid, err := installedICCID(reader, w, cfg, result)
	if err != nil {
		return "", err
	}
	encodedICCID, err := sgp22.NewICCID(iccid)
	if err != nil {
		return "", fmt.Errorf("encode installed ICCID: %w", err)
	}
	if err := client.EnableProfile(encodedICCID, true); err != nil {
		return "", fmt.Errorf("enable profile: %w", err)
	}
	if err := sendNotificationsAfter(client, lastSequence); err != nil {
		return "", err
	}
	return iccid, nil
}

func ts43ActivationCode(cfg ts43.DownloadConfig) (*lpa.ActivationCode, error) {
	if cfg.ActivationCode != "" {
		var ac lpa.ActivationCode
		if err := ac.UnmarshalText([]byte(cfg.ActivationCode)); err != nil {
			return nil, err
		}
		ac.IMEI = cfg.IMEI
		return &ac, nil
	}
	if cfg.SMDPFQDN == "" {
		return nil, errors.New("TS.43 download config does not contain activation code or SM-DP+")
	}
	address, err := httpsURL(cfg.SMDPFQDN)
	if err != nil {
		return nil, err
	}
	return &lpa.ActivationCode{
		SMDP:       address,
		MatchingID: cfg.MatchingID,
		IMEI:       cfg.IMEI,
	}, nil
}

func installedICCID(reader *bufio.Reader, w io.Writer, cfg ts43.DownloadConfig, result *sgp22.LoadBoundProfilePackageResponse) (string, error) {
	if cfg.ProfileICCID != "" {
		return cfg.ProfileICCID, nil
	}
	if result != nil && result.Notification != nil {
		if iccid := result.Notification.ICCID.String(); iccid != "" {
			return iccid, nil
		}
	}
	return prompt(reader, w, "Installed ICCID: ")
}

func lastNotificationSequence(client *lpa.Client) (sgp22.SequenceNumber, error) {
	notifications, err := client.ListNotification()
	if err != nil {
		return 0, fmt.Errorf("list notifications before download: %w", err)
	}
	return maxNotificationSequence(notifications), nil
}

func maxNotificationSequence(notifications []*sgp22.NotificationMetadata) sgp22.SequenceNumber {
	var maxSequence sgp22.SequenceNumber
	for _, notification := range notifications {
		if notification != nil && notification.SequenceNumber > maxSequence {
			maxSequence = notification.SequenceNumber
		}
	}
	return maxSequence
}

func sendNotificationsAfter(client *lpa.Client, lastSequence sgp22.SequenceNumber) error {
	notifications, err := client.ListNotification()
	if err != nil {
		return fmt.Errorf("list notifications after enable: %w", err)
	}
	for _, notification := range notificationsAfter(notifications, lastSequence) {
		if err := sendNotification(client, notification.SequenceNumber); err != nil {
			return err
		}
	}
	return nil
}

func notificationsAfter(notifications []*sgp22.NotificationMetadata, lastSequence sgp22.SequenceNumber) []*sgp22.NotificationMetadata {
	var out []*sgp22.NotificationMetadata
	for _, notification := range notifications {
		if notification != nil && notification.SequenceNumber > lastSequence {
			out = append(out, notification)
		}
	}
	slices.SortFunc(out, func(a, b *sgp22.NotificationMetadata) int {
		switch {
		case a.SequenceNumber < b.SequenceNumber:
			return -1
		case a.SequenceNumber > b.SequenceNumber:
			return 1
		default:
			return 0
		}
	})
	return out
}

type notificationClient interface {
	RetrieveNotificationList(searchCriteria any) ([]*sgp22.PendingNotification, error)
	HandleNotification(pendingNotification *sgp22.PendingNotification) error
}

func sendNotification(client notificationClient, sequenceNumber sgp22.SequenceNumber) error {
	notifications, err := client.RetrieveNotificationList(sequenceNumber)
	if err != nil {
		return fmt.Errorf("retrieve notification %d: %w", sequenceNumber, err)
	}
	if len(notifications) == 0 {
		return fmt.Errorf("notification %d is missing", sequenceNumber)
	}
	if err := client.HandleNotification(notifications[0]); err != nil {
		return fmt.Errorf("handle notification %d: %w", sequenceNumber, err)
	}
	return nil
}
