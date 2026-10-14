package upstreamproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	ProbeStageTCPConnect   = "tcp_connect"
	ProbeStageHandshake    = "socks5_handshake"
	ProbeStageUDPAssociate = "udp_associate"
	ProbeStageUDPRelay     = "udp_relay"
	ProbeStageOK           = "ok"
)

type ProbeConfig struct {
	ProxyAddr string
	Username  string
	Password  string
	Timeout   time.Duration
	// UDPProbeTargets overrides the fixed public DNS endpoints used for the data-plane check.
	UDPProbeTargets []string
}

type ProbeResult struct {
	CheckedAt      time.Time    `json:"checked_at"`
	Egress         *EgressProbe `json:"egress,omitempty"`
	ProxyAddr      string       `json:"proxy_addr"`
	Stage          string       `json:"stage"`
	Reachable      bool         `json:"reachable"`
	HandshakeOK    bool         `json:"handshake_ok"`
	UDPAssociateOK bool         `json:"udp_associate_ok"`
	UDPRelayOK     bool         `json:"udp_relay_ok"`
	UDPProbeTarget string       `json:"udp_probe_target,omitempty"`
	AuthMethod     string       `json:"auth_method,omitempty"`
	RelayAddr      string       `json:"relay_addr,omitempty"`
	DurationMS     int64        `json:"duration_ms"`
	Diagnosis      string       `json:"diagnosis,omitempty"`
	Hint           string       `json:"hint,omitempty"`
	Error          string       `json:"error,omitempty"`
}

func (r ProbeResult) OK() bool {
	return r.UDPAssociationOK() && r.UDPRelayOK
}

func (r ProbeResult) UDPAssociationOK() bool {
	return r.Reachable && r.HandshakeOK && r.UDPAssociateOK
}

func (r ProbeResult) FailureSummary() string {
	if r.OK() {
		return "前置代理探测通过"
	}

	parts := make([]string, 0, 3)
	if strings.TrimSpace(r.Diagnosis) != "" {
		parts = append(parts, r.Diagnosis)
	}
	if strings.TrimSpace(r.Hint) != "" {
		parts = append(parts, "建议: "+r.Hint)
	}
	if strings.TrimSpace(r.Error) != "" {
		parts = append(parts, "细节: "+r.Error)
	}
	return strings.Join(parts, "；")
}

