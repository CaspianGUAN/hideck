package device

import (
	"context"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
	"github.com/yibaiba/hideck/internal/smsnotify"
	"github.com/yibaiba/hideck/pkg/logger"
)

type poolVoWiFiRuntimeDispatcher struct {
	pool *Pool
}

func applyVoWiFiSMSMemoryPressure(set func(bool), persistErr error, retained vowifiSMSRecordResult) {
	if set == nil {
		return
	}
	if persistErr != nil {
		set(true)
		return
	}
	if retained.Stored || retained.Duplicate || retained.Suppressed {
		set(false)
	}
}

func isCompatVoWiFiIncomingSMSLog(msg string) bool {
	msg = strings.TrimSpace(msg)
	return strings.HasPrefix(msg, "收到新短信 / VoWiFi\n") ||
		strings.Contains(msg, "\n通道    VoWiFi\n") ||
		strings.Contains(msg, "\n通道  VoWiFi\n")
}

func (d poolVoWiFiRuntimeDispatcher) Dispatch(ctx context.Context, e eventhost.Event) {
	if d.pool == nil || e == nil {
		return
	}
	recorder := vowifiSMSHistoryRecorder{pool: d.pool}
	var recordResult vowifiSMSRecordResult
	var receivedOn SMSIdentity
	switch v := e.(type) {
	case eventhost.SMSReceived:
		// One snapshot of the receiving SIM serves observers and notifiers,
		// so a profile switch while the message is stored cannot split them.
		receivedOn = recorder.resolveReceivingIdentity(v.DevID)
		res, err := recorder.recordReceived(v, receivedOn)
		applyVoWiFiSMSMemoryPressure(func(full bool) {
			d.pool.SetVoWiFiSMSMemoryFull(v.DevID, full)
		}, err, res)
		if err != nil {
			logger.Warn("VoWiFi 上层入库入站短信失败", "device", v.DevID, "sender", v.Sender, "err", err)
		}
		recordResult = res
		if res.Stored && !v.Incomplete {
			d.pool.notifyInboundSMS(InboundSMS{
				DeviceID: v.DevID, ICCID: receivedOn.ICCID, IMSI: receivedOn.IMSI,
				Sender: v.Sender, Content: v.Content, Time: v.Time,
			})
		}
	case eventhost.SMSSent:
		if err := recorder.RecordSent(v); err != nil {
			logger.Warn("VoWiFi 上层入库出站短信失败", "device", v.DevID, "to", v.TargetURI, "err", err)
		}
	case eventhost.LocalNumberLearned:
		if err := recorder.RecordLocalNumberLearned(v); err != nil {
			logger.Warn("VoWiFi 上层持久化本机号码失败", "device", v.DevID, "imsi", v.IMSI, "phone", v.Number, "err", err)
		}
	case eventhost.MWIUpdated:
		d.pool.RecordVoWiFiMWI(v.DevID, VoWiFiMWIState{
			MessagesWaiting: v.MessagesWaiting, VoiceNew: v.VoiceNew, VoiceOld: v.VoiceOld,
			Account: v.Account, UpdatedAt: v.Time,
		})
		logger.Info("VoWiFi MWI 更新", "device", v.DevID, "waiting", v.MessagesWaiting,
			"voice_new", v.VoiceNew, "voice_old", v.VoiceOld)
	case eventhost.CallWaiting:
		d.pool.notifyCallWaiting(v)
	}

	notifier := d.pool.getNotifier()
	if notifier == nil {
		return
	}

	if sms, ok := e.(eventhost.SMSReceived); ok {
		if smsnotify.ShouldSuppressReceivedSMS(sms.Content) {
			logger.Info("VoWiFi 短信已过滤（运营商 OTA/无法解码二进制包），不进行通知推送", "device", sms.DevID, "sender", sms.Sender)
			return
		}
		if recordResult.Duplicate {
			logger.Info("VoWiFi 短信重复（通过数据库去重兜底），不进行重复通知推送", "device", sms.DevID, "sender", sms.Sender)
			return
		}
		notifySMS(notifier, InboundSMS{
			DeviceID: sms.DevID, ICCID: receivedOn.ICCID, IMSI: receivedOn.IMSI,
			Sender: sms.Sender, Content: sms.Content, Time: sms.Time,
		}, "VoWiFi")
	}

	logNotify, ok := e.(eventhost.LogNotify)
	if !ok || strings.TrimSpace(logNotify.Message) == "" || isCompatVoWiFiIncomingSMSLog(logNotify.Message) {
		return
	}
	notifier.NotifyRaw(logNotify.Message)
}

func (p *Pool) notifyCallWaiting(event eventhost.CallWaiting) {
	if p == nil {
		return
	}
	notifier := p.getNotifier()
	if notifier == nil {
		return
	}
	if incoming, ok := notifier.(IncomingCallNotifier); ok {
		incoming.NotifyIncomingCall(event.DevID, event.Caller, event.Callee)
		return
	}
	notifier.NotifyRaw(formatCallWaitingNotify(event.DevID, event.Caller, event.Callee))
}

func formatCallWaitingNotify(deviceID, caller, callee string) string {
	return "呼叫等待\n设备    " + strings.TrimSpace(deviceID) +
		"\n主叫    " + strings.TrimSpace(caller) +
		"\n被叫    " + strings.TrimSpace(callee)
}
