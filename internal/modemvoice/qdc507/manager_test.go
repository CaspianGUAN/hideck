package qdc507

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

type testClient struct {
	failStop bool
	scripts  []string
	boot     string
	bindErr  error
}

func (c *testClient) Bind(context.Context, string) (Target, error) {
	if c.bindErr != nil {
		return Target{}, c.bindErr
	}
	if c.boot != "" {
		return Target{usb: "3-2.1", boot: c.boot}, nil
	}
	return Target{usb: "3-2.1", boot: "11111111-2222-3333-4444-555555555555"}, nil
}
func (c *testClient) Upload(context.Context, Target, Upload) error { return nil }
func (c *testClient) Shell(_ context.Context, _ Target, script string) (ShellResult, error) {
	c.scripts = append(c.scripts, script)
	if script == stopRouteScript && c.failStop {
		return ShellResult{Status: 70}, nil
	}
	return ShellResult{}, nil
}

func testManager(t *testing.T, c *testClient) *Manager {
	t.Helper()
	m, err := NewManager(Options{USB: "3-2.1", Firmware: "QDC507GLEFM21", Client: c, Source: fstest.MapFS{}, Check: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	m.target, _ = c.Bind(context.Background(), "3-2.1")
	return m
}

func TestFailedCleanupRetainsLeaseAndCanBeRetried(t *testing.T) {
	c := &testClient{failStop: true}
	m := testManager(t, c)
	lease, err := m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err == nil {
		t.Fatal("failed cleanup accepted")
	}
	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("started over failed cleanup")
	}
	c.failStop = false
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyFailurePreventsRemoteMutation(t *testing.T) {
	c := &testClient{}
	m := testManager(t, c)
	m.options.Check = func(context.Context) error { return errors.New("SIM generation changed") }
	if _, err := m.Prepare(context.Background()); err == nil {
		t.Fatal("policy ignored")
	}
	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("policy ignored")
	}
	if len(c.scripts) != 0 {
		t.Fatal("contacted module after policy failure")
	}
}

func TestIdentityChangeAfterLaunchReturnsCleanupOwner(t *testing.T) {
	c := &testClient{}
	m := testManager(t, c)
	checks := 0
	m.options.Check = func(context.Context) error {
		checks++
		if checks == 3 {
			return errors.New("SIM changed")
		}
		return nil
	}
	lease, err := m.Start(context.Background())
	if err == nil || lease == nil {
		t.Fatalf("lease=%v err=%v", lease, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if m.active != nil {
		t.Fatal("route ownership remained after cleanup")
	}
}

func TestCleanupAfterModuleRebootDoesNotTouchNewRuntime(t *testing.T) {
	c := &testClient{}
	m := testManager(t, c)
	lease, err := m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.scripts = nil
	c.bindErr = errors.New("USB temporarily absent")
	if err := lease.Close(); err == nil || m.active != lease {
		t.Fatal("missing USB was treated as completed cleanup")
	}
	c.bindErr = nil
	c.boot = "99999999-2222-3333-4444-555555555555"
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.scripts) != 0 || m.active != nil || m.target.boot != "" {
		t.Fatalf("new boot touched or old boot retained: scripts=%v target=%v", c.scripts, m.target)
	}
	if _, err := m.Start(context.Background()); err == nil {
		t.Fatal("new boot reused old preparation")
	}
}
