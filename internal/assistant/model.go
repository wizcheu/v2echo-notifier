package assistant

import "time"

const PushServiceURL = "https://app.v2echo.com/api/push"
const PushTestBody = "这是一条 V2Echo 测试推送，收到此消息说明推送已到达设备。"

type Config struct {
	Cookie          string `json:"cookie,omitempty"`
	ProxyMode       string `json:"proxy_mode"`
	ProxyURL        string `json:"proxy_url"`
	APIToken        string `json:"api_token"`
	RelayURL        string `json:"relay_url"`
	RelayToken      string `json:"relay_token"`
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type State struct {
	TokenIssue         string    `json:"token_issue"`
	CookieIssue        string    `json:"cookie_issue"`
	TokenCheckedAt     time.Time `json:"token_checked_at"`
	CookieCheckedAt    time.Time `json:"cookie_checked_at"`
	NextTokenCheck     time.Time `json:"next_token_check"`
	HasPushAPIID       bool      `json:"has_push_api_id"`
	LastPushAPIID      int64     `json:"last_push_api_id"`
	NextWeb            time.Time `json:"next_web"`
	HasWebUnread       bool      `json:"has_web_unread"`
	WebUnreadCount     int       `json:"web_unread_count"`
	WebObservedAt      time.Time `json:"web_observed_at"`
	InitialPairingDone bool      `json:"initial_pairing_done"`
	InitialUnreadCount int       `json:"initial_unread_count"`
	InitialUnreadAt    time.Time `json:"initial_unread_at"`
	AccountID          int64     `json:"account_id"`
	Username           string    `json:"username"`
	Verified           bool      `json:"verified"`
	Phase              string    `json:"phase"`
	Page               int       `json:"page"`
	AnchorID           int64     `json:"anchor_id"`
	AnchorSet          bool      `json:"anchor_set"`
	HighWater          int64     `json:"high_water"`
	CycleMax           int64     `json:"cycle_max"`
	LastPageMinID      int64     `json:"last_page_min_id"`
	NextCheck          time.Time `json:"next_check"`
	NextAPI            time.Time `json:"next_api"`
	LastSuccess        time.Time `json:"last_success"`
	LastError          string    `json:"last_error"`
	Failures           int       `json:"failures"`
	AuthBlocked        bool      `json:"auth_blocked"`
	Quota              Quota     `json:"quota"`
}

type Notification struct {
	ID              int64   `json:"id"`
	MemberID        int64   `json:"member_id"`
	ForMemberID     int64   `json:"for_member_id"`
	Text            string  `json:"text"`
	Payload         *string `json:"payload"`
	PayloadRendered string  `json:"payload_rendered"`
	Created         int64   `json:"created"`
	Member          struct {
		Username string `json:"username"`
	} `json:"member"`
}

// Event is a notification, initial unread summary, or explicitly requested test.
// Reposts preserve its identity and content even if the source later changes.
type Event struct {
	EventID         string    `json:"event_id"`
	Type            string    `json:"type"`
	SourceAccountID int64     `json:"source_account_id"`
	NotificationID  int64     `json:"notification_id,omitempty"`
	BindingID       string    `json:"binding_id,omitempty"`
	UnreadCount     int       `json:"unread_count,omitempty"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Delivery struct {
	Type        string `json:"type"`
	EventID     string `json:"event_id"`
	Status      string `json:"status"`
	Submitted   bool   `json:"submitted"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt"`
	Detail      string `json:"detail"`
	Payload     string `json:"-"`
}
