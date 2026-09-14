package assistant

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

// Quota is persisted before every outgoing V2EX request. Missing headers never
// turn off the fallback budget. Headers describe the shared upstream IP window,
// not a guaranteed allocation owned by this process.
type Quota struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
	Observed  bool      `json:"observed"`
}

func (q *Quota) refresh(now time.Time) {
	if q.Limit <= 0 {
		q.Limit = 600
	}
	if !q.Reset.After(now) {
		q.Remaining = q.Limit
		q.Reset = now.Add(time.Hour)
		q.Observed = false
	}
}

func (q Quota) Reserve() int { return max(1, int(math.Ceil(float64(q.Limit)*0.1))) }

func (q *Quota) Delay(now time.Time) time.Duration {
	q.refresh(now)
	available := q.Remaining - q.Reserve()
	if available <= 0 {
		return q.Reset.Sub(now) + time.Second
	}
	return max(10*time.Second, time.Duration(math.Ceil(float64(q.Reset.Sub(now))/float64(available))))
}

func (q *Quota) Exhausted(now time.Time) bool { q.refresh(now); return q.Remaining <= q.Reserve() }

func (q *Quota) Observe(h http.Header, now time.Time) {
	q.refresh(now)
	limit, e1 := strconv.Atoi(h.Get("X-Rate-Limit-Limit"))
	remain, e2 := strconv.Atoi(h.Get("X-Rate-Limit-Remaining"))
	reset, e3 := strconv.ParseInt(h.Get("X-Rate-Limit-Reset"), 10, 64)
	if e1 == nil && e2 == nil && e3 == nil && limit > 0 && limit <= 1000000 && remain >= 0 && remain <= limit && reset > now.Unix() && reset <= now.Add(48*time.Hour).Unix() {
		q.Limit = limit
		q.Remaining = remain
		q.Reset = time.Unix(reset, 0)
		q.Observed = true
	}
}

func retryAfter(h http.Header, now time.Time) time.Duration {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n > 0 {
		return time.Duration(min(n, 172800)) * time.Second
	}
	if t, err := http.ParseTime(h.Get("Retry-After")); err == nil && t.After(now) {
		return min(t.Sub(now), 48*time.Hour)
	}
	return 0
}

func backoff(failures int) time.Duration {
	return time.Duration(1<<min(max(failures, 1), 10)) * 15 * time.Second
}
