package assistant

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// A daily account-local window in fixed UTC+8, independent of host timezone.
type PushSchedule struct {
	Mode  string `json:"mode"`
	Start string `json:"start"`
	End   string `json:"end"`
}

var pushTimezone = time.FixedZone("UTC+8", 8*60*60)
var ErrQuietHours = errors.New("休息时段，检查与推送已暂停")
var ErrSyncDisabled = errors.New("同步已暂停，请先启用同步与上报")

func defaultPushSchedule() *PushSchedule {
	return &PushSchedule{Mode: "window", Start: "08:00", End: "24:00"}
}

func clockMinute(s string, end bool) (int, error) {
	if end && s == "24:00" {
		return 1440, nil
	}
	if len(s) != 5 || s[2] != ':' {
		return 0, errors.New("时间格式应为 HH:mm")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("时间格式应为 HH:mm")
		}
	}
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	if h > 23 || m > 59 {
		return 0, errors.New("开始时间为 00:00–23:59；结束时间还可使用 24:00")
	}
	return h*60 + m, nil
}

func (s PushSchedule) validate() error {
	if s.Mode != "window" && s.Mode != "all_day" {
		return errors.New("请选择指定时段或全天运行")
	}
	a, err := clockMinute(s.Start, false)
	if err != nil {
		return err
	}
	b, err := clockMinute(s.End, true)
	if err != nil {
		return err
	}
	if s.Mode == "window" && a == b {
		return errors.New("开始与结束不能相同；如需全天运行，请选择全天运行")
	}
	return nil
}

func (c Config) schedule() PushSchedule {
	if c.PushSchedule == nil {
		return *defaultPushSchedule()
	}
	return *c.PushSchedule
}

// window returns the active interval, or the next interval during rest.
// The start is inclusive and the end is exclusive, including overnight ranges.
func (s PushSchedule) window(now time.Time) (start, end time.Time, allowed bool) {
	if s.validate() != nil {
		return time.Time{}, time.Time{}, false // Invalid persisted data fails closed.
	}
	if s.Mode == "all_day" {
		return time.Time{}, time.Time{}, true
	}
	a, _ := clockMinute(s.Start, false)
	b, _ := clockMinute(s.End, true)
	if a == 0 && b == 1440 {
		return time.Time{}, time.Time{}, true
	}
	duration := b - a
	if duration <= 0 {
		duration += 1440
	}
	local := now.In(pushTimezone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, pushTimezone)
	for _, offset := range []int{-1, 0, 1} {
		start = day.AddDate(0, 0, offset).Add(time.Duration(a) * time.Minute)
		end = start.Add(time.Duration(duration) * time.Minute)
		if now.Before(end) {
			return start, end, !now.Before(start)
		}
	}
	return
}

func (c Config) allowsPush(now time.Time) bool {
	_, _, allowed := c.schedule().window(now)
	return allowed
}

func quietHoursError(c Config, now time.Time) error {
	start, _, _ := c.schedule().window(now)
	return fmt.Errorf("%w，将于 %s（UTC+8）恢复", ErrQuietHours, start.In(pushTimezone).Format("01-02 15:04"))
}

// Stop an operation that straddles the boundary before it can issue another
// request. A relative timeout also works with the engine's injected test time.
func (c Config) windowContext(ctx context.Context, now time.Time) (context.Context, context.CancelFunc) {
	_, end, _ := c.schedule().window(now)
	if end.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, end.Sub(now))
}

type PushScheduleStatus struct {
	Timezone   string    `json:"timezone"`
	ServerTime time.Time `json:"server_time"`
	Resting    bool      `json:"resting"`
	NextStart  time.Time `json:"next_start"`
	WindowEnd  time.Time `json:"window_end"`
}

func (c Config) scheduleStatus(now time.Time) PushScheduleStatus {
	start, end, allowed := c.schedule().window(now)
	status := PushScheduleStatus{Timezone: "UTC+8", ServerTime: now, Resting: c.Enabled && !allowed}
	if c.Enabled {
		if allowed {
			status.WindowEnd = end
		} else {
			status.NextStart = start
		}
	}
	return status
}
