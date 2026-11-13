package admin

import "testing"

func TestBootstrapCannotBeReenabledByEnvironmentAfterAdminChange(t *testing.T) {
	m, initial, path, state := setup(t)
	owner := m.Access().List()[0]
	if err := m.ChangeAccess(owner.ID, initial, "update", owner.ID, "owner", "long-replacement-password"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOT_DASHBOARD_TOKEN", initial)
	next, _, err := Open(m.Settings().Config, path, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, ok := next.Access().Verify("local", initial); ok {
		t.Fatal("old bootstrap restored")
	}
	if _, ok := next.Access().Verify("owner", "long-replacement-password"); !ok {
		t.Fatal("updated administrator lost on restart")
	}
}
