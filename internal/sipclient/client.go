package sipclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	defaultLocalPort      = 5070
	defaultExpires        = 300
	defaultRingTimeout    = 60 * time.Second
	minRegisterRetry      = 5 * time.Second
	maxRegisterRetry      = 60 * time.Second
	registerRequestTimout = 15 * time.Second
	userAgentName         = "HiDeck"
)

const (
	// ModeRegister registers as an extension; the PBX reaches us over the
	// registration flow, which also works across NAT.
	ModeRegister = "register"
	// ModeTrunk is an IP-authenticated trunk with no registration, for a
	// PBX on the same host or LAN that can reach our listen port directly.
	ModeTrunk = "trunk"
)

// Settings is the normalized form of config.SIPClientConfig.
type Settings struct {
	Mode         string
	ServerHost   string
	ServerPort   int
	Transport    string
	Username     string
	AuthUsername string
	Password     string
	LocalPort    int
	Expires      time.Duration
	DeviceID     string
	InboundTo    string
	RingTimeout  time.Duration
}

// SettingsFromConfig validates and fills defaults.
func SettingsFromConfig(cfg config.SIPClientConfig) (Settings, error) {
	host, port, err := splitServer(cfg.Server)
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{
		Mode:       strings.ToLower(strings.TrimSpace(cfg.Mode)),
		ServerHost: host, ServerPort: port,
		Transport:    strings.ToLower(strings.TrimSpace(cfg.Transport)),
		Username:     strings.TrimSpace(cfg.Username),
		AuthUsername: strings.TrimSpace(cfg.AuthUsername),
		Password:     cfg.Password,
		LocalPort:    cfg.LocalPort,
		Expires:      time.Duration(cfg.Expires) * time.Second,
		DeviceID:     strings.TrimSpace(cfg.DeviceID),
		InboundTo:    strings.TrimSpace(cfg.InboundTo),
		RingTimeout:  time.Duration(cfg.RingTimeout) * time.Second,
	}
	if settings.Mode == "" {
		settings.Mode = ModeRegister
	}
	if settings.Mode != ModeRegister && settings.Mode != ModeTrunk {
		return Settings{}, fmt.Errorf("sipclient: unsupported mode %q", cfg.Mode)
	}
	if settings.Transport == "" {
		settings.Transport = "tcp"
	}
	if settings.Transport != "tcp" && settings.Transport != "udp" {
		return Settings{}, fmt.Errorf("sipclient: unsupported transport %q", cfg.Transport)
	}
	if settings.Username == "" && settings.Mode == ModeRegister {
		return Settings{}, errors.New("sipclient: username is required")
	}
	if settings.Username == "" {
		settings.Username = "hideck"
	}
	if settings.AuthUsername == "" {
		settings.AuthUsername = settings.Username
	}
	if settings.LocalPort <= 0 {
		settings.LocalPort = defaultLocalPort
	}
	if settings.Expires <= 0 {
		settings.Expires = defaultExpires * time.Second
	}
	if settings.RingTimeout <= 0 {
		settings.RingTimeout = defaultRingTimeout
	}
	return settings, nil
}

// owner identifies this line to the phone service; lines bound to different
// cards stay distinct even when they share a username.
func (s Settings) owner() string {
	if s.DeviceID == "" {
		return "sip:" + s.Username
	}
	return "sip:" + s.Username + "@" + s.DeviceID
}

// CheckLines rejects line sets that would fight over a card or a port: with
// more than one line, each must be bound to its own card and listen on its
// own local port.
func CheckLines(lines []Settings) error {
	if len(lines) < 2 {
		return nil
	}
	devices := make(map[string]bool, len(lines))
	ports := make(map[int]bool, len(lines))
	for index, line := range lines {
		if line.DeviceID == "" {
			return fmt.Errorf("sipclient: line %d needs device_id when several SIP lines are configured", index+1)
		}
		if devices[line.DeviceID] {
			return fmt.Errorf("sipclient: device_id %q is bound to more than one SIP line", line.DeviceID)
		}
		if ports[line.LocalPort] {
			return fmt.Errorf("sipclient: local_port %d is used by more than one SIP line", line.LocalPort)
		}
		devices[line.DeviceID], ports[line.LocalPort] = true, true
	}
	return nil
}

func splitServer(value string) (string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0, errors.New("sipclient: server is required")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return value, 5060, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("sipclient: invalid server port in %q", value)
	}
	return host, port, nil
}

// Client registers to the PBX as one extension and bridges calls between the
// PBX and the modem through the phone service.
type Client struct {
	settings Settings
	phone    Phone
	devices  Devices
	owner    string
	localIP  string

	ua      *sipgo.UserAgent
	client  *sipgo.Client
	server  *sipgo.Server
	contact sip.ContactHeader
	dialogs *sipgo.DialogClientCache
	serving *sipgo.DialogServerCache

	pbxIPs []net.IP

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	listener io.Closer

	mu         sync.Mutex
	registered bool
	byCall     map[string]*bridge
	byMedia    map[string]*bridge
	bySIPCall  map[string]*bridge
}

