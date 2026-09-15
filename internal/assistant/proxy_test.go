package assistant

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyValidationEncryptionAndCooldownPreservation(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s)
	st := State{Verified: true, AccountID: 7, Username: "tester", NextAPI: time.Now().Add(time.Hour), Failures: 3}
	s.SaveConfig(Config{APIToken: "pat", RelayToken: "sender", RelayURL: "https://relay.example", IntervalSeconds: 180}, st, false)
	s.DB.Exec("UPDATE relay_schedule SET next_sync=9999999999,failures=5")
	raw := "https://proxy-user:proxy-password@proxy.example:8443/"
	cfg := Config{ProxyMode: "custom", ProxyURL: raw, IntervalSeconds: 180, RelayURL: "https://relay.example"}
	if err := e.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	var saved string
	s.DB.QueryRow("SELECT body FROM settings").Scan(&saved)
	if strings.Contains(saved, "proxy-user") || strings.Contains(saved, "proxy-password") || strings.Contains(saved, "proxy.example") {
		t.Fatal("proxy URL not encrypted")
	}
	restored := NewEngine(s)
	defer restored.network.CloseIdleConnections()
	read, err := restored.Store.Config()
	if err != nil || read.ProxyURL != strings.TrimSuffix(raw, "/") {
		t.Fatal("proxy not restored", err)
	}
	cfg.ProxyURL = ""
	if err = e.Configure(cfg); err != nil {
		t.Fatal("blank custom update should preserve saved URL", err)
	}
	updated, _ := s.Config()
	if updated.ProxyURL != read.ProxyURL {
		t.Fatal("saved proxy changed")
	}
	next := queryInt(t, s, "SELECT next_sync FROM relay_schedule")
	after := stateOf(t, s)
	if next != 9999999999 || after.Failures != 3 || !after.NextAPI.Equal(st.NextAPI) || !after.Verified {
		t.Fatal("proxy update reset account or cooldown")
	}
	for _, raw := range []string{"socks5://proxy.example:1080", "http://proxy.example/path", "http://proxy.example?", "https://proxy.example#", "http://proxy.example:0", "http://proxy.example:65536", "http://proxy.example:", "http://user:secret\n@proxy.example", "http://:password@proxy.example", "http://user:pass%0a@proxy.example"} {
		if _, _, err := normalizeProxy("custom", raw); err == nil {
			t.Fatalf("invalid proxy accepted: %q", raw)
		}
	}
	cfg.ProxyMode = "direct"
	if err = e.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	updated, _ = s.Config()
	if updated.ProxyURL != "" {
		t.Fatal("disabled custom credentials retained")
	}
}

func TestIndependentProxySettingsPreserveAccountState(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s)
	defer e.network.CloseIdleConnections()
	st := State{Verified: true, AccountID: 7, Username: "tester", NextAPI: time.Now().Add(time.Hour), NextCheck: time.Now().Add(2 * time.Hour), Failures: 3, LastError: "waiting"}
	cfg := Config{Enabled: true, APIToken: "pat", RelayToken: "sender", RelayURL: "https://relay.example", IntervalSeconds: 180}
	if err := s.SaveConfig(cfg, st, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE relay_schedule SET next_sync=9999999999,failures=5,blocked=1"); err != nil {
		t.Fatal(err)
	}
	before := stateOf(t, s)
	if err := e.ConfigureProxy(ProxyConfig{Mode: "custom", URL: "http://user:secret@proxy.example:8080"}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Config()
	if err != nil || after.APIToken != cfg.APIToken || after.RelayToken != cfg.RelayToken || after.RelayURL != cfg.RelayURL || after.IntervalSeconds != cfg.IntervalSeconds || !after.Enabled || !reflect.DeepEqual(before, stateOf(t, s)) {
		t.Fatal("independent proxy save changed account settings or state", err)
	}
	if queryInt(t, s, "SELECT next_sync FROM relay_schedule") != 9999999999 || queryInt(t, s, "SELECT blocked FROM relay_schedule") != 1 || queryInt(t, s, "SELECT failures FROM relay_schedule") != 5 {
		t.Fatal("proxy save changed relay schedule")
	}
	if err := e.ConfigureProxy(ProxyConfig{Mode: "custom"}); err != nil {
		t.Fatal(err)
	}
	// Saving the separate connection form must not discard proxy credentials.
	if err := e.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	preserved, _ := s.Config()
	if preserved.ProxyMode != after.ProxyMode || preserved.ProxyURL != after.ProxyURL {
		t.Fatal("connection form overwrote proxy")
	}
}

