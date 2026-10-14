package upstreamproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	egressTraceURL = "https://www.cloudflare.com/cdn-cgi/trace"
	maxTraceBytes  = 16 * 1024 // Bound the untrusted diagnostic response body.
)

type EgressProbe struct {
	Source      string    `json:"source"`
	CheckedAt   time.Time `json:"checked_at"`
	Reachable   bool      `json:"reachable"`
	IP          string    `json:"ip,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// ProbeDiagnostics adds an independent HTTPS egress observation. It must not
// change the DNS/UDP result used by the existing ePDG recovery selection.
func ProbeDiagnostics(ctx context.Context, cfg ProbeConfig) (ProbeResult, error) {
	egressDone := make(chan EgressProbe, 1)
	go func() { egressDone <- probeSOCKS5Egress(ctx, cfg) }()
	result, err := ProbeSOCKS5(ctx, cfg)
	egress := <-egressDone
	result.Egress = &egress
	return result, err
}

// The client is constructed at this network boundary. No environment proxy or
// direct fallback is allowed, including when SOCKS5 authentication fails.
func probeSOCKS5Egress(ctx context.Context, cfg ProbeConfig) EgressProbe {
	transport, err := egressTransport(cfg)
	if err != nil {
		return failedEgress(err)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result := probeEgress(ctx, client)
	// Transport errors may contain proxy userinfo. Never publish credentials.
	secrets := []string{cfg.Password, cfg.Username, url.PathEscape(cfg.Password), url.PathEscape(cfg.Username)}
	if cfg.Username != "" || cfg.Password != "" {
		secrets = append([]string{url.UserPassword(cfg.Username, cfg.Password).String()}, secrets...)
	}
	for _, secret := range secrets {
		if secret != "" {
			result.Error = strings.ReplaceAll(result.Error, secret, "[redacted]")
		}
	}
	return result
}

func egressTransport(cfg ProbeConfig) (*http.Transport, error) {
	if _, _, err := net.SplitHostPort(strings.TrimSpace(cfg.ProxyAddr)); err != nil {
		return nil, errors.New("SOCKS5 地址不是有效的 host:port")
	}
	proxyURL := &url.URL{Scheme: "socks5", Host: strings.TrimSpace(cfg.ProxyAddr)}
	if cfg.Username != "" || cfg.Password != "" {
		proxyURL.User = url.UserPassword(cfg.Username, cfg.Password)
	}
	return &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}, nil
}

func probeEgress(ctx context.Context, client httpDoer) EgressProbe {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, egressTraceURL, nil)
	if err != nil {
		return failedEgress(err)
	}
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return failedEgress(fmt.Errorf("经代理访问 HTTPS 出口检测服务失败: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return failedEgress(fmt.Errorf("出口检测服务返回 HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTraceBytes+1))
	if err != nil {
		return failedEgress(fmt.Errorf("读取出口检测结果失败: %w", err))
	}
	if len(body) > maxTraceBytes {
		return failedEgress(errors.New("出口检测响应过大"))
	}
	return parseEgressTrace(string(body))
}

func failedEgress(err error) EgressProbe {
	return EgressProbe{Source: "Cloudflare HTTPS", CheckedAt: time.Now(), Error: err.Error()}
}

func parseEgressTrace(body string) EgressProbe {
	fields := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			if _, duplicate := fields[key]; duplicate && (key == "ip" || key == "loc") {
				return failedEgress(errors.New("出口检测响应包含重复字段"))
			}
			fields[key] = value
		}
	}
	ip, err := netip.ParseAddr(fields["ip"])
	if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return failedEgress(errors.New("出口检测响应没有有效公网 IP"))
	}
	result := EgressProbe{Source: "Cloudflare HTTPS", CheckedAt: time.Now(), Reachable: true, IP: ip.Unmap().String()}
	code := NormalizeCountryCode(fields["loc"])
	if len(code) != 2 || code == "XX" || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		result.Error = "检测服务未返回可识别的出口国家"
		return result
	}
	result.CountryCode = code
	return result
}