// New prepares the SIP user agent; Start begins registration and listening.
func New(settings Settings, phoneService Phone, devices Devices) (*Client, error) {
	localIP, err := routeLocalIP(settings.ServerHost, settings.ServerPort)
	if err != nil {
		return nil, err
	}
	pbxIPs, err := net.LookupIP(settings.ServerHost)
	if err != nil || len(pbxIPs) == 0 {
		return nil, fmt.Errorf("sipclient: resolve PBX host %q: %w", settings.ServerHost, err)
	}
	ua, err := sipgo.NewUA(sipgo.WithUserAgent(userAgentName), sipgo.WithUserAgentHostname(localIP))
	if err != nil {
		return nil, fmt.Errorf("sipclient: create user agent: %w", err)
	}
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(localIP))
	if err != nil {
		_ = ua.Close()
		return nil, fmt.Errorf("sipclient: create client: %w", err)
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		_ = ua.Close()
		return nil, fmt.Errorf("sipclient: create server: %w", err)
	}
	contactURI := sip.Uri{Scheme: "sip", User: settings.Username, Host: localIP, Port: settings.LocalPort}
	contactURI.UriParams = sip.NewParams()
	contactURI.UriParams.Add("transport", settings.Transport)
	contact := sip.ContactHeader{Address: contactURI}
	c := &Client{
		settings: settings, phone: phoneService, devices: devices,
		owner: settings.owner(), localIP: localIP, pbxIPs: pbxIPs,
		ua: ua, client: client, server: server, contact: contact,
		dialogs: sipgo.NewDialogClientCache(client, contact),
		serving: sipgo.NewDialogServerCache(client, contact),
		byCall:  make(map[string]*bridge), byMedia: make(map[string]*bridge),
		bySIPCall: make(map[string]*bridge),
	}
	server.OnInvite(c.onInvite)
	server.OnAck(c.onAck)
	server.OnBye(c.onBye)
	server.OnOptions(c.onOptions)
	return c, nil
}

// Start listens for PBX requests, registers, and follows phone events.
func (c *Client) Start() {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.listen()
	c.wg.Add(1)
	if c.settings.Mode == ModeTrunk {
		c.setRegistered(true)
	} else {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.registerLoop()
		}()
	}
	go func() {
		defer c.wg.Done()
		c.followPhoneEvents()
	}()
	logger.Info("SIP 客户端已启动", "mode", c.settings.Mode, "server", c.serverAddr(), "transport", c.settings.Transport,
		"username", c.settings.Username, "local_ip", c.localIP, "local_port", c.settings.LocalPort,
		"device_id", c.settings.DeviceID)
}

// Stop unregisters and closes the user agent.
func (c *Client) Stop() {
	if c.cancel == nil {
		return
	}
	if c.settings.Mode == ModeRegister {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if _, err := c.register(ctx, 0); err != nil {
			logger.Warn("SIP 注销失败", "err", err)
		}
		cancel()
	}
	c.cancel()
	if c.listener != nil {
		_ = c.listener.Close()
	}
	_ = c.ua.Close()
	c.wg.Wait()
}

// listen accepts PBX-initiated connections on the local port. Behind NAT the
// PBX reaches us over the registration connection instead, so a failure here
// is not fatal.
func (c *Client) listen() {
	address := net.JoinHostPort("0.0.0.0", strconv.Itoa(c.settings.LocalPort))
	var err error
	if c.settings.Transport == "udp" {
		var conn net.PacketConn
		if conn, err = net.ListenPacket("udp4", address); err == nil {
			c.listener = conn
			go func() { _ = c.server.ServeUDP(conn) }()
		}
	} else {
		var listener net.Listener
		if listener, err = net.Listen("tcp4", address); err == nil {
			c.listener = listener
			go func() { _ = c.server.ServeTCP(listener) }()
		}
	}
	if err != nil {
		logger.Warn("SIP 客户端监听失败，仅依赖注册连接接收来电", "listen", address, "err", err)
	}
}

func (c *Client) serverAddr() string {
	return net.JoinHostPort(c.settings.ServerHost, strconv.Itoa(c.settings.ServerPort))
}

func (c *Client) serverURI(user string) sip.Uri {
	uri := sip.Uri{Scheme: "sip", User: user, Host: c.settings.ServerHost, Port: c.settings.ServerPort}
	uri.UriParams = sip.NewParams()
	uri.UriParams.Add("transport", c.settings.Transport)
	return uri
}

func (c *Client) aor() sip.Uri {
	return sip.Uri{Scheme: "sip", User: c.settings.Username, Host: c.settings.ServerHost}
}

