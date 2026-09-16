import io
import json
import threading
import unittest
from unittest.mock import Mock, patch

import bridge


class BoundaryTests(unittest.TestCase):
    def test_failure_response_and_log_identify_stage_without_exception_secrets(self):
        raw = json.dumps({"account": "a" * 32, "binding": "b" * 64}).encode()
        request = Mock()
        request.makefile.return_value = io.BytesIO(
            b"POST /start HTTP/1.0\r\nAuthorization: Bearer synthetic-token\r\nContent-Length: "
            + str(len(raw)).encode() + b"\r\n\r\n" + raw)
        output = io.BytesIO()
        request.sendall.side_effect = output.write
        browser = Mock(stage="homepage", lock=threading.Lock())
        def fail_visit(_):
            browser.stage = "homepage"
            raise RuntimeError("A2=synthetic-secret https://user:password@proxy.example")
        browser.visit.side_effect = fail_visit
        server = Mock(token="synthetic-token", browser=browser)
        with patch("builtins.print") as log:
            bridge.Handler(request, ("127.0.0.1", 1234), server)
        headers, body = output.getvalue().split(b"\r\n\r\n", 1)
        self.assertIn(b"502", headers)
        self.assertEqual(json.loads(body), {"code": "operation_failed", "stage": "homepage"})
        self.assertIn("homepage", str(log.call_args))
        self.assertNotIn("synthetic-secret", str(log.call_args))
        self.assertNotIn("password", str(log.call_args))

    def test_document_navigation_cannot_mark_notifications_read(self):
        for url in [bridge.HOME, "https://www.v2ex.com/cdn-cgi/challenge-platform/test", "https://challenges.cloudflare.com/test"]:
            self.assertTrue(bridge.allowed_document(url))
        for url in ["https://www.v2ex.com/notifications", "https://www.v2ex.com/?next=/notifications", "https://www.v2ex.com/signout", "https://evil.example/", "file:///config", "https://www.v2ex.com.evil.example/", "http://www.v2ex.com/"]:
            self.assertFalse(bridge.allowed_document(url), url)

    def test_cookie_restore_is_domain_scoped_and_preserves_session_cookies(self):
        cookies = bridge.clean_cookies([
            {"name": "A2", "value": "synthetic", "domain": "www.v2ex.com", "expires": -1},
            {"name": "cf_clearance", "value": "synthetic", "domain": ".v2ex.com", "expires": 9999999999},
            {"name": "bad", "value": "x", "domain": "evil.example"},
            {"name": "old", "value": "x", "domain": "v2ex.com", "expires": 1},
        ])
        self.assertEqual([c["name"] for c in cookies], ["A2", "cf_clearance"])
        self.assertNotIn("expires", cookies[0])
        self.assertEqual(bridge.imported_cookies("A2=a=b; test=ok")[0]["value"], "a=b")
        with self.assertRaises(ValueError):
            bridge.imported_cookies("cf_clearance=only")

    def test_proxy_hostname_boundary(self):
        for host in ["www.v2ex.com", "challenges.cloudflare.com", "x.dnstest.dev"]:
            self.assertTrue(bridge.allowed_host(host))
        for host in ["127.0.0.1", "169.254.169.254", "cloudflare.com.evil.example", "notifier"]:
            self.assertFalse(bridge.allowed_host(host))

    def test_authenticated_https_upstream_and_no_direct_fallback(self):
        proxy = Mock(upstream="https://user:p%40ss@proxy.example:443")
        handler = bridge.TunnelHandler.__new__(bridge.TunnelHandler)
        handler.server = proxy
        handler.connection = Mock()
        handler.rfile = io.BytesIO(b"CONNECT www.v2ex.com:443 HTTP/1.1\r\nHost: www.v2ex.com:443\r\n\r\n")
        handler.wfile = io.BytesIO()
        upstream = Mock()
        # Upstream refuses authentication. It must not trigger a direct socket.
        upstream.recv.side_effect = [bytes([b]) for b in b"HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"]
        with patch.object(bridge.socket, "create_connection", return_value=Mock()) as connect, patch.object(bridge.ssl, "create_default_context") as tls:
            tls.return_value.wrap_socket.return_value = upstream
            handler.handle()
            connect.assert_called_once_with(("proxy.example", 443), 12)
            tls.return_value.wrap_socket.assert_called_once()
            self.assertEqual(tls.return_value.wrap_socket.call_args.kwargs["server_hostname"], "proxy.example")
            sent = upstream.sendall.call_args.args[0]
            self.assertIn(b"Proxy-Authorization: Basic dXNlcjpwQHNz", sent)
            self.assertIn(b"502", handler.wfile.getvalue())

    def test_old_document_is_not_returned_after_new_response_headers(self):
        browser = bridge.Browser(Mock())
        browser.ensure = Mock()
        cdp = Mock(status=0, challenged=False, response_loader="", document_loader="old")
        browser.cdp = cdp
        evaluations = 0

        def send(method, params=None):
            nonlocal evaluations
            if method == "Runtime.evaluate":
                evaluations += 1
                cdp.status = 200
                cdp.response_loader = "new"
                if evaluations == 2:
                    cdp.document_loader = "new"
                return {"result": {"value": json.dumps({"url": bridge.HOME, "ready": "complete", "html": "old" if evaluations == 1 else "new"})}}
            if method == "Network.getCookies":
                return {"cookies": []}
            return {}

        cdp.send.side_effect = send
        with patch.object(bridge.time, "sleep"):
            result = browser.visit({})
        self.assertEqual(result["html"], "new")
        self.assertEqual(evaluations, 2)


if __name__ == "__main__":
    unittest.main()
