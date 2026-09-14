package assistant

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestQuotaAdaptsToRemainingWindow(t *testing.T) {
	now := time.Unix(1789200000, 0)
	q := Quota{Limit: 600, Remaining: 100, Reset: now.Add(1200 * time.Second)}
	if got := q.Delay(now); got != 30*time.Second {
		t.Fatalf("got %v want 30s", got)
	}
	q.Remaining = 60
	if !q.Exhausted(now) || q.Delay(now) != 1201*time.Second {
		t.Fatal("reserve was consumed")
	}
	h := http.Header{}
	h.Set("X-Rate-Limit-Limit", "120")
	h.Set("X-Rate-Limit-Remaining", "50")
	h.Set("X-Rate-Limit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
	q.Observe(h, now)
	if q.Limit != 120 || q.Remaining != 50 || !q.Observed {
		t.Fatal(q)
	}
	h.Set("X-Rate-Limit-Remaining", "99999")
	q.Observe(h, now)
	if q.Remaining != 50 {
		t.Fatal("accepted invalid headers")
	}
	q.Observe(nil, now)
	if q.Remaining != 50 {
		t.Fatal("missing headers reset budget")
	}
}

func TestFallbackWindowRefresh(t *testing.T) {
	now := time.Now()
	q := Quota{}
	if q.Delay(now) < 10*time.Second || q.Remaining != 600 {
		t.Fatal(q)
	}
	q.Remaining = 0
	if !q.Exhausted(now) {
		t.Fatal("empty budget not enforced")
	}
	if q.Exhausted(now.Add(2*time.Hour)) || q.Remaining != 600 {
		t.Fatal("window failed to reset")
	}
}
