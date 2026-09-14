package assistant

import (
	"strings"
	"testing"
)

func TestSecretsAndStateSurviveDatabaseReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{APIToken: "private-api-secret", RelayToken: "private-relay-secret", IntervalSeconds: 180}
	st := State{AccountID: 7, Phase: "history", Page: 3, AnchorSet: true, AnchorID: 100, HighWater: 100}
	if err = s.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err = s.DB.QueryRow("SELECT body FROM settings WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "private-") {
		t.Fatal("credentials stored as plaintext")
	}
	s.DB.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	got, err := s.Config()
	if err != nil || got.APIToken != cfg.APIToken || got.RelayToken != cfg.RelayToken {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	restored, err := s.State()
	if err != nil || restored.Page != 3 || restored.HighWater != 100 {
		t.Fatalf("checkpoint lost: %+v %v", restored, err)
	}
}

func TestPageEventAndCheckpointAreAtomic(t *testing.T) {
	s := testStore(t)
	old := State{AccountID: 7, Phase: "live", Page: 1, AnchorSet: true, AnchorID: 1, HighWater: 1}
	seed(t, s, old)
	if _, err := s.DB.Exec("CREATE TRIGGER fail_event BEFORE INSERT ON outbox BEGIN SELECT RAISE(FAIL,'simulated disk error'); END"); err != nil {
		t.Fatal(err)
	}
	next := old
	next.HighWater = 2
	if err := s.ImportPage(next, []Notification{notification(2)}, true); err == nil {
		t.Fatal("expected insert failure")
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM notifications") != 0 || stateOf(t, s).HighWater != 1 {
		t.Fatal("partially committed failed page")
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_event"); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportPage(next, []Notification{notification(2)}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportPage(next, []Notification{notification(2)}, true); err != nil {
		t.Fatal(err)
	}
	if queryInt(t, s, "SELECT COUNT(*) FROM outbox") != 1 {
		t.Fatal("replayed page created duplicate events")
	}
}
