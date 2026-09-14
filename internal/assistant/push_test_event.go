package assistant

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

var pushTestID = regexp.MustCompile(`^[0-9a-f]{64}$`)

const pushTestLifetime = 15 * time.Minute

type PushTestState struct {
	NextAllowed int64                `json:"next_allowed"`
	Latest      *DeliveryHistoryItem `json:"latest"`
}

func (s *Store) PushTestState() (PushTestState, error) {
	var state PushTestState
	var eventID string
	if err := s.DB.QueryRow("SELECT next_allowed,event_id FROM push_test_schedule WHERE id=1").Scan(&state.NextAllowed, &eventID); err != nil {
		return state, err
	}
	if eventID == "" {
		return state, nil
	}
	page, err := s.deliveryHistory(0, 1, "", "", "test", eventID)
	if err == nil && len(page.Items) > 0 {
		state.Latest = &page.Items[0]
	}
	return state, err
}

type pushTestError struct {
	status     int
	message    string
	retryAfter int
}

func (e *pushTestError) Error() string { return e.message }

// A browser-generated ID survives uncertain HTTP responses. Replaying it only
// returns the existing event; it cannot reset the relay schedule or resend APNs.
func (e *Engine) QueuePushTest(eventID string, now time.Time) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	if !pushTestID.MatchString(eventID) {
		return &pushTestError{status: 400, message: "测试请求编号不正确"}
	}
	var existing string
	err := e.Store.DB.QueryRow("SELECT payload FROM outbox WHERE event_id=?", eventID).Scan(&existing)
	if err == nil {
		var event Event
		if json.Unmarshal([]byte(existing), &event) != nil || event.Type != "test" {
			return &pushTestError{status: 409, message: "请求编号已被其他事件使用"}
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	if !cfg.Enabled || !st.Verified || st.AuthBlocked || st.AccountID <= 0 || st.Username == "" {
		return &pushTestError{status: 409, message: "请先启用同步，并等待 V2EX 账号验证通过"}
	}
	if cfg.RelayURL != PushServiceURL || cfg.RelayToken == "" {
		return &pushTestError{status: 409, message: "请先在连接与设置中配对接收设备"}
	}
	var blocked bool
	if err = e.Store.DB.QueryRow("SELECT blocked FROM relay_schedule WHERE id=1").Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return &pushTestError{status: 409, message: "推送凭据已失效，请重新配对接收设备"}
	}
	test, err := e.Store.PushTestState()
	if err != nil {
		return err
	}
	if test.NextAllowed > now.Unix() {
		return &pushTestError{status: 429, message: "测试请求过于频繁，请等待三分钟冷却结束", retryAfter: int(test.NextAllowed - now.Unix())}
	}
	if test.Latest != nil && test.Latest.Status == "pending" && test.Latest.CreatedAt.Add(pushTestLifetime).After(now) {
		return &pushTestError{status: 409, message: "上一条测试仍在处理中，请等待回执或测试过期"}
	}
	event := Event{EventID: eventID, Type: "test", SourceAccountID: st.AccountID,
		Title: "@" + st.Username, Body: PushTestBody, CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(pushTestLifetime)}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := e.Store.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO outbox(event_id,payload,priority,detail) VALUES(?,?,1,?)", eventID, string(raw), "测试已加入队列，等待上报"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE push_test_schedule SET next_allowed=?,event_id=? WHERE id=1", now.Add(relayInterval).Unix(), eventID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) queuePushTest(e *Engine, w http.ResponseWriter, r *http.Request) {
	var input struct {
		EventID string `json:"event_id"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, 400, "测试请求格式不正确")
		return
	}
	if err := e.QueuePushTest(input.EventID, time.Now()); err != nil {
		var problem *pushTestError
		if errors.As(err, &problem) {
			if problem.retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(problem.retryAfter))
			}
			fail(w, problem.status, problem.message)
		} else {
			fail(w, 500, "无法保存测试请求，请使用同一请求重试")
		}
		return
	}
	writeJSON(w, 202, map[string]string{"event_id": input.EventID, "status": "queued"})
}
