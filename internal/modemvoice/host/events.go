package host

import "github.com/iniwex5/vowifi-go/runtimehost/voicehost"

// Notifications are ordered per device and delivered outside device locks.
// A callback may synchronously query/reject a call without blocking other devices.
type notification struct {
	incoming *voicehost.IncomingCall
	event    voicehost.CallEvent
}

type notificationQueue struct {
	pending []notification
}

func (c *Controller) SubscribeIncomingCalls(fn func(voicehost.IncomingCall)) func() {
	c.mu.Lock()
	index := len(c.incoming)
	c.incoming = append(c.incoming, fn)
	c.mu.Unlock()
	return func() { c.mu.Lock(); c.incoming[index] = nil; c.mu.Unlock() }
}

func (c *Controller) SubscribeCallEvents(fn func(voicehost.CallEvent)) func() {
	c.mu.Lock()
	index := len(c.events)
	c.events = append(c.events, fn)
	c.mu.Unlock()
	return func() { c.mu.Lock(); c.events[index] = nil; c.mu.Unlock() }
}

func (c *Controller) publish(n notification) {
	deviceID := n.event.DeviceID
	if n.incoming != nil {
		deviceID = n.incoming.DeviceID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.notifications == nil {
		c.notifications = make(map[string]*notificationQueue)
	}
	queue := c.notifications[deviceID]
	if queue == nil {
		queue = &notificationQueue{}
		c.notifications[deviceID] = queue
		go c.dispatch(deviceID, queue)
	}
	queue.pending = append(queue.pending, n)
}

func (c *Controller) dispatch(deviceID string, queue *notificationQueue) {
	for {
		c.mu.Lock()
		if len(queue.pending) == 0 {
			delete(c.notifications, deviceID)
			c.mu.Unlock()
			return
		}
		n := queue.pending[0]
		queue.pending[0] = notification{}
		queue.pending = queue.pending[1:]
		incoming := append([]func(voicehost.IncomingCall){}, c.incoming...)
		events := append([]func(voicehost.CallEvent){}, c.events...)
		c.mu.Unlock()
		if n.incoming != nil {
			for _, fn := range incoming {
				if fn != nil {
					fn(*n.incoming)
				}
			}
		} else {
			for _, fn := range events {
				if fn != nil {
					fn(n.event)
				}
			}
		}
	}
}
