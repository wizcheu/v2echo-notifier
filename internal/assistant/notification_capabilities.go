package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type notificationCapabilities struct {
	Routing     int `json:"notification_routing"`
	Encryption  int `json:"notification_body_encryption"`
	Details     int `json:"notification_details"`
	Samples     int `json:"notification_test_samples"`
	MaxEvents   int `json:"max_events"`
	MaxReceipts int `json:"max_receipts"`
	MaxBytes    int `json:"max_sync_bytes"`
}

func (e *Engine) notificationCapabilities(ctx context.Context, base string) (notificationCapabilities, error) {
	var caps notificationCapabilities
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/health", nil)
	if err != nil {
		return caps, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.Relay.Do(req)
	if err != nil {
		return caps, errors.New("暂时无法确认推送服务能力，原事件保留等待重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return caps, &notificationHTTPError{header: resp.Header.Clone()}
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&caps)
	if err != nil || caps.Encryption < 2 || caps.Routing < 1 || caps.Details < 1 || caps.Samples < 1 || caps.MaxEvents < 1 || caps.MaxReceipts < 1 || caps.MaxBytes < 4096 {
		return caps, errors.New("请先升级推送服务以支持通知分类跳转、标题与正文加密和分类测试；原事件保留等待重试")
	}
	return caps, nil
}
