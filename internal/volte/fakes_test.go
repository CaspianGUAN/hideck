package volte

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

type FakeModem struct {
	mu            sync.Mutex
	ID            string
	Port          string
	IMEI          string
	IMS           int
	VoLTE         int
	USB           []string
	AutoSel       int
	MBN           []MBNEntry
	FailIMS11     bool
	Fail          map[string]error
	ListTimeouts  int
	Commands      []string
	PendingUAC    *int
	NextID        string
	NextPort      string
	UACImmediate  bool
	Audio         string
	StopIMS       int
	SetNative     int
	EnsureErr     error
	EnsureCount   int
	ReleaseCount  int
	imsRegHandler func(*qmi.IMSARegistrationStatus)
	imsSvcHandler func(*qmi.IMSAServicesStatus)
	voiceHandler  func(*qmi.VoiceAllCallInfo)
	allCalls      *qmi.VoiceAllCallInfo
	Reg           Registration
	RegErr        error
	COPS          string
	CEREG         int
	imsCID        int
	imsActive     bool
	hangupErr     error
	dialVoice     *qmi.VoiceAllCallInfo
	UACUnusable   bool
	manageCalls   []qmi.VoiceManageCallsRequest
	manageErrs    []error
	dtmfActions   []string
	burstDTMFErr  error
	startDTMFErr  error
	stopDTMFErr   error
}

func newFakeModem() *FakeModem {
	return &FakeModem{
		ID:      "wwan1",
		Port:    "/dev/ttyUSB6",
		IMEI:    "861234567890123",
		IMS:     0,
		VoLTE:   0,
		USB:     []string{"0x2C7C", "0x125", "1", "1", "1", "1", "1", "0", "0"},
		AutoSel: 1,
		MBN: []MBNEntry{
			{Index: 0, Selected: true, Activated: true, Name: "ROW_Generic_3GPP"},
			{Index: 11, Name: "Volte_OpenMkt-Commercial-CMCC"},
			{Index: 12, Name: "VoLTE_OPNMKT_CT"},
			{Index: 13, Name: "CU-VoLTE"},
		},
		UACImmediate: true,
		Audio:        "",
		Reg:          Registration{Registered: true, VoiceAvailable: true},
		COPS:         "46000",
		CEREG:        1,
		imsCID:       2,
	}
}

