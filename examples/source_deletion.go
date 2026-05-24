package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/damonto/euicc-go/ts43"
	sgp22 "github.com/damonto/euicc-go/v2"
)

var errSourceProfileDeletionDeclined = errors.New("source profile deletion declined")

type profileDeletionClient interface {
	DisableProfile(identifier any, refresh bool) error
	DeleteProfile(identifier any) error
	ListNotification(filters ...sgp22.NotificationEvent) ([]*sgp22.NotificationMetadata, error)
	RetrieveNotificationList(searchCriteria any) ([]*sgp22.PendingNotification, error)
	HandleNotification(pendingNotification *sgp22.PendingNotification) error
}

func deleteSourceProfile(reader *bufio.Reader, w io.Writer, client profileDeletionClient, event ts43.SourceProfileDeletionEvent) error {
	if event.ICCID == "" {
		return errors.New("source profile ICCID is required")
	}
	iccid, err := sgp22.NewICCID(event.ICCID)
	if err != nil {
		return fmt.Errorf("encode source ICCID: %w", err)
	}
	confirmed, err := confirmSourceProfileDeletion(reader, w, event.ICCID)
	if err != nil {
		return err
	}
	if !confirmed {
		return errSourceProfileDeletionDeclined
	}

	lastSequence, err := lastDeleteNotificationSequence(client)
	if err != nil {
		return err
	}
	if err := client.DisableProfile(iccid, true); err != nil && !errors.Is(err, sgp22.ErrProfileNotInEnabledState) {
		return fmt.Errorf("disable source profile: %w", err)
	}
	if err := client.DeleteProfile(iccid); err != nil {
		return fmt.Errorf("delete source profile: %w", err)
	}
	if err := sendDeleteNotificationsAfter(client, lastSequence, iccid); err != nil {
		return err
	}
	return nil
}

func confirmSourceProfileDeletion(reader *bufio.Reader, w io.Writer, iccid string) (bool, error) {
	for {
		answer, err := prompt(reader, w, fmt.Sprintf("Delete source profile %s? [y/N] ", iccid))
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		default:
			fmt.Fprintln(w, "Answer yes or no.")
		}
	}
}

func lastDeleteNotificationSequence(client profileDeletionClient) (sgp22.SequenceNumber, error) {
	notifications, err := client.ListNotification(sgp22.NotificationEventDelete)
	if err != nil {
		return 0, fmt.Errorf("list delete notifications before source deletion: %w", err)
	}
	return maxNotificationSequence(notifications), nil
}

func sendDeleteNotificationsAfter(client profileDeletionClient, lastSequence sgp22.SequenceNumber, iccid sgp22.ICCID) error {
	notifications, err := client.ListNotification(sgp22.NotificationEventDelete)
	if err != nil {
		return fmt.Errorf("list delete notifications after source deletion: %w", err)
	}
	var sent bool
	for _, notification := range notificationsAfter(notifications, lastSequence) {
		if notification.ProfileManagementOperation != sgp22.NotificationEventDelete {
			continue
		}
		if notification.ICCID.String() != iccid.String() {
			continue
		}
		if err := sendNotification(client, notification.SequenceNumber); err != nil {
			return err
		}
		sent = true
	}
	if !sent {
		return fmt.Errorf("delete notification for source ICCID %s is missing", iccid)
	}
	return nil
}
