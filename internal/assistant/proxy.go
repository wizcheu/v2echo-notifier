package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

func normalizeProxy(mode, raw string) (string, string, error) {
	if mode == "" {
		mode = "environment"
	}
	if mode == "environment" || mode == "direct" {
		return mode, "", nil
	}
	if mode != "custom" {
		return "", "", errors.New("代理模式不正确")
	}
	raw = strings.TrimSpace(raw)
	invalid := errors.New("代理地址需为 http:// 或 https:// 地址，可包含用户名和密码，不能包含路径、查询参数或片段")
	if len(raw) == 0 || len(raw) > 4096 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
		return "", "", invalid
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", invalid
		}
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if u.User.Username() == "" || strings.Contains(u.User.Username(), ":") || strings.IndexFunc(u.User.Username()+password, unicode.IsControl) >= 0 {
			return "", "", invalid
		}
	}
	u.Path = ""
	u.RawPath = ""
	return mode, u.String(), nil
}

// Never return userinfo through management APIs, including the username.
func proxyAddress(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

// Each account has its own connection pool. Configuration is read at request
// time, so restart and all three outbound clients use the same persisted policy.
// A failed configured proxy never falls back to a direct connection.
type accountTransport struct {
	store   *Store
	base    *http.Transport
	mu      sync.Mutex
	key     string
	current *http.Transport
}

func (p *accountTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg, err := p.store.Config()
	if err != nil {
		return nil, errors.New("无法读取网络代理设置")
	}
	mode, raw, err := normalizeProxy(cfg.ProxyMode, cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	key := mode + "\n" + raw
	p.mu.Lock()
	if p.current == nil || p.key != key {
		next := proxyTransport(p.base, mode, raw)
		if p.current != nil {
			p.current.CloseIdleConnections()
		}
		p.current = next
		p.key = key
	}
	transport := p.current
	p.mu.Unlock()
	response, err := transport.RoundTrip(req)
	if err != nil {
		// net/http proxy errors may include the credential-bearing proxy URL.
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, errors.New("网络连接失败，请检查网络或代理设置")
	}
	return response, nil
}

func (p *accountTransport) CloseIdleConnections() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current != nil {
		p.current.CloseIdleConnections()
		p.current = nil
		p.key = ""
	}
}

// The same transport policy is used for normal traffic and unsaved test drafts.
func proxyTransport(base *http.Transport, mode, raw string) *http.Transport {
	next := base.Clone()
	switch mode {
	case "environment":
		next.Proxy = http.ProxyFromEnvironment
	case "direct":
		next.Proxy = nil
	case "custom":
		u, _ := url.Parse(raw)
		next.Proxy = http.ProxyURL(u)
	}
	return next
}

type ProxyConfig struct {
	Mode string `json:"proxy_mode"`
	URL  string `json:"proxy_url"`
}

func resolveProxy(input ProxyConfig, saved Config) (ProxyConfig, error) {
	if input.Mode == "custom" && strings.TrimSpace(input.URL) == "" {
		input.URL = saved.ProxyURL
	}
	mode, raw, err := normalizeProxy(input.Mode, input.URL)
	return ProxyConfig{Mode: mode, URL: raw}, err
}

func (e *Engine) ConfigureProxy(input ProxyConfig) error {
	e.relayMu.Lock()
	defer e.relayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.removed {
		return ErrAccountRemoved
	}
	cfg, err := e.Store.Config()
	if err != nil {
		return err
	}
	resolved, err := resolveProxy(input, cfg)
	if err != nil {
		return err
	}
	st, err := e.Store.State()
	if err != nil {
		return err
	}
	cfg.ProxyMode, cfg.ProxyURL = resolved.Mode, resolved.URL
	if err = e.Store.SaveConfig(cfg, st, false); err != nil {
		return err
	}
	if e.network != nil {
		e.network.CloseIdleConnections()
	}
	return nil
}

type ProxyCheck struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	Status    int    `json:"http_status"`
	Latency   int64  `json:"latency_ms"`
	Message   string `json:"message"`
}
type ProxyTestResult struct {
	Mode     string       `json:"proxy_mode"`
	TestedAt time.Time    `json:"tested_at"`
	Checks   []ProxyCheck `json:"checks"`
}

var ErrProxyTestRunning = errors.New("连接测试正在进行，请等待本次测试完成")

func (e *Engine) TestProxy(ctx context.Context, input ProxyConfig) (ProxyTestResult, error) {
	e.mu.Lock()
	if e.removed {
		e.mu.Unlock()
		return ProxyTestResult{}, ErrAccountRemoved
	}
	cfg, err := e.Store.Config()
	e.mu.Unlock()
	if err != nil {
		return ProxyTestResult{}, err
	}
	resolved, err := resolveProxy(input, cfg)
	if err != nil {
		return ProxyTestResult{}, err
	}
	if !e.proxyTestMu.TryLock() {
		return ProxyTestResult{}, ErrProxyTestRunning
	}
	defer e.proxyTestMu.Unlock()
	now := time.Now()
	transport := proxyTransport(e.network.base, resolved.Mode, resolved.URL)
	defer transport.CloseIdleConnections()
	client := secureClient(8 * time.Second)
	client.Transport = transport
	targets := []ProxyCheck{{Name: "V2EX", URL: "https://www.v2ex.com/"}, {Name: "V2Echo 推送服务", URL: "https://app.v2echo.com/api/push/v1/health"}}
	return ProxyTestResult{Mode: resolved.Mode, TestedAt: now.UTC(), Checks: probeConnections(ctx, client, targets)}, nil
}

func probeConnections(ctx context.Context, client *http.Client, targets []ProxyCheck) []ProxyCheck {
	results := append([]ProxyCheck(nil), targets...)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			started := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, results[i].URL, nil)
			if err == nil {
				req.Header.Set("User-Agent", "V2Echo-Notifier/0.1 ProxyTest")
				req.Header.Set("Accept", "*/*")
				var response *http.Response
				response, err = client.Do(req)
				if err == nil {
					response.Body.Close()
					results[i].Connected = true
					results[i].Status = response.StatusCode
					if response.StatusCode >= 200 && response.StatusCode < 300 {
						results[i].Message = "连接成功"
					} else {
						results[i].Message = fmt.Sprintf("网络已连通，站点返回 HTTP %d", response.StatusCode)
					}
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					results[i].Message = "测试已取消或超时"
				} else if errors.Is(err, context.DeadlineExceeded) {
					results[i].Message = "连接超时，请检查代理或网络"
				} else {
					results[i].Message = "连接失败，请检查代理地址、认证、网络和证书"
				}
			}
			results[i].Latency = time.Since(started).Milliseconds()
		}(i)
	}
	wg.Wait()
	return results
}