func (f *FakeModem) ExecuteAT(deviceID, cmd string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if deviceID != f.ID {
		return "", fmt.Errorf("device %s not present", deviceID)
	}
	f.Commands = append(f.Commands, cmd)
	if f.Fail != nil {
		if err, ok := f.Fail[cmd]; ok {
			if err != nil {
				return "ERROR", err
			}
			return "ERROR", nil
		}
	}
	switch {
	case cmd == "AT+CGSN" || cmd == "AT+GSN":
		return f.IMEI + "\r\nOK\r\n", nil
	case cmd == IMSQueryCommand():
		return fmt.Sprintf("+QCFG: \"ims\",%d,%d\r\nOK\r\n", f.IMS, f.VoLTE), nil
	case strings.HasPrefix(cmd, `AT+QCFG="ims",`):
		return f.writeIMS(cmd)
	case cmd == USBConfigQueryCommand():
		return fmt.Sprintf("+QCFG: \"usbcfg\",%s\r\nOK\r\n", strings.Join(f.USB, ",")), nil
	case strings.HasPrefix(cmd, `AT+QCFG="usbcfg"`):
		return f.writeUSB(cmd)
	case cmd == MBNAutoSelQueryCommand():
		return fmt.Sprintf("+QMBNCFG: \"AutoSel\",%d\r\nOK\r\n", f.AutoSel), nil
	case cmd == MBNListQueryCommand():
		if f.ListTimeouts > 0 {
			f.ListTimeouts--
			return "", errors.New("timeout")
		}
		return f.listMBN(), nil
	case strings.HasPrefix(cmd, `AT+QMBNCFG="autosel",`):
		bit := 0
		if strings.HasSuffix(cmd, ",1") {
			bit = 1
		}
		f.AutoSel = bit
		return "OK", nil
	case strings.HasPrefix(cmd, `AT+QMBNCFG="select",`):
		name := strings.Trim(strings.TrimPrefix(cmd, `AT+QMBNCFG="select",`), `"`)
		found := false
		for i := range f.MBN {
			f.MBN[i].Selected = f.MBN[i].Name == name
			f.MBN[i].Activated = false
			if f.MBN[i].Selected {
				found = true
			}
		}
		if !found {
			return "ERROR", nil
		}
		return "OK", nil
	case cmd == MBNActivateCommand():
		for i := range f.MBN {
			f.MBN[i].Activated = f.MBN[i].Selected
		}
		return "OK", nil
	case cmd == "AT+CFUN=1,1":
		return "OK", nil
	case cmd == COPSNumericFormatCommand():
		return "OK", nil
	case cmd == COPSQueryCommand():
		plmn := f.COPS
		if plmn == "" {
			plmn = "46000"
		}
		return fmt.Sprintf("+COPS: 0,2,\"%s\",7\r\nOK\r\n", plmn), nil
	case cmd == CEREGQueryCommand():
		return fmt.Sprintf("+CEREG: 0,%d\r\nOK\r\n", f.CEREG), nil
	case cmd == CGDCONTQueryCommand():
		return "+CGDCONT: 1,\"IP\",\"cmnet\"\r\n+CGDCONT: 2,\"IPV4V6\",\"ims\"\r\nOK\r\n", nil
	case cmd == CGACTQueryCommand():
		bit := 0
		if f.imsActive {
			bit = 1
		}
		return fmt.Sprintf("+CGACT: 1,1\r\n+CGACT: %d,%d\r\nOK\r\n", f.imsCID, bit), nil
	case strings.HasPrefix(cmd, "AT+CGACT="):
		f.imsActive = strings.Contains(cmd, ",2") || strings.HasSuffix(cmd, ",2")
		if strings.Contains(cmd, "1,2") {
			f.imsActive = true
		}
		return "OK", nil
	default:
		return "OK", nil
	}
}

func (f *FakeModem) writeIMS(cmd string) (string, error) {
	args := strings.TrimPrefix(cmd, `AT+QCFG="ims",`)
	parts := strings.Split(args, ",")
	if cmd == IMSEnableCommand() && f.FailIMS11 {
		return "ERROR", nil
	}
	if len(parts) == 0 {
		return "ERROR", nil
	}
	ims, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return "ERROR", nil
	}
	f.IMS = ims
	if len(parts) > 1 {
		volte, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return "ERROR", nil
		}
		f.VoLTE = volte
	} else {
		f.VoLTE = 0
	}
	return "OK", nil
}

func (f *FakeModem) writeUSB(cmd string) (string, error) {
	args := strings.TrimPrefix(cmd, `AT+QCFG=`)
	parts := splitQCFGArgs(strings.TrimPrefix(args, `"usbcfg",`))
	if len(parts) < 3 {
		return "ERROR", nil
	}
	if canonHexID(parts[0]) != canonHexID(f.USB[0]) || canonHexID(parts[1]) != canonHexID(f.USB[1]) {
		return "ERROR", nil
	}
	bit, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "ERROR", nil
	}
	if f.UACImmediate {
		cp := append([]string(nil), parts...)
		f.USB = cp
	} else {
		f.PendingUAC = &bit
	}
	return "OK", nil
}

func (f *FakeModem) listMBN() string {
	var b strings.Builder
	for _, e := range f.MBN {
		sel, act := 0, 0
		if e.Selected {
			sel = 1
		}
		if e.Activated {
			act = 1
		}
		fmt.Fprintf(&b, "+QMBNCFG: \"List\",%d,%d,%d,\"%s\",0x1,1\r\n", e.Index, sel, act, e.Name)
	}
	b.WriteString("OK\r\n")
	return b.String()
}