func TestProxyDraftProbeDoesNotPersistOrResetBudgets(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s)
	defer e.network.CloseIdleConnections()
	if err := e.ConfigureProxy(ProxyConfig{Mode: "direct"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Config()
	stateBefore := stateOf(t, s)
	if _, err := s.DB.Exec("UPDATE relay_schedule SET next_sync=9999999999,failures=5,blocked=1"); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	e.network.base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts.Add(1)
		if address != "draft.example:8443" {
			t.Errorf("draft proxy not used: %s", address)
		}
		return nil, errors.New("synthetic failure with proxy-secret")
	}
	if _, err := e.TestProxy(context.Background(), ProxyConfig{Mode: "custom", URL: "socks5://invalid.example"}); err == nil {
		t.Fatal("invalid draft accepted")
	}
	result, err := e.TestProxy(context.Background(), ProxyConfig{Mode: "custom", URL: "https://user:proxy-secret@draft.example:8443"})
	if err != nil || len(result.Checks) != 2 || result.Mode != "custom" || attempts.Load() != 2 {
		t.Fatal("draft probe failed", err, attempts.Load())
	}
	for _, check := range result.Checks {
		if check.Connected || check.Status != 0 || strings.Contains(check.Message, "proxy-secret") {
			t.Fatal("incorrect or unsafe failure result")
		}
	}
	if _, err := e.TestProxy(context.Background(), ProxyConfig{Mode: "custom", URL: "https://user:proxy-secret@draft.example:8443"}); err != nil {
		t.Fatal("completed probe prevented immediate retry", err)
	}
	after, _ := s.Config()
	if attempts.Load() != 4 || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(stateBefore, stateOf(t, s)) || queryInt(t, s, "SELECT next_sync FROM relay_schedule") != 9999999999 || queryInt(t, s, "SELECT failures FROM relay_schedule") != 5 || queryInt(t, s, "SELECT blocked FROM relay_schedule") != 1 {
		t.Fatal("manual probe mutated account or schedule")
	}
}

