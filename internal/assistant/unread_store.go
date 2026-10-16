package assistant

import (
	"encoding/json"
	"time"
)

// Save candidates and pagination atomically. Only a completed, rechecked range
// becomes deliverable. Existing event payloads are immutable, even after retry.
func (s *Store) importUnreadPage(st State, items []Notification, reset, finish, paired bool, now time.Time) (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if reset {
		if _, err = tx.Exec("DELETE FROM unread_scan_items"); err != nil {
			return "", err
		}
	}
	for _, n := range items {
		raw, err := json.Marshal(n)
		if err != nil {
			return "", err
		}
		title, body, _ := notificationPresentation(n, st.Username)
		if _, err = tx.Exec(`INSERT INTO notifications(account_id,id,created,title,body,raw) VALUES(?,?,?,?,?,?)
          ON CONFLICT(account_id,id) DO UPDATE SET title=excluded.title,body=excluded.body,raw=excluded.raw`,
			st.AccountID, n.ID, n.Created, title, body, string(raw)); err != nil {
			return "", err
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO unread_scan_items(id) VALUES(?)", n.ID); err != nil {
			return "", err
		}
	}
	var lastEventID string
	if finish && paired && st.WebUnreadCount > 0 {
		rows, err := tx.Query(`SELECT n.raw FROM notifications n JOIN unread_scan_items c ON c.id=n.id
          WHERE n.account_id=? ORDER BY n.id`, st.AccountID)
		if err != nil {
			return "", err
		}
		var events []Event
		for rows.Next() {
			var raw string
			var n Notification
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal([]byte(raw), &n)
			}
			if err != nil {
				rows.Close()
				return "", err
			}
			events = append(events, notificationEvent(st, n, now, st.WebUnreadCount))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		for _, event := range events {
			raw, err := json.Marshal(event)
			if err != nil {
				return "", err
			}
			result, err := tx.Exec("INSERT OR IGNORE INTO outbox(event_id,payload) VALUES(?,?)", event.EventID, string(raw))
			if err != nil {
				return "", err
			}
			if changed, _ := result.RowsAffected(); changed > 0 {
				lastEventID = event.EventID
			}
		}
	}
	if finish || st.WebUnreadCount == 0 {
		// Never cancel an attempted event: a timeout can hide server acceptance.
		if _, err = tx.Exec(`UPDATE outbox SET status='skipped',detail='当前未读范围已更新，尚未提交的旧提醒已跳过'
          WHERE status='pending' AND submitted=0 AND attempts=0 AND
          (json_extract(payload,'$.type') IN ('unread_summary','initial_unread') OR
           (json_extract(payload,'$.type')='notification' AND
            (?=0 OR json_extract(payload,'$.notification_id') NOT IN (SELECT id FROM unread_scan_items))))`, st.WebUnreadCount); err != nil {
			return "", err
		}
		if _, err = tx.Exec("DELETE FROM unread_scan_items"); err != nil {
			return "", err
		}
	}
	if err = saveState(tx, st); err != nil {
		return "", err
	}
	return lastEventID, tx.Commit()
}
