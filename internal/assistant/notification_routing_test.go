package assistant

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNotificationRoutingUsesActionAndRealNotificationID(t *testing.T) {
	// Words in a linked topic and quoted reply must never determine routing.
	topic := `<a href="/t/123">回复 提及 感谢 收藏 mentioned thanked favorited</a>`
	for _, tc := range []struct{ action, kind string }{
		{"在 " + topic + " 回复了你", "reply"},
		{"replied to " + topic, "reply"},
		{"在回复 " + topic + " 时提到了你", "mention"},
		{"mentioned you in " + topic, "mention"},
		{"感谢了你发布的主题 › " + topic, "thanks_topic"},
		{"thanked your topic " + topic, "thanks_topic"},
		{"感谢了你在主题 › " + topic + " 里的回复", "thanks_reply"},
		{"thanked your reply in " + topic, "thanks_reply"},
		{"收藏了你的主题 " + topic, "favorite"},
		{"favorited your topic " + topic, "favorite"},
		{"新的动作 " + topic, "other"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			n := notification(42)
			n.Text, n.PayloadRendered = tc.action, "感谢了你的回复 mentioned reply"
			event := notificationEvent(State{AccountID: 7, Username: "tester"}, n, time.Now(), 1)
			if event.NotificationKind != tc.kind || event.NotificationID != 42 {
				t.Fatal("routing must use the structural action and original ID", event)
			}
		})
	}
}

func TestNotificationRoutingWaitsForCapableService(t *testing.T) {
	e, _ := pushTestFixture(t)
	e.Relay.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"notification_body_encryption":2,"notification_details":1,"notification_test_samples":1,"max_events":20,"max_receipts":50,"max_sync_bytes":65536}`), nil
	})
	cfg, _ := e.Store.Config()
	if _, err := e.notificationCapabilities(t.Context(), cfg.RelayURL); err == nil {
		t.Fatal("old service would reject the new routing field")
	}
}

func TestNotificationRoutingPreservesOldCachedWire(t *testing.T) {
	e, _ := pushTestFixture(t)
	_, _, event, _ := bodyFixture(t)
	cfg, _ := e.Store.Config()
	var recipients = []bodyRecipient{}
	legacy, err := e.encryptedWire(t.Context(), cfg, event, 0, &recipients)
	if err != nil {
		t.Fatal(err)
	}
	event.NotificationKind = "reply"
	retried, err := e.encryptedWire(t.Context(), cfg, event, 1, &recipients)
	if err != nil || string(legacy) != string(retried) || strings.Contains(string(retried), "notification_kind") {
		t.Fatal("an existing idempotent submission must never be rewritten", err)
	}
	event.EventID = strings.Repeat("e", 64)
	raw, err := e.encryptedWire(t.Context(), cfg, event, 0, &recipients)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["notification_kind"] != "reply" || wire["notification_id"] != float64(event.NotificationID) || wire["title"] != nil || wire["body"] != nil {
		t.Fatal("new routing metadata must coexist with encrypted content", wire)
	}
}
