package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const checkHistoryLimit = 50

type CheckStage struct {
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Detail     string `json:"detail"`
}

// Counts are observations from this attempt only. A missing count is never zero.
// Do not store credentials, request URLs or upstream response bodies here.
type CheckRecord struct {
	ID              int64      `json:"id"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      time.Time  `json:"finished_at"`
	DurationMS      int64      `json:"duration_ms"`
	Trigger         string     `json:"trigger"`
	Mode            string     `json:"mode"`
	Status          string     `json:"status"`
	UnreadCount     *int       `json:"unread_count"`
	FirstID         *int64     `json:"first_id"`
	PreviousFirstID *int64     `json:"previous_first_id"`
	Web             CheckStage `json:"web"`
	API             CheckStage `json:"api"`
	Summary         string     `json:"summary"`
	Outcome         string     `json:"outcome"`
	EventID         string     `json:"event_id,omitempty"`
}

func newCheckRecord(st *State, now time.Time, mode string) CheckRecord {
	c := CheckRecord{StartedAt: now.UTC(), Trigger: "automatic", Mode: mode, Status: "failed",
		Web:     CheckStage{Status: "skipped", Detail: "本轮未读取网页首页"},
		API:     CheckStage{Status: "skipped", Detail: "本轮未发起通知 API 请求"},
		Summary: "检查未完成", Outcome: "本轮未创建上报事件"}
	if st.CheckRequested {
		c.Trigger = "manual"
		st.CheckRequested = false
	}
	if st.HasPushAPIID {
		first := st.LastPushAPIID
		c.PreviousFirstID = &first
	}
	return c
}

func (s *Store) finishCheck(c *CheckRecord, start time.Time, result *error) {
	c.DurationMS = time.Since(start).Milliseconds()
	c.FinishedAt = c.StartedAt.Add(time.Duration(c.DurationMS) * time.Millisecond)
	if *result != nil {
		c.Status = "failed"
		c.Summary = "检查结果未能完整保存"
		c.Outcome = "本轮未完成本地保存，请检查服务器存储后重试"
		c.EventID = ""
	}
	*result = errors.Join(*result, s.SaveCheckRecord(*c))
}

func (s *Store) SaveCheckRecord(c CheckRecord) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO check_history(body) VALUES(?)", string(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM check_history WHERE id NOT IN (SELECT id FROM check_history ORDER BY id DESC LIMIT ?)", checkHistoryLimit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CheckHistory() ([]CheckRecord, error) {
	rows, err := s.DB.Query("SELECT id,body FROM check_history ORDER BY id DESC LIMIT ?", checkHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CheckRecord{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var item CheckRecord
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		item.ID = id
		items = append(items, item)
	}
	return items, rows.Err()
}

func failedCheckStage(err error, start time.Time, fallback string) CheckStage {
	s := CheckStage{Status: "failed", DurationMS: time.Since(start).Milliseconds(), Detail: fallback}
	var we *webRequestError
	var ae *APIError
	if errors.As(err, &we) {
		s.HTTPStatus, s.Detail = we.status, we.message
	} else if errors.As(err, &ae) {
		s.HTTPStatus, s.Detail = ae.Code, ae.Error()
	}
	return s
}

func (s *Server) checkHistory(e *Engine, w http.ResponseWriter, r *http.Request) {
	items, err := e.Store.CheckHistory()
	if err != nil {
		fail(w, 500, "无法读取检查记录，请重试")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": checkHistoryLimit})
}