func ProbeSOCKS5(ctx context.Context, cfg ProbeConfig) (ProbeResult, error) {
	startedAt := time.Now()
	result := ProbeResult{
		ProxyAddr: strings.TrimSpace(cfg.ProxyAddr),
		Stage:     ProbeStageTCPConnect,
	}
	if result.ProxyAddr == "" {
		result.Error = "empty proxy addr"
		return finalizeProbeResult(result, startedAt), errors.New(result.Error)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := probeDeadline(ctx, timeout)
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	dialer := &net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(probeCtx, "tcp", result.ProxyAddr)
	if err != nil {
		result.Error = fmt.Sprintf("代理 TCP 连接失败: %v", err)
		return finalizeProbeResult(result, startedAt), err
	}
	defer conn.Close()

	result.Reachable = true
	if err := conn.SetDeadline(deadline); err != nil {
		result.Error = fmt.Sprintf("设置探测超时失败: %v", err)
		return finalizeProbeResult(result, startedAt), err
	}

	selectedMethod, err := probeHandshake(conn, cfg.Username, cfg.Password)
	if err != nil {
		result.Stage = ProbeStageHandshake
		result.Error = err.Error()
		return finalizeProbeResult(result, startedAt), err
	}
	result.Stage = ProbeStageUDPAssociate
	result.HandshakeOK = true
	result.AuthMethod = socks5AuthMethodName(selectedMethod)

	udpConn, err := openProbeUDPClient(conn)
	if err != nil {
		result.Error = err.Error()
		return finalizeProbeResult(result, startedAt), err
	}
	defer udpConn.Close()

	relayAddr, err := probeUDPAssociate(probeCtx, conn, udpConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		result.Error = err.Error()
		return finalizeProbeResult(result, startedAt), err
	}
	result.UDPAssociateOK = true
	result.RelayAddr = relayAddr.String()
	result.Stage = ProbeStageUDPRelay

	target, err := probeUDPRelay(probeCtx, udpConn, relayAddr, cfg.UDPProbeTargets, deadline)
	if err != nil {
		result.Error = err.Error()
		return finalizeProbeResult(result, startedAt), err
	}

	result.Stage = ProbeStageOK
	result.UDPRelayOK = true
	result.UDPProbeTarget = target
	return finalizeProbeResult(result, startedAt), nil
}

func finalizeProbeResult(result ProbeResult, startedAt time.Time) ProbeResult {
	result.CheckedAt = time.Now()
	result.DurationMS = time.Since(startedAt).Milliseconds()
	annotateProbeResult(&result)
	return result
}

func annotateProbeResult(result *ProbeResult) {
	if result == nil {
		return
	}
	if result.OK() {
		result.Diagnosis = "代理公共 DNS UDP 数据往返正常"
		return
	}

	errText := strings.ToLower(strings.TrimSpace(result.Error))
	switch result.Stage {
	case ProbeStageTCPConnect:
		result.Diagnosis = "无法与代理建立 TCP 连接"
		switch {
		case strings.Contains(errText, "connection refused"):
			result.Hint = "代理地址可达，但目标端口没有监听 SOCKS5 服务"
		case strings.Contains(errText, "i/o timeout"):
			result.Hint = "检查代理地址、端口、防火墙或 ACL，确认当前主机可以访问该端口"
		default:
			result.Hint = "检查代理地址、端口和网络连通性"
		}
	case ProbeStageHandshake:
		switch {
		case strings.Contains(errText, "0xff"):
			result.Diagnosis = "代理拒绝了当前提供的认证方式"
			result.Hint = "检查代理是否要求用户名密码，或当前凭据是否与服务端配置一致"
		case strings.Contains(errText, "用户名密码鉴权但未提供凭据"):
			result.Diagnosis = "代理要求用户名密码认证"
			result.Hint = "为该前置代理填写正确的用户名和密码"
		case strings.Contains(errText, "鉴权失败"):
			result.Diagnosis = "代理用户名或密码错误"
			result.Hint = "检查保存的用户名密码是否正确"
		case strings.Contains(errText, "版本不匹配"):
			result.Diagnosis = "目标端口不是标准 SOCKS5 服务"
			result.Hint = "确认这里填写的是 SOCKS5 端口，而不是 HTTP、混合端口或其他协议端口"
		case strings.Contains(errText, "响应读取失败: eof"):
			result.Diagnosis = "代理在 SOCKS5 握手阶段直接断开了连接"
			result.Hint = "通常表示该端口并非标准 SOCKS5，或服务端策略直接拒绝当前来源连接"
		default:
			result.Diagnosis = "SOCKS5 握手失败"
			result.Hint = "检查代理协议类型、认证方式和服务端访问策略"
		}
	case ProbeStageUDPAssociate:
		switch {
		case strings.Contains(errText, "被拒绝: 状态码"):
			result.Diagnosis = "代理明确拒绝了 UDP Associate"
			result.Hint = "通常表示代理未开启 UDP 转发，或当前策略禁止 UDP relay"
		case strings.Contains(errText, "响应解析失败: 读取响应头失败: eof"):
			result.Diagnosis = "代理在 UDP Associate 阶段直接断开了连接"
			result.Hint = "通常表示该 SOCKS5 只支持 TCP，不支持 UDP Associate，或 UDP relay 未开启"
		case strings.Contains(errText, "版本不匹配"):
			result.Diagnosis = "代理返回了非标准的 UDP Associate 响应"
			result.Hint = "确认目标端口是标准 SOCKS5 UDP Associate 端口，而不是其他混合协议端口"
		default:
			result.Diagnosis = "UDP Associate 失败"
			result.Hint = "检查代理是否支持 SOCKS5 UDP Associate，以及是否允许当前来源使用 UDP relay"
		}
	case ProbeStageUDPRelay:
		result.Diagnosis = "代理接受 UDP Associate，但公共 DNS UDP 数据无法完成往返"
		result.Hint = "检查代理服务端的 UDP 监听、防火墙、安全组和 UDP 转发链路"
	default:
		result.Diagnosis = "前置代理探测失败"
		result.Hint = "请检查代理协议、认证和 UDP 转发能力"
	}
}

func probeDeadline(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}