func (c *Client) registerLoop() {
	retry := minRegisterRetry
	for {
		ctx, cancel := context.WithTimeout(c.ctx, registerRequestTimout)
		granted, err := c.register(ctx, c.settings.Expires)
		cancel()
		wait := retry
		if err != nil {
			c.setRegistered(false)
			logger.Warn("SIP 注册失败", "server", c.serverAddr(), "err", err, "retry_in", retry)
			retry = min(retry*2, maxRegisterRetry)
		} else {
			if !c.setRegistered(true) {
				logger.Info("SIP 注册成功", "server", c.serverAddr(), "username", c.settings.Username, "expires", granted)
			}
			retry = minRegisterRetry
			wait = refreshAfter(granted)
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func refreshAfter(granted time.Duration) time.Duration {
	refresh := granted * 4 / 5
	if granted-refresh > 30*time.Second {
		refresh = granted - 30*time.Second
	}
	return max(refresh, minRegisterRetry)
}

// setRegistered stores the state and reports whether it was already set.
func (c *Client) setRegistered(value bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.registered
	c.registered = value
	return previous == value
}

func (c *Client) isRegistered() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registered
}

func (c *Client) register(ctx context.Context, expires time.Duration) (time.Duration, error) {
	request := sip.NewRequest(sip.REGISTER, c.serverURI(""))
	request.SetTransport(strings.ToUpper(c.settings.Transport))
	from := &sip.FromHeader{Address: c.aor(), Params: sip.NewParams()}
	from.Params.Add("tag", sip.GenerateTagN(16))
	request.AppendHeader(from)
	request.AppendHeader(&sip.ToHeader{Address: c.aor()})
	contact := c.contact.Clone()
	request.AppendHeader(contact)
	request.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(int(expires/time.Second))))
	request.AppendHeader(sip.NewHeader("User-Agent", userAgentName))
	response, err := c.client.Do(ctx, request, sipgo.ClientRequestRegisterBuild)
	if err != nil {
		return 0, err
	}
	if response.StatusCode == sip.StatusUnauthorized || response.StatusCode == sip.StatusProxyAuthRequired {
		response, err = c.client.DoDigestAuth(ctx, request, response, sipgo.DigestAuth{
			Username: c.settings.AuthUsername, Password: c.settings.Password,
		})
		if err != nil {
			return 0, err
		}
	}
	if response.StatusCode != sip.StatusOK {
		return 0, fmt.Errorf("registrar answered %d %s", response.StatusCode, response.Reason)
	}
	return grantedExpires(response, expires), nil
}

func grantedExpires(response *sip.Response, requested time.Duration) time.Duration {
	if header := response.GetHeader("Expires"); header != nil {
		if seconds, err := strconv.Atoi(strings.TrimSpace(header.Value())); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	for _, header := range response.GetHeaders("Contact") {
		if index := strings.Index(strings.ToLower(header.Value()), "expires="); index >= 0 {
			text := header.Value()[index+len("expires="):]
			if end := strings.IndexAny(text, ";,> "); end >= 0 {
				text = text[:end]
			}
			if seconds, err := strconv.Atoi(text); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
		}
	}
	return requested
}

func (c *Client) onOptions(request *sip.Request, tx sip.ServerTransaction) {
	_ = tx.Respond(sip.NewResponseFromRequest(request, sip.StatusOK, "OK", nil))
}

func (c *Client) onAck(request *sip.Request, tx sip.ServerTransaction) {
	_ = c.serving.ReadAck(request, tx)
}

func (c *Client) onBye(request *sip.Request, tx sip.ServerTransaction) {
	err := c.serving.ReadBye(request, tx)
	if errors.Is(err, sipgo.ErrDialogDoesNotExists) || errors.Is(err, sipgo.ErrDialogOutsideDialog) {
		err = c.dialogs.ReadBye(request, tx)
	}
	if err != nil {
		_ = tx.Respond(sip.NewResponseFromRequest(request, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist", nil))
	}
}

// routeLocalIP finds the local address the kernel would use to reach the PBX.
func routeLocalIP(host string, port int) (string, error) {
	conn, err := net.Dial("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", fmt.Errorf("sipclient: resolve local address towards %s: %w", host, err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

// fromPBX reports whether a request came from the configured PBX. Calls are
// placed on the modem only for the PBX, never for other hosts that can reach
// the listen port.
func (c *Client) fromPBX(request *sip.Request) bool {
	host, _, err := net.SplitHostPort(request.Source())
	if err != nil {
		host = request.Source()
	}
	source := net.ParseIP(strings.Trim(host, "[]"))
	if source == nil {
		return false
	}
	for _, ip := range c.pbxIPs {
		if ip.Equal(source) || (ip.IsLoopback() && source.IsLoopback()) {
			return true
		}
	}
	return false
}
