package assistant

import (
	"context"
	"encoding/json"
	"time"
)

const tokenCheckInterval = 24 * time.Hour

func canReadWeb(cfg Config, st State) bool {
	return cfg.Cookie != "" && (st.Verified || (st.TokenIssue != "" && st.AccountID > 0 && st.Username != ""))
}

func (st *State) tokenValid(now time.Time) {
	st.TokenIssue = ""
	st.TokenCheckedAt = now
	st.NextTokenCheck = now.Add(tokenCheckInterval)
}

// A quiet account still needs to discover an expired/revoked Token. The token
// endpoint has no read-state side effects and uses the existing shared budget.
func (e *Engine) checkTokenLocked(ctx context.Context, cfg Config, st *State, now time.Time) (bool, error) {
	if st.Quota.Exhausted(now) {
		st.NextAPI = st.Quota.Reset.Add(time.Second)
		return false, e.Store.SaveState(*st)
	}
	if e.Budget != nil {
		allowed, err := e.Budget.Reserve(now)
		if err != nil || !allowed {
			return false, err
		}
	}
	st.Quota.Remaining--
	st.NextAPI = now.Add(st.Quota.Delay(now))
	st.NextTokenCheck = now.Add(15 * time.Minute)
	if err := e.Store.SaveState(*st); err != nil {
		return true, err
	}
	var result json.RawMessage
	headers, err := e.requestAPI(ctx, cfg.APIToken, "token", &result, now)
	st.Quota.Observe(headers, now)
	st.NextAPI = now.Add(st.Quota.Delay(now))
	if err != nil {
		return true, e.recordFailure(*st, headers, err, now)
	}
	st.tokenValid(now)
	return true, e.Store.SaveState(*st)
}
