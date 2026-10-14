package netprobe

import (
	"context"
	"fmt"
	"net/http"
)

// Check verifies an explicitly selected endpoint using this prober's bound
// interface. Unlike public-IP discovery it preserves the actual failure.
func (p *Prober) Check(ctx context.Context, target string) error {
	if p.cfg.Interface == "" {
		return fmt.Errorf("data probe requires an explicit interface")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("data probe HTTP status %d", response.StatusCode)
	}
	return nil
}