func (f *FakeModem) StopSoftwareIMS(string) error { f.StopIMS++; return nil }
func (f *FakeModem) SetNativeIMS(context.Context, string, bool) error {
	f.SetNative++
	return nil
}
func (f *FakeModem) EnsureIMSClients(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureCount++
	return f.EnsureErr
}
func (f *FakeModem) ReleaseIMSClients(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ReleaseCount++
	return nil
}
func (f *FakeModem) OnIMSRegistration(_ string, handler func(*qmi.IMSARegistrationStatus)) error {
	f.mu.Lock()
	f.imsRegHandler = handler
	f.mu.Unlock()
	return nil
}
func (f *FakeModem) OnIMSServices(_ string, handler func(*qmi.IMSAServicesStatus)) error {
	f.mu.Lock()
	f.imsSvcHandler = handler
	f.mu.Unlock()
	return nil
}
func (f *FakeModem) fireIMSRegistration(info *qmi.IMSARegistrationStatus) {
	f.mu.Lock()
	h := f.imsRegHandler
	f.mu.Unlock()
	if h != nil {
		h(info)
	}
}
func (f *FakeModem) IMSAStatus(context.Context, string) (Registration, error) {
	return f.Reg, f.RegErr
}
func (f *FakeModem) AudioDevice(string) string {
	if f.UACUnusable {
		return ""
	}
	return f.Audio
}
func (f *FakeModem) USBAudioUnusable(string) bool { return f.UACUnusable }
func (f *FakeModem) VOICEDial(context.Context, string, string) (uint8, error) {
	f.mu.Lock()
	info := f.dialVoice
	f.mu.Unlock()
	if info != nil {
		f.fireVoice(info)
	}
	return 1, nil
}
func (f *FakeModem) VOICEAnswer(context.Context, string, uint8) error { return nil }
func (f *FakeModem) VOICEHangup(context.Context, string, uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hangupErr
}
func (f *FakeModem) VOICEManageCalls(_ context.Context, _ string, req qmi.VoiceManageCallsRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manageCalls = append(f.manageCalls, req)
	if len(f.manageErrs) == 0 {
		return nil
	}
	err := f.manageErrs[0]
	f.manageErrs = f.manageErrs[1:]
	return err
}
func (f *FakeModem) VOICEBurstDTMF(_ context.Context, _ string, callID uint8, digits string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dtmfActions = append(f.dtmfActions, fmt.Sprintf("burst:%d:%s", callID, digits))
	return f.burstDTMFErr
}
func (f *FakeModem) VOICEStartContinuousDTMF(_ context.Context, _ string, callID, digit uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dtmfActions = append(f.dtmfActions, fmt.Sprintf("start:%d:%c", callID, digit))
	return f.startDTMFErr
}
func (f *FakeModem) VOICEStopContinuousDTMF(_ context.Context, _ string, callID uint8) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dtmfActions = append(f.dtmfActions, fmt.Sprintf("stop:%d", callID))
	return f.stopDTMFErr
}
func (f *FakeModem) OnVoiceStatus(_ string, handler func(*qmi.VoiceAllCallInfo)) error {
	f.mu.Lock()
	f.voiceHandler = handler
	f.mu.Unlock()
	return nil
}
func (f *FakeModem) VOICEGetAllCallInfo(context.Context, string) (*qmi.VoiceAllCallInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allCalls == nil {
		return &qmi.VoiceAllCallInfo{}, nil
	}
	return f.allCalls, nil
}
func (f *FakeModem) fireVoice(info *qmi.VoiceAllCallInfo) {
	f.mu.Lock()
	h := f.voiceHandler
	f.mu.Unlock()
	if h != nil {
		h(info)
	}
}

type reenumWaiter struct {
	modem   *FakeModem
	timeout bool
}

func (w *reenumWaiter) WaitByIMEI(_ context.Context, imei string, _ time.Duration) (string, error) {
	if w.timeout {
		return "", context.DeadlineExceeded
	}
	w.modem.mu.Lock()
	defer w.modem.mu.Unlock()
	if imei != w.modem.IMEI {
		return "", fmt.Errorf("imei mismatch")
	}
	if w.modem.PendingUAC != nil {
		w.modem.USB[len(w.modem.USB)-1] = strconv.Itoa(*w.modem.PendingUAC)
		w.modem.PendingUAC = nil
	}
	if w.modem.NextID != "" {
		w.modem.ID = w.modem.NextID
		w.modem.NextID = ""
	}
	if w.modem.NextPort != "" {
		w.modem.Port = w.modem.NextPort
		w.modem.NextPort = ""
	}
	return w.modem.ID, nil
}
