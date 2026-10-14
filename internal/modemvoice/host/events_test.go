package host

import (
	"sync"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

func TestNotificationsIsolateDevicesAndPreserveDeviceOrder(t *testing.T) {
	c := New(Options{})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	received := make(chan string, 4)
	c.SubscribeIncomingCalls(func(in voicehost.IncomingCall) {
		if in.DeviceID == "A" {
			close(entered)
			<-release
		}
		received <- in.DeviceID + ":incoming"
	})
	c.SubscribeCallEvents(func(ev voicehost.CallEvent) { received <- ev.DeviceID + ":" + ev.Type })
	c.publish(notification{incoming: &voicehost.IncomingCall{DeviceID: "A"}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("device A callback did not start")
	}
	c.publish(notification{event: voicehost.CallEvent{DeviceID: "A", Type: "CallEnded"}})
	c.publish(notification{incoming: &voicehost.IncomingCall{DeviceID: "B"}})
	c.publish(notification{event: voicehost.CallEvent{DeviceID: "B", Type: "CallEnded"}})
	expectNotification(t, received, "B:incoming")
	expectNotification(t, received, "B:CallEnded")
	unblock()
	expectNotification(t, received, "A:incoming")
	expectNotification(t, received, "A:CallEnded")
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.notifications) == 0
	})
}

func TestNotificationQueueRestartsAndAllowsReentrantPublish(t *testing.T) {
	c := New(Options{})
	received := make(chan string, 2)
	unsubscribe := c.SubscribeIncomingCalls(func(in voicehost.IncomingCall) {
		c.publish(notification{event: voicehost.CallEvent{DeviceID: in.DeviceID, Type: "CallEnded"}})
		received <- "incoming"
	})
	c.SubscribeCallEvents(func(ev voicehost.CallEvent) { received <- ev.Type })
	for i := 0; i < 3; i++ {
		c.publish(notification{incoming: &voicehost.IncomingCall{DeviceID: "A"}})
		expectNotification(t, received, "incoming")
		expectNotification(t, received, "CallEnded")
		waitFor(t, func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			return len(c.notifications) == 0
		})
	}
	unsubscribe()
	c.publish(notification{incoming: &voicehost.IncomingCall{DeviceID: "A"}})
	c.publish(notification{event: voicehost.CallEvent{DeviceID: "A", Type: "after unsubscribe"}})
	expectNotification(t, received, "after unsubscribe")
}

func expectNotification(t *testing.T, received <-chan string, want string) {
	t.Helper()
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("notification = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("notification %q blocked", want)
	}
}
