package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const relayInterval = 30 * time.Second
const relayBatchEvents = 20
const relayBatchReceipts = 50

// Acceptance by APNs is not proof of delivery to the device.
type Receipt struct {
	Reason            string `json:"reason,omitempty"`
	EventID           string `json:"event_id"`
	Status            string `json:"status"`
	APNsID            string `json:"apns_id,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}
type syncRequest struct {
	Events   []json.RawMessage `json:"events"`
	Receipts []string          `json:"receipts"`
}
type syncResponse struct {
	Receipts          []Receipt `json:"receipts"`
	RetryAfterSeconds int       `json:"retry_after_seconds"`
}

// Credentials/config edits share relayMu. Schedule lives separately from API
// collection state and survives restart, re-pairing and configuration changes.
func (e *Engine) Deliver(ctx context.Context, now time.Time) error {
	entered := time.Now()
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	effectiveNow := now.Add(time.Since(entered))
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.RelayURL == "" || cfg.RelayToken == "" || !cfg.allowsPush(effectiveNow) {
		return nil
	}
	ctx, cancel := cfg.windowContext(ctx, effectiveNow)
	defer cancel()
	windowStart, _, _ := cfg.schedule().window(effectiveNow)
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	var next int64
	var failures int
	var blocked bool
	if err = e.Store.DB.QueryRow("SELECT next_sync,failures,blocked FROM relay_schedule WHERE id=1").Scan(&next, &failures, &blocked); err != nil {
		return err
	}
	if blocked || next > now.Unix() {
		return nil
	}
	// Expiry must also advance when an older server cannot accept details yet.
	if _, err = e.Store.DB.Exec(`UPDATE outbox SET status='expired',detail='已超过通知有效期'
      WHERE status='pending' AND julianday(json_extract(payload,'$.expires_at'))<=julianday(?)`, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	// Summaries and receipt lookups retain their original protocol. Body-bearing
	// events require encryption support before any submission, including events
	// queued before upgrade. An older service never triggers plaintext fallback.
	var enhanced bool
	if err = e.Store.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM outbox WHERE status='pending' AND submitted=0 AND next_attempt<=?
      AND (json_extract(payload,'$.type')='notification' OR json_extract(payload,'$.test_kind') IS NOT NULL)
      AND (json_extract(payload,'$.type')='test' OR ?))`, now.Unix(), windowStart.IsZero() || !st.LastSuccess.Before(windowStart)).Scan(&enhanced); err != nil {
		return err
	}
	eventLimit, receiptLimit, byteLimit := 4, 16, 16<<10
	if enhanced {
		caps, err := e.notificationCapabilities(ctx, cfg.RelayURL)
		if err != nil {
			_, saveErr := e.Store.DB.Exec(`UPDATE relay_schedule SET next_sync=? WHERE id=1`, notificationRetryAt(err, now))
			if saveErr != nil {
				return saveErr
			}
			_, saveErr = e.Store.DB.Exec(`UPDATE outbox SET detail=? WHERE status='pending' AND submitted=0
              AND (json_extract(payload,'$.type')='notification' OR json_extract(payload,'$.test_kind') IS NOT NULL)`, err.Error())
			return saveErr
		}
		eventLimit, receiptLimit, byteLimit = min(relayBatchEvents, caps.MaxEvents), min(relayBatchReceipts, caps.MaxReceipts), min(64<<10, caps.MaxBytes)
	}
	var recipients []bodyRecipient
	deliveries := []Delivery{}
	// Separate limits prevent pending receipts from starving new notifications.
	for _, submitted := range []bool{false, true} {
		limit := eventLimit
		if submitted {
			limit = receiptLimit
		}
		rows, err := e.Store.DB.Query(`SELECT event_id,status,submitted,attempts,next_attempt,detail,payload FROM outbox
    WHERE status='pending' AND submitted=? AND next_attempt<=? ORDER BY priority DESC,rowid LIMIT ?`, submitted, now.Unix(), limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d Delivery
			if err = rows.Scan(&d.EventID, &d.Status, &d.Submitted, &d.Attempts, &d.NextAttempt, &d.Detail, &d.Payload); err != nil {
				rows.Close()
				return err
			}
			deliveries = append(deliveries, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	batch := syncRequest{Events: []json.RawMessage{}, Receipts: []string{}}
	active := []Delivery{}
	for _, d := range deliveries {
		var ev Event
		if err = json.Unmarshal([]byte(d.Payload), &ev); err != nil {
			return err
		}
		if !ev.ExpiresAt.After(now) {
			d.Status = "expired"
			d.Detail = "已超过通知有效期"
			if err = e.Store.SaveDelivery(d); err != nil {
				return err
			}
			continue
		}
		// Recheck current unread state before resuming automatic pushes. Tests
		// and receipt lookups do not depend on an upstream notification check.
		if !d.Submitted && ev.Type != "test" && !windowStart.IsZero() && st.LastSuccess.Before(windowStart) {
			continue
		}
		if d.Submitted {
			batch.Receipts = append(batch.Receipts, d.EventID)
		} else {
			payload := json.RawMessage(d.Payload)
			if encryptedBodyEvent(ev) {
				payload, err = e.encryptedWire(ctx, cfg, ev, d.Attempts, &recipients)
				if errors.Is(err, errPreEncryptionAttempt) {
					d.Status, d.Detail = "skipped", "升级前已尝试发送的旧事件不再上传明文；请在应用内查看"
					if err = e.Store.SaveDelivery(d); err != nil {
						return err
					}
					continue
				}
				if err != nil {
					if errors.Is(err, ErrPairingRequired) {
						d.Status, d.Detail = "blocked", "推送凭据无效或已撤销，请重新配对"
						return e.Store.saveRelayRound([]Delivery{d}, now.Add(3*time.Minute).Unix(), failures)
					}
					d.Detail = "无法安全准备通知密文（服务不可用或设备密钥变化），保留原事件；请检查连接或重新注册接收设备"
					return e.Store.saveRelayRound([]Delivery{d}, notificationRetryAt(err, now), failures)
				}
			} else if ev.Type == "unread_summary" {
				payload, err = json.Marshal(struct {
					EventID         string    `json:"event_id"`
					Type            string    `json:"type"`
					SourceAccountID int64     `json:"source_account_id"`
					UnreadCount     int       `json:"unread_count"`
					CreatedAt       time.Time `json:"created_at"`
					ExpiresAt       time.Time `json:"expires_at"`
				}{ev.EventID, ev.Type, ev.SourceAccountID, ev.UnreadCount, ev.CreatedAt, ev.ExpiresAt})
				if err != nil {
					return err
				}
			} else if ev.Type == "test" {
				// The test template is service-owned; local preview text stays local.
				payload, err = json.Marshal(struct {
					EventID         string    `json:"event_id"`
					Type            string    `json:"type"`
					SourceAccountID int64     `json:"source_account_id"`
					CreatedAt       time.Time `json:"created_at"`
					ExpiresAt       time.Time `json:"expires_at"`
					TestKind        string    `json:"test_kind,omitempty"`
				}{ev.EventID, ev.Type, ev.SourceAccountID, ev.CreatedAt, ev.ExpiresAt, ev.TestKind})
				if err != nil {
					return err
				}
			}
			batch.Events = append(batch.Events, payload)
		}
		d.Attempts++
		active = append(active, d)
	}
	if len(active) == 0 {
		return nil
	} // No heartbeat or empty receipt polling.
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	// JSON escaping can make otherwise valid events exceed the wire limit.
	// Leave overflow events untouched in the queue for a later round.
	for len(raw) > byteLimit {
		if len(batch.Events) == 0 {
			return fmt.Errorf("批量请求超过大小限制")
		}
		var overflow Event
		if err = json.Unmarshal(batch.Events[len(batch.Events)-1], &overflow); err != nil {
			return err
		}
		batch.Events = batch.Events[:len(batch.Events)-1]
		kept := active[:0]
		for _, d := range active {
			if d.EventID != overflow.EventID {
				kept = append(kept, d)
			}
		}
		active = kept
		raw, err = json.Marshal(batch)
		if err != nil {
			return err
		}
	}
	if len(active) == 0 {
		// A server advertising a smaller capacity may not fit even one immutable
		// encrypted event. Keep it queued without sending an invalid empty batch.
		_, err = e.Store.DB.Exec("UPDATE relay_schedule SET next_sync=? WHERE id=1", now.Add(3*time.Minute).Unix())
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RelayURL+"/v1/sync", strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.RelayToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	failures++
	next = now.Add(max(relayInterval, min(time.Hour, backoff(failures))) + time.Duration(rand.IntN(16))*time.Second).Unix()
	for i := range active {
		active[i].NextAttempt = next
	}
	// Atomic reservation before network I/O also covers a crash/lost response.
	if err = e.Store.saveRelayRound(active, next, failures); err != nil {
		return err
	}
	resp, err := e.Relay.Do(req)
	if err != nil {
		for i := range active {
			active[i].Detail = "推送服务连接失败，将保留原事件并退避重试"
		}
		return e.Store.saveRelayRound(active, next, failures)
	}
	defer resp.Body.Close()
	headerNext := now.Add(retryAfter(resp.Header, now)).Unix()
	next = max(next, headerNext)
	valid := false
	if resp.StatusCode == 200 {
		var result syncResponse
		// A shared connection can return 10 device results per event/receipt.
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
		err = readErr
		if err == nil {
			if len(responseBody) > 256<<10 {
				err = fmt.Errorf("回执超过大小限制")
			} else {
				err = json.Unmarshal(responseBody, &result)
			}
		}
		receipts := map[string]Receipt{}
		if err == nil && len(result.Receipts) == len(active) {
			for _, r := range result.Receipts {
				receipts[r.EventID] = r
			}
			valid = len(receipts) == len(active)
			for _, d := range active {
				r, ok := receipts[d.EventID]
				if !ok || (r.Status != "pending" && r.Status != "apns_accepted" && r.Status != "rejected" && r.Status != "expired" && !(r.Status == "not_found" && d.Submitted)) {
					valid = false
				}
			}
		}
		if valid {
			failures = 0
			next = max(headerNext, now.Add(max(relayInterval, time.Duration(min(max(result.RetryAfterSeconds, 0), 172800))*time.Second)+time.Duration(rand.IntN(16))*time.Second).Unix())
			for i := range active {
				d := &active[i]
				r := receipts[d.EventID]
				d.NextAttempt = next
				switch r.Status {
				case "apns_accepted":
					d.Submitted = true
					d.Status = "apns_accepted"
					d.Detail = "APNs 已接收，不代表设备已送达"
				case "pending":
					d.Submitted = true
					d.Detail = "已提交，等待下一轮查询推送结果；设备可能已经收到消息。"
					d.NextAttempt = max(next, now.Add(time.Duration(min(max(r.RetryAfterSeconds, 0), 172800))*time.Second).Unix())
				case "rejected":
					d.Submitted = true
					d.Status = "rejected"
					d.Detail = "推送服务或 APNs 已拒绝事件"
					if r.Reason == "before_binding" {
						d.Status = "skipped"
						d.Detail = "通知产生于本次配对之前，已跳过"
					}
				case "expired":
					d.Submitted = true
					d.Status = "expired"
					d.Detail = "服务端事件已过期"
				case "not_found":
					d.Detail = "服务端暂未找到已接收事件的回执，将继续查询"
					d.NextAttempt = max(next, now.Add(min(time.Hour, backoff(d.Attempts))).Unix())
				}
			}
		}
	}
	if !valid {
		for i := range active {
			d := &active[i]
			d.NextAttempt = next
			switch {
			case resp.StatusCode == 401 || resp.StatusCode == 403:
				d.Status = "blocked"
				d.Detail = "推送凭据无效或已撤销，请重新配对"
			case resp.StatusCode == 429 || resp.StatusCode >= 500:
				d.Detail = fmt.Sprintf("推送服务暂不可用或额度受限（HTTP %d），等待下一轮", resp.StatusCode)
			case enhanced && (resp.StatusCode == 400 || resp.StatusCode == 404 || resp.StatusCode == 413):
				d.Detail = "推送服务尚不支持本批详细通知，请先升级推送服务；原事件保留等待重试"
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				d.Status = "rejected"
				d.Detail = fmt.Sprintf("推送服务拒绝本批事件（HTTP %d）", resp.StatusCode)
			default:
				d.Detail = "批量回执格式或事件 ID 不匹配，保留原事件等待重试"
			}
		}
	}
	return e.Store.saveRelayRound(active, next, failures)
}

var ErrPairingRequired = errors.New("接收设备需重新配对，自动检查与推送已暂停")

func (s *Store) RelayBlocked() (bool, error) {
	var blocked bool
	err := s.DB.QueryRow("SELECT blocked FROM relay_schedule WHERE id=1").Scan(&blocked)
	return blocked, err
}

func (s *Store) saveRelayRound(deliveries []Delivery, next int64, failures int) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	blocked := false
	for _, d := range deliveries {
		if d.Status == "blocked" {
			blocked = true
		}
	}
	if _, err = tx.Exec("UPDATE relay_schedule SET next_sync=?,failures=?,blocked=MAX(blocked,?) WHERE id=1", next, failures, blocked); err != nil {
		return err
	}
	for _, d := range deliveries {
		if _, err = tx.Exec("UPDATE outbox SET status=?,submitted=?,attempts=?,next_attempt=?,detail=? WHERE event_id=?", d.Status, d.Submitted, d.Attempts, d.NextAttempt, d.Detail, d.EventID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
