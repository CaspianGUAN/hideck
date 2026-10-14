package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type probeAPI struct {
	client              *http.Client
	base, device, token string
}

func newAPI(o options, client *http.Client) (*probeAPI, error) {
	u, err := url.Parse(o.server)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("probe API must be explicit local http://127.0.0.1:port; run through SSH")
	}
	if !validateDeviceID(o.device) {
		return nil, errors.New("invalid device ID")
	}
	token := os.Getenv("HIDECK_PROBE_TOKEN")
	if token == "" {
		return nil, errors.New("HIDECK_PROBE_TOKEN is required")
	}
	return &probeAPI{client: client, base: o.server, device: o.device, token: token}, nil
}

func (a *probeAPI) request(ctx context.Context, path string, body any) ([]byte, error) {
	var payload []byte
	method := http.MethodGet
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
		method = http.MethodPost
	}
	r, err := http.NewRequestWithContext(ctx, method, a.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(r)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &failure)
		return nil, fmt.Errorf("API HTTP %d: %s", response.StatusCode, failure.Message)
	}
	return raw, nil
}

func (a *probeAPI) ExecuteATContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	raw, err := a.request(ctx, "/api/devices/"+a.device+"/actions/at", map[string]any{"cmd": command, "timeout_ms": timeout.Milliseconds()})
	if err != nil {
		return "", err
	}
	var response struct{ Status, Response string }
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	if response.Status != "ok" {
		return "", errors.New("AT request did not return success")
	}
	fmt.Printf("%s AT command=%q response=%q\n", time.Now().Format(time.RFC3339), command, response.Response)
	lines := strings.FieldsFunc(response.Response, func(r rune) bool { return r == '\r' || r == '\n' })
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) != "OK" {
		return "", errors.New("AT response has no final OK; inspect response above")
	}
	return response.Response, nil
}

func (a *probeAPI) checkDevice(ctx context.Context, o options) error {
	raw, err := a.request(ctx, "/api/devices/"+a.device+"/overview", nil)
	if err != nil {
		return err
	}
	var response struct {
		Devices []struct {
			ID      string `json:"id"`
			USBPath string `json:"usb_path"`
			Modem   struct {
				ICCID string `json:"iccid"`
			} `json:"modem"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	for _, d := range response.Devices {
		if d.ID == o.device && d.Modem.ICCID == o.iccid && d.USBPath == o.usb {
			return nil
		}
	}
	return errors.New("device/SIM/USB identity does not match; no call placed")
}