func TestProxyProbeStatusRedirectAndCredentialScope(t *testing.T) {
	client := secureClient(time.Second)
	var calls atomic.Int32
	client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.Method != "GET" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("Proxy-Authorization") != "" {
			t.Error("probe carried account credentials")
		}
		status := 200
		switch req.URL.Path {
		case "/redirect":
			status = 302
		case "/forbidden":
			status = 403
		case "/timeout":
			return nil, context.DeadlineExceeded
		case "/followed":
			t.Error("probe followed redirect")
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://target.example/followed"}}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})
	checks := probeConnections(context.Background(), client, []ProxyCheck{{URL: "https://target.example/"}, {URL: "https://target.example/redirect"}, {URL: "https://target.example/forbidden"}, {URL: "https://target.example/timeout"}})
	if calls.Load() != 4 {
		t.Fatal("incorrect request count")
	}
	for i, status := range []int{200, 302, 403, 0} {
		if checks[i].Status != status || checks[i].Connected != (status > 0) {
			t.Fatalf("incorrect status for probe %d: %+v", i, checks[i])
		}
	}
	if !strings.Contains(checks[3].Message, "超时") {
		t.Fatal("timeout was not explained")
	}
}

func TestHTTPAndHTTPSProxyTunnelAllClientsWithoutLeakingCredentials(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[encrypted], func(t *testing.T) {
			var connects, originRequests atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				originRequests.Add(1)
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked to origin")
				}
				if r.URL.Path == "/api/v2/member" {
					if r.Header.Get("Authorization") != "Bearer synthetic-pat" {
						t.Error("origin authentication missing")
					}
					io.WriteString(w, `{"success":true,"result":{"id":7,"username":"tester"}}`)
				} else {
					io.WriteString(w, "ok")
				}
			}))
			defer target.Close()
			expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password"))
			proxyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "CONNECT" || r.Host != strings.TrimPrefix(target.URL, "https://") {
					t.Error("wrong proxy destination")
					w.WriteHeader(400)
					return
				}
				if r.Header.Get("Proxy-Authorization") != expectedAuth || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("CONNECT credential scope incorrect")
					w.WriteHeader(407)
					return
				}
				upstream, err := net.DialTimeout("tcp", r.Host, time.Second)
				if err != nil {
					w.WriteHeader(502)
					return
				}
				client, buffer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					upstream.Close()
					return
				}
				connects.Add(1)
				buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				buffer.Flush()
				done := make(chan struct{})
				go func() { io.Copy(upstream, buffer); upstream.Close(); close(done) }()
				io.Copy(client, upstream)
				client.Close()
				<-done
			})
			var proxy *httptest.Server
			if encrypted {
				proxy = httptest.NewTLSServer(proxyHandler)
			} else {
				proxy = httptest.NewServer(proxyHandler)
			}
			defer proxy.Close()
			s := testStore(t)
			e := NewEngine(s)
			defer e.network.CloseIdleConnections()
			roots := x509.NewCertPool()
			roots.AddCert(target.Certificate())
			if encrypted {
				roots.AddCert(proxy.Certificate())
			}
			e.network.base.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			proxyURL := strings.Replace(proxy.URL, "://", "://proxy-user:proxy-password@", 1)
			if err := e.Configure(Config{IntervalSeconds: 180, ProxyMode: "custom", ProxyURL: proxyURL}); err != nil {
				t.Fatal(err)
			}
			e.API.BaseURL = target.URL + "/api/v2/"
			var member Member
			if _, err := e.API.get(context.Background(), "synthetic-pat", "member", &member); err != nil || member.ID != 7 {
				t.Fatal("API proxy failed", err)
			}
			for _, client := range []*http.Client{e.Web, e.Relay} {
				req, _ := http.NewRequest("GET", target.URL+"/", nil)
				req.Header.Set("Cookie", "synthetic-cookie")
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
			if connects.Load() == 0 || originRequests.Load() != 3 {
				t.Fatal("clients did not use proxy")
			}
			// A refused proxy must not fall back to a reachable destination.
			blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(407) }))
			defer blocked.Close()
			if err := e.Configure(Config{IntervalSeconds: 180, ProxyMode: "custom", ProxyURL: blocked.URL}); err != nil {
				t.Fatal(err)
			}
			if res, err := e.Web.Get(target.URL); err == nil {
				res.Body.Close()
				t.Fatal("failed proxy silently bypassed")
			}
			if originRequests.Load() != 3 {
				t.Fatal("failed proxy reached origin")
			}
			// Explicit direct mode replaces the old pool and ignores the custom proxy.
			if err := e.Configure(Config{IntervalSeconds: 180, ProxyMode: "direct"}); err != nil {
				t.Fatal(err)
			}
			res, err := e.Web.Get(target.URL)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if originRequests.Load() != 4 {
				t.Fatal("direct mode did not reach origin")
			}
		})
	}
}

func TestProxyRejectsConcurrentProbeAndAllowsRetry(t *testing.T) {
	e := NewEngine(testStore(t))
	defer e.network.CloseIdleConnections()
	started := make(chan struct{}, 2)
	e.network.base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := e.TestProxy(ctx, ProxyConfig{Mode: "direct"})
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("probe did not start")
		}
	}
	if _, err := e.TestProxy(context.Background(), ProxyConfig{Mode: "direct"}); !errors.Is(err, ErrProxyTestRunning) {
		t.Fatalf("concurrent probe was not rejected: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled probe did not finish")
	}
	// A cancelled run also releases the slot immediately.
	if _, err := e.TestProxy(ctx, ProxyConfig{Mode: "direct"}); err != nil {
		t.Fatal("cancelled probe prevented retry", err)
	}
}
