package assistant

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

type APIBudget struct {
	mu sync.Mutex
	DB *sql.DB
}
type budgetState struct {
	Quota Quota     `json:"quota"`
	Next  time.Time `json:"next"`
}

func (b *APIBudget) load() (budgetState, error) {
	var state budgetState
	var raw string
	err := b.DB.QueryRow("SELECT body FROM api_budget WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal([]byte(raw), &state)
	return state, err
}
func (b *APIBudget) save(state budgetState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = b.DB.Exec("INSERT INTO api_budget(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", string(raw))
	return err
}

// Ready lets the scheduler keep its next account while the shared window is
// closed. Advancing every tick can starve accounts when cadence and count align.
func (b *APIBudget) Ready(now time.Time) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, err := b.load()
	if err != nil {
		return false, err
	}
	return !now.Before(state.Next) && !state.Quota.Exhausted(now), nil
}
func (b *APIBudget) Reserve(now time.Time) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, err := b.load()
	if err != nil {
		return false, err
	}
	if now.Before(state.Next) {
		return false, nil
	}
	if state.Quota.Exhausted(now) {
		return false, nil
	}
	state.Quota.Remaining--
	state.Next = now.Add(state.Quota.Delay(now))
	return true, b.save(state)
}
func (b *APIBudget) Observe(headers http.Header, requestError error, now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, err := b.load()
	if err != nil {
		return err
	}
	state.Quota.Observe(headers, now)
	state.Next = maxTime(state.Next, now.Add(state.Quota.Delay(now)))
	var apiError *APIError
	if errors.As(requestError, &apiError) && apiError.Code == 429 {
		state.Quota.Remaining = 0
		state.Next = maxTime(state.Next, maxTime(state.Quota.Reset.Add(time.Second), now.Add(retryAfter(headers, now))))
	}
	return b.save(state)
}
func (b *APIBudget) Snapshot() (budgetState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.load()
}
