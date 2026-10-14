package mbn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type testSession struct {
	mu            sync.Mutex
	owner         Owner
	auto          bool
	selected      string
	active        string
	commands      []string
	reboots       int
	rebootErr     error
	rebootEntered chan struct{}
	rebootRelease chan struct{}
	registration  Registration
	prepared      int
	uploadEnabled bool
	uploaded      []byte
	imported      bool
	deleted       string
}

func (f *testSession) Owner() Owner                           { return f.owner }
func (f *testSession) Prepare(context.Context, Request) error { f.prepared++; return nil }
func (f *testSession) Probe(context.Context, string) error    { return errors.New("probe not configured") }
func (f *testSession) Close() error                           { return nil }
func (f *testSession) Resume(context.Context) error           { return nil }
func (f *testSession) Upload(_ context.Context, _ string, data []byte) error {
	if f.uploadEnabled {
		f.uploaded = append([]byte(nil), data...)
		return nil
	}
	return errors.New("upload unavailable in this fixture")
}
func (f *testSession) Registration(context.Context) (Registration, error) { return f.registration, nil }
func (f *testSession) Reboot(ctx context.Context, _ time.Duration) error {
	if f.rebootEntered != nil {
		select {
		case f.rebootEntered <- struct{}{}:
		default:
		}
	}
	if f.rebootRelease != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.rebootRelease:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reboots++
	if f.rebootErr != nil {
		return f.rebootErr
	}
	f.active = f.selected
	return nil
}
func (f *testSession) Execute(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	switch cmd {
	case "AT+QMBNCFG=?":
		return `+QMBNCFG:("List","Select","AutoSel","Add","Delete")`, nil
	case `AT+QMBNCFG="AutoSel"`:
		if f.auto {
			return `+QMBNCFG:"AutoSel",1`, nil
		}
		return `+QMBNCFG:"AutoSel",0`, nil
	case `AT+QMBNCFG="AutoSel",0`:
		f.auto = false
		return "OK", nil
	case `AT+QMBNCFG="AutoSel",1`:
		f.auto = true
		return "OK", nil
	case `AT+QMBNCFG="List"`:
		var response string
		names := []string{"A", "B"}
		if f.imported {
			names = append(names, "C")
		}
		for i, name := range names {
			if name == f.deleted {
				continue
			}
			sel, act := 0, 0
			if name == f.selected {
				sel = 1
			}
			if name == f.active {
				act = 1
			}
			response += fmt.Sprintf("+QMBNCFG:\"List\",%d,%d,%d,\"%s\",v1,20260101\n", i, sel, act, name)
		}
		return response, nil
	}
	if strings.HasPrefix(cmd, `AT+QMBNCFG="Add",`) && len(f.uploaded) > 0 {
		f.imported = true
		return "OK", nil
	}
	if strings.HasPrefix(cmd, `AT+QFDEL=`) {
		return "OK", nil
	}
	if strings.HasPrefix(cmd, `AT+QMBNCFG="Delete",`) {
		f.deleted = strings.Trim(strings.TrimPrefix(cmd, `AT+QMBNCFG="Delete",`), `"`)
		return "OK", nil
	}
	if strings.HasPrefix(cmd, `AT+QMBNCFG="Select",`) {
		f.selected = strings.Trim(strings.TrimPrefix(cmd, `AT+QMBNCFG="Select",`), `"`)
		return "OK", nil
	}
	return "", fmt.Errorf("unexpected command %s", cmd)
}

type testGateway struct{ session *testSession }

func (g testGateway) Identity(context.Context, string) (Owner, error) { return g.session.owner, nil }

func (g testGateway) Open(context.Context, string, bool) (Session, error) { return g.session, nil }

