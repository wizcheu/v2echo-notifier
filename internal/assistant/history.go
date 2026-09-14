package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type DeliveryHistoryItem struct {
	Delivery
	Sequence       int64     `json:"sequence"`
	NotificationID int64     `json:"notification_id"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	UnreadCount    int       `json:"unread_count"`
	CreatedAt      time.Time `json:"created_at"`
	QueuedAt       int64     `json:"queued_at"`
	FirstAttemptAt int64     `json:"first_attempt_at"`
	LastAttemptAt  int64     `json:"last_attempt_at"`
	SubmittedAt    int64     `json:"submitted_at"`
	FinishedAt     int64     `json:"finished_at"`
}

type DeliveryHistoryPage struct {
	Items      []DeliveryHistoryItem `json:"items"`
	NextCursor int64                 `json:"next_cursor"`
}

// Payloads are the immutable, locally saved upload summaries. The timestamps
// record local observations, never an inferred APNs acceptance/device arrival time.
// Existing v4 rows without history metadata remain readable with unknown times.
func (s *Store) DeliveryHistory(before int64, limit int, status, search string) (DeliveryHistoryPage, error) {
	return s.deliveryHistory(before, limit, status, search, "", "")
}

func (s *Store) deliveryHistory(before int64, limit int, status, search, kind, eventID string) (DeliveryHistoryPage, error) {
	page := DeliveryHistoryPage{Items: []DeliveryHistoryItem{}}
	if before < 0 || limit < 1 || limit > 50 || len([]rune(search)) > 200 {
		return page, errors.New("历史查询参数不正确")
	}
	switch status {
	case "", "pending", "apns_accepted", "rejected", "blocked", "expired", "skipped":
	default:
		return page, errors.New("推送状态不正确")
	}
	where := []string{"1=1"}
	args := []any{}
	if eventID != "" {
		where = append(where, "o.event_id=?")
		args = append(args, eventID)
	}
	if kind != "" {
		where = append(where, "json_extract(o.payload,'$.type')=?")
		args = append(args, kind)
	}
	if before > 0 {
		where = append(where, "o.rowid<?")
		args = append(args, before)
	}
	if status != "" {
		where = append(where, "o.status=?")
		args = append(args, status)
	}
	if search != "" {
		where = append(where, `instr(lower(COALESCE(json_extract(o.payload,'$.title'),'') || char(10) ||
          COALESCE(json_extract(o.payload,'$.body'),'') || char(10) || o.event_id || char(10) ||
          COALESCE(json_extract(o.payload,'$.notification_id'),'')),lower(?))>0`)
		args = append(args, search)
	}
	args = append(args, limit+1)
	rows, err := s.DB.Query(`SELECT o.rowid,o.event_id,o.status,o.submitted,o.attempts,o.next_attempt,o.detail,o.payload,
      COALESCE(h.queued_at,0),COALESCE(h.first_attempt_at,0),COALESCE(h.last_attempt_at,0),COALESCE(h.submitted_at,0),COALESCE(h.finished_at,0)
      FROM outbox o LEFT JOIN delivery_history h ON h.event_id=o.event_id
      WHERE `+strings.Join(where, " AND ")+` ORDER BY o.rowid DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item DeliveryHistoryItem
		if err := rows.Scan(&item.Sequence, &item.EventID, &item.Status, &item.Submitted, &item.Attempts, &item.NextAttempt, &item.Detail, &item.Payload,
			&item.QueuedAt, &item.FirstAttemptAt, &item.LastAttemptAt, &item.SubmittedAt, &item.FinishedAt); err != nil {
			return page, err
		}
		var event Event
		if err := json.Unmarshal([]byte(item.Payload), &event); err != nil {
			return page, err
		}
		item.Type = event.Type
		if item.Type == "" {
			item.Type = "notification"
		}
		item.Title, item.Body, item.NotificationID = event.Title, event.Body, event.NotificationID
		item.UnreadCount, item.CreatedAt = event.UnreadCount, event.CreatedAt
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1].Sequence
	}
	return page, nil
}

func (s *Server) deliveryHistory(e *Engine, w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	before, limit := int64(0), 20
	var err error
	if value := query.Get("before"); value != "" {
		before, err = strconv.ParseInt(value, 10, 64)
	}
	if err != nil || before < 0 {
		fail(w, 400, "历史分页参数不正确")
		return
	}
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	search, status := strings.TrimSpace(query.Get("q")), query.Get("status")
	if err != nil || limit < 1 || limit > 50 || len([]rune(search)) > 200 {
		fail(w, 400, "历史查询参数不正确")
		return
	}
	switch status {
	case "", "pending", "apns_accepted", "rejected", "blocked", "expired", "skipped":
	default:
		fail(w, 400, "推送状态不正确")
		return
	}
	page, err := e.Store.DeliveryHistory(before, limit, status, search)
	if err != nil {
		fail(w, 500, "无法读取推送历史")
		return
	}
	writeJSON(w, 200, page)
}
