package runtimehost

import (
	"context"

	"github.com/iniwex5/vowifi-go/internal/vowifi/events"
	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
)

type imsEventBridge struct {
	dispatcher eventhost.Dispatcher
}

func (b *imsEventBridge) OnIMSEvent(event events.Event) {
	if b == nil || b.dispatcher == nil {
		return
	}
	eventDispatcherAdapter{dispatch: b.dispatcher}.Dispatch(context.Background(), event)
}

type eventDispatcherAdapter struct {
	dispatch eventhost.Dispatcher
}

func (adapter eventDispatcherAdapter) Dispatch(ctx context.Context, event events.Event) {
	if adapter.dispatch == nil {
		return
	}
	adapter.dispatch.Dispatch(ctx, moduleEventFromInternal(event))
}

func moduleEventFromInternal(event events.Event) eventhost.Event {
	switch value := event.(type) {
	case events.EventSMSReceived:
		return publicSMSReceived(value)
	case *events.EventSMSReceived:
		return publicSMSReceived(*value)
	case events.EventSMSSent:
		return publicSMSSent(value)
	case *events.EventSMSSent:
		return publicSMSSent(*value)
	case events.EventLocalNumberLearned:
		return publicLocalNumberLearned(value)
	case *events.EventLocalNumberLearned:
		return publicLocalNumberLearned(*value)
	case events.EventLogNotify:
		return eventhost.LogNotify{DevID: value.DevID, Message: value.Message}
	case *events.EventLogNotify:
		return eventhost.LogNotify{DevID: value.DevID, Message: value.Message}
	case events.EventMWIUpdated:
		return publicMWIUpdated(value)
	case *events.EventMWIUpdated:
		return publicMWIUpdated(*value)
	case events.EventCallWaiting:
		return publicCallWaiting(value)
	case *events.EventCallWaiting:
		return publicCallWaiting(*value)
	default:
		if event == nil {
			return eventhost.Generic{}
		}
		return eventhost.Generic{
			EventType: event.Type(), DevID: event.DeviceID(), TypeName: event.Type(),
		}
	}
}

func publicSMSReceived(value events.EventSMSReceived) eventhost.SMSReceived {
	return eventhost.SMSReceived{
		DevID: value.DevID, Sender: value.Sender, TargetURI: value.TargetURI,
		Content: value.Content, Time: value.Time,
		FragmentSessionKey: value.FragmentSessionKey, Incomplete: value.Incomplete,
	}
}

func publicSMSSent(value events.EventSMSSent) eventhost.SMSSent {
	return eventhost.SMSSent{
		DevID: value.DevID, TargetURI: value.TargetURI, Content: value.Content,
		Time: value.Time, TotalParts: value.TotalParts, MessageID: value.MessageID,
		ICCID: value.ICCID, IMSI: value.IMSI,
	}
}

func publicLocalNumberLearned(value events.EventLocalNumberLearned) eventhost.LocalNumberLearned {
	return eventhost.LocalNumberLearned{
		DevID: value.DevID, IMSI: value.IMSI, Number: value.Number, Source: value.Source,
		Time: value.Time,
	}
}

func publicMWIUpdated(value events.EventMWIUpdated) eventhost.MWIUpdated {
	return eventhost.MWIUpdated{
		DevID: value.DevID, MessagesWaiting: value.MessagesWaiting,
		VoiceNew: value.VoiceNew, VoiceOld: value.VoiceOld,
		Account: value.Account, Time: value.Time,
	}
}

func publicCallWaiting(value events.EventCallWaiting) eventhost.CallWaiting {
	received := value.ReceivedAt
	if received.IsZero() {
		received = value.Time
	}
	return eventhost.CallWaiting{
		DevID: value.DevID, CallID: value.CallID, Caller: value.Caller,
		Callee: value.Callee, ActiveID: value.ActiveID, Time: received,
	}
}