func testService(t *testing.T) (*Service, *testSession) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&Asset{}, &Policy{}, &Operation{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := database.DB()
	t.Cleanup(func() { sqlDB.Close() })
	f := &testSession{owner: Owner{DeviceID: "d", IMEI: "imei", ICCID: "card", Model: "EC20F", Firmware: "firmware"}, selected: "A", active: "A"}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Options{Context: ctx, Store: NewDatabaseStore(database), Files: DiskFiles{Directory: t.TempDir()}, Gateway: testGateway{f}})
	t.Cleanup(func() { cancel(); s.Wait() })
	return s, f
}
func waitOperation(t *testing.T, s *Service, id string) Operation {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		o, err := s.Operation(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !o.Running() {
			s.Wait()
			return o
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return Operation{}
}
func TestApplyVerifiedAndIdempotent(t *testing.T) {
	s, f := testService(t)
	ctx := context.Background()
	request := Request{RequestID: "same", Kind: "apply", Profile: "B"}
	op, err := s.Start(ctx, "d", request)
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "succeeded" || result.Current.Active() != "B" {
		t.Fatalf("%+v", result)
	}
	again, err := s.Start(ctx, "d", request)
	if err != nil || again.ID != op.ID {
		t.Fatalf("%+v %v", again, err)
	}
	if f.reboots != 1 {
		t.Fatalf("reboots %d", f.reboots)
	}
	request.Profile = "A"
	if _, err := s.Start(ctx, "d", request); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestUnconfirmedRebootIsNotSuccess(t *testing.T) {
	s, f := testService(t)
	f.rebootErr = errors.New("unplugged")
	op, err := s.Start(context.Background(), "d", Request{RequestID: "1", Kind: "apply", Profile: "B"})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status == "succeeded" || !strings.Contains(result.Error, "unplugged") {
		t.Fatalf("%+v", result)
	}
}
func TestRestartNeverReplaysMutation(t *testing.T) {
	s, f := testService(t)
	op := Operation{ID: "old", DeviceID: "d", RequestID: "old", Status: "running", Stage: "applying"}
	if err := s.store.SaveOperation(context.Background(), &op); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := s.Operation(context.Background(), op.ID)
	if err != nil || op.Status != "needs_attention" || len(f.commands) != 0 {
		t.Fatalf("%+v %v", op, err)
	}
}
func TestCancelTrialRestoresOriginal(t *testing.T) {
	s, f := testService(t)
	f.rebootEntered = make(chan struct{}, 1)
	f.rebootRelease = make(chan struct{})
	op, err := s.Start(context.Background(), "d", Request{RequestID: "1", Kind: "trial", Candidates: []string{"B"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.rebootEntered:
	case <-time.After(time.Second):
		t.Fatal("not rebooting")
	}
	if _, err := s.Cancel(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
	close(f.rebootRelease)
	result := waitOperation(t, s, op.ID)
	if result.Status != "cancelled" || f.active != "A" {
		t.Fatalf("%+v active=%s", result, f.active)
	}
}
func TestAssetIntegrityAndConflict(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	a, err := s.Upload(ctx, Asset{Name: "a.mbn"}, []byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, Asset{Name: "a.mbn"}, []byte("different")); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	_, data, err := s.Asset(ctx, a.ID)
	if err != nil || string(data) != "binary" {
		t.Fatalf("%q %v", data, err)
	}
	if err := s.SavePolicy(ctx, Policy{Key: "sim:card", Mode: "fixed", Model: "EC20F", Firmware: "firmware", Profile: "A", AssetID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(ctx, a.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestRestoreRejectsDifferentSIM(t *testing.T) {
	s, f := testService(t)
	prior := Operation{ID: "old", DeviceID: "d", RequestID: "old", Status: "succeeded", Original: Snapshot{Owner: f.owner}}
	prior.Original.Owner.ICCID = "another-card"
	if err := s.store.SaveOperation(context.Background(), &prior); err != nil {
		t.Fatal(err)
	}
	op, err := s.Start(context.Background(), "d", Request{RequestID: "1", Kind: "restore", RestoreOperation: prior.ID})
	if err != nil {
		t.Fatal(err)
	}
	result := waitOperation(t, s, op.ID)
	if result.Status != "needs_attention" || f.reboots != 0 {
		t.Fatalf("%+v", result)
	}
}
