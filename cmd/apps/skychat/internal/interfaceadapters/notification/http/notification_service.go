// Package channel contains code of the notification service of interfaceadapters
package channel

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app/notification"
)

// NotificationService provides a channel implementation of the Service
type NotificationService struct {
	notifCh chan string
}

// NewNotificationService constructor for NotificationService
func NewNotificationService() *NotificationService {
	n := NotificationService{}

	return &n
}

// Notify sends out the notifications to the channel
func (ns *NotificationService) Notify(notification notification.Notification) error {
	jsonNotification, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	fmt.Printf("Notify: \n")
	fmt.Printf("%s \n", jsonNotification)
	ns.GetChannel() <- string(jsonNotification)
	return nil
}

//InitChannel inits the channel with make
func (ns *NotificationService) InitChannel() {
	ns.notifCh = make(chan string)
}

// GetChannel returns the channel of the notification service
func (ns *NotificationService) GetChannel() chan string {
	return ns.notifCh
}

//Defer Channel includes all messages when the channel has to be deferred
func (ns *NotificationService) DeferChannel() {
	close(ns.notifCh)
	ns.notifCh = nil
	fmt.Println("SSE: client connection is closed")
}
