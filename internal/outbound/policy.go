// Package outbound implements host-side, per-SIM abuse prevention. It does not
// govern incoming traffic or IMS protocol maintenance.
package outbound

import (
	"fmt"
	"time"

	"github.com/yibaiba/hideck/pkg/simidentity"
)

type Kind string

const (
	Call Kind = "call"
	SMS  Kind = "sms"
)

// Zero values inherit defaults; disabling requires the explicit Disabled flag.
type Limit struct {
	Disabled        *bool `mapstructure:"disabled"`
	IntervalSeconds int   `mapstructure:"interval_seconds"`
	PerHour         int   `mapstructure:"per_hour"`
	PerDay          int   `mapstructure:"per_day"`
}

type Policy struct {
	Calls Limit `mapstructure:"calls"`
	SMS   Limit `mapstructure:"sms"`
}

type Config struct {
	Policy `mapstructure:",squash"`
	Cards  map[string]Policy `mapstructure:"cards"`
}

func CanonicalICCID(value string) string {
	return simidentity.CanonicalICCID(value)
}

func (c Config) normalized() (Config, error) {
	base, err := normalizePolicy(c.Policy, Policy{
		Calls: Limit{IntervalSeconds: 10, PerHour: 20, PerDay: 100},
		SMS:   Limit{IntervalSeconds: 5, PerHour: 30, PerDay: 100},
	})
	if err != nil {
		return Config{}, err
	}
	result := Config{Policy: base, Cards: make(map[string]Policy, len(c.Cards))}
	for id, policy := range c.Cards {
		key := CanonicalICCID(id)
		if key == "" {
			return Config{}, fmt.Errorf("outbound limits: empty SIM identity")
		}
		if _, exists := result.Cards[key]; exists {
			return Config{}, fmt.Errorf("outbound limits: duplicate SIM override")
		}
		value, err := normalizePolicy(policy, base)
		if err != nil {
			return Config{}, err
		}
		result.Cards[key] = value
	}
	return result, nil
}

func normalizePolicy(value, defaults Policy) (Policy, error) {
	calls, err := normalizeLimit(value.Calls, defaults.Calls)
	if err != nil {
		return Policy{}, err
	}
	sms, err := normalizeLimit(value.SMS, defaults.SMS)
	return Policy{Calls: calls, SMS: sms}, err
}

func normalizeLimit(value, defaults Limit) (Limit, error) {
	const maxIntervalSeconds = int64((1<<63 - 1) / time.Second)
	if int64(value.IntervalSeconds) > maxIntervalSeconds {
		return Limit{}, fmt.Errorf("outbound limits: interval_seconds overflows time.Duration")
	}
	if value.IntervalSeconds < 0 || value.PerHour < 0 || value.PerDay < 0 {
		return Limit{}, fmt.Errorf("outbound limits: values must not be negative; use disabled to opt out")
	}
	if value.IntervalSeconds == 0 {
		value.IntervalSeconds = defaults.IntervalSeconds
	}
	if value.PerHour == 0 {
		value.PerHour = defaults.PerHour
	}
	if value.PerDay == 0 {
		value.PerDay = defaults.PerDay
	}
	if value.Disabled == nil {
		value.Disabled = defaults.Disabled
	}
	if value.Disabled != nil {
		disabled := *value.Disabled
		value.Disabled = &disabled
	}
	return value, nil
}
