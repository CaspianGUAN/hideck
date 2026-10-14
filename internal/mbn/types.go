package mbn

import (
	"context"
	"errors"
	"time"
)

var (
	ErrConflict       = errors.New("MBN 操作冲突")
	ErrOwnerChanged   = errors.New("MBN 操作所属模组或 SIM 已改变")
	ErrNotFound       = errors.New("MBN 记录不存在")
	ErrNeedsAttention = errors.New("MBN 操作结果待确认")
)

const (
	DefaultRecovery     = 180 * time.Second
	DefaultRegistration = 120 * time.Second
	DefaultStability    = 30 * time.Second
)

type Owner struct {
	DeviceID           string `json:"device_id"`
	IMEI               string `json:"imei"`
	ICCID              string `json:"iccid"`
	Model              string `json:"model"`
	Firmware           string `json:"firmware"`
	Generation         uint64 `json:"generation"`
	IdentityGeneration uint64 `json:"identity_generation"`
}

type Snapshot struct {
	Owner        Owner        `json:"owner"`
	Capabilities Capabilities `json:"capabilities"`
	AutoSel      bool         `json:"autosel"`
	Profiles     []Profile    `json:"profiles"`
}

func (s Snapshot) Active() string {
	for _, p := range s.Profiles {
		if p.Activated {
			return p.Name
		}
	}
	return ""
}

type Asset struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	SHA256    string    `gorm:"uniqueIndex" json:"sha256"`
	Size      int64     `json:"size"`
	Model     string    `json:"model"`
	Firmware  string    `json:"firmware"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

func (Asset) TableName() string { return "mbn_assets" }

type Policy struct {
	SessionGeneration         uint64    `gorm:"-" json:"-"`
	SessionIdentityGeneration uint64    `gorm:"-" json:"-"`
	Key                       string    `gorm:"primaryKey" json:"key"`
	Mode                      string    `json:"mode"` // inherit, autosel, fixed
	Model                     string    `json:"model"`
	Firmware                  string    `json:"firmware"`
	Profile                   string    `json:"profile"`
	Version                   string    `json:"version"`
	AssetID                   string    `json:"asset_id"`
	AllowImport               bool      `json:"allow_import"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

func (Policy) TableName() string { return "mbn_policies" }

type Request struct {
	ExpectedOwner              *Owner   `json:"expected_owner,omitempty"`
	RequestID                  string   `json:"request_id"`
	Kind                       string   `json:"kind"`
	Profile                    string   `json:"profile"`
	Version                    string   `json:"version"`
	AutoSel                    bool     `json:"autosel"`
	AssetID                    string   `json:"asset_id"`
	ConfirmName                string   `json:"confirm_name"`
	AcceptNoBackup             bool     `json:"accept_no_backup"`
	AcceptUnknownCompatibility bool     `json:"accept_unknown_compatibility"`
	Replacement                string   `json:"replacement"`
	Candidates                 []string `json:"candidates"`
	Goal                       string   `json:"goal"`
	ProbeURL                   string   `json:"probe_url"`
	AllowData                  bool     `json:"allow_data"`
	AllowNativeIMS             bool     `json:"allow_native_ims"`
	TargetPLMN                 string   `json:"target_plmn"`
	RecoverySeconds            int      `json:"recovery_seconds"`
	RegistrationSeconds        int      `json:"registration_seconds"`
	StabilitySeconds           int      `json:"stability_seconds"`
	RestoreOperation           string   `json:"restore_operation"`
}

type TrialResult struct {
	Profile   string    `json:"profile"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Passed    bool      `json:"passed"`
	Error     string    `json:"error,omitempty"`
	PLMN      string    `json:"plmn"`
}

type Operation struct {
	RecoveryOf      []string      `gorm:"serializer:json" json:"recovery_of,omitempty"`
	ExpectedOwner   Owner         `gorm:"serializer:json" json:"expected_owner"`
	ID              string        `gorm:"primaryKey" json:"id"`
	DeviceID        string        `gorm:"index;uniqueIndex:mbn_request" json:"device_id"`
	RequestID       string        `gorm:"uniqueIndex:mbn_request" json:"request_id"`
	Request         Request       `gorm:"serializer:json" json:"request"`
	Original        Snapshot      `gorm:"serializer:json" json:"original"`
	Current         Snapshot      `gorm:"serializer:json" json:"current"`
	Status          string        `gorm:"index" json:"status"`
	Stage           string        `json:"stage"`
	Error           string        `json:"error,omitempty"`
	Warning         string        `json:"warning,omitempty"`
	CancelRequested bool          `json:"cancel_requested"`
	Results         []TrialResult `gorm:"serializer:json" json:"results"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

func (Operation) TableName() string { return "mbn_operations" }

func (o Operation) Running() bool {
	return o.Status == "queued" || o.Status == "running" || o.Status == "cancelling"
}

type Registration struct {
	Registered, Attached, IMS bool
	PLMN                      string
}

// Session retains physical/SIM ownership across enumeration, not a serial lock.
type Session interface {
	Owner() Owner
	Prepare(context.Context, Request) error
	Execute(context.Context, string) (string, error)
	Upload(context.Context, string, []byte) error
	Reboot(context.Context, time.Duration) error
	Registration(context.Context) (Registration, error)
	Probe(context.Context, string) error
	Close() error
	Resume(context.Context) error
}

type Gateway interface {
	Identity(context.Context, string) (Owner, error)
	Open(context.Context, string, bool) (Session, error)
}

type Store interface {
	SaveOperation(context.Context, *Operation) error
	Operation(context.Context, string) (Operation, error)
	Operations(context.Context, string) ([]Operation, error)
	FindRequest(context.Context, string, string) (Operation, error)
	Policies(context.Context) ([]Policy, error)
	SavePolicy(context.Context, Policy) error
	Assets(context.Context) ([]Asset, error)
	SaveAsset(context.Context, Asset) error
	DeleteAsset(context.Context, string) error
}

type Files interface {
	Put(string, []byte) error
	Read(string) ([]byte, error)
	Delete(string) error
}
