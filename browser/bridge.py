"""Local browser companion. Only a human solves challenges in the Selkies UI.

The controller receives homepage HTML and V2EX cookies, never API credentials.
A single ephemeral Chromium profile is active; durable cookies live encrypted
in notifier. No arbitrary URL or script is accepted by the controller API.
"""
import base64
import hmac
import http.client
import json
import os
import queue
import re
import select
import shutil
import socket
import socketserver
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOME = "https://www.v2ex.com/"
MAX_BODY = 256 * 1024
ALLOWED_HOSTS = ("v2ex.com", "cloudflare.com", "dnstest.dev")
STAGES = {"request", "reset", "profile", "launch", "devtools", "configure", "cookies", "navigate", "homepage"}
ERROR_CODES = {"operation_failed", "browser_exited", "browser_not_ready", "cdp_failed", "cdp_timeout",
               "navigation_failed", "proxy_tunnel_failed", "dns_failed", "tls_failed", "homepage_timeout"}


class BrowserFailure(RuntimeError):
    def __init__(self, code):
        self.code = code if code in ERROR_CODES else "operation_failed"
        super().__init__(self.code)


def allowed_host(host):
    return any(host == suffix or host.endswith("." + suffix) for suffix in ALLOWED_HOSTS)


def allowed_document(url):
    parsed = urllib.parse.urlsplit(url)
    return parsed.scheme == "https" and (
        (parsed.netloc == "www.v2ex.com" and parsed.path == "/" and not parsed.query)
        or (parsed.netloc == "www.v2ex.com" and parsed.path.startswith("/cdn-cgi/"))
        or parsed.netloc == "challenges.cloudflare.com"
        or parsed.netloc == "dnstest.dev" or parsed.netloc.endswith(".dnstest.dev")
    )


def clean_cookies(cookies):
    result = []
    for c in cookies or []:
        if not isinstance(c, dict) or c.get("domain", "").lstrip(".") not in ("v2ex.com", "www.v2ex.com"):
            continue
        if not isinstance(c.get("name"), str) or not isinstance(c.get("value"), str):
            continue
        item = {k: c[k] for k in ("name", "value", "domain", "path", "secure", "httpOnly", "sameSite") if k in c}
        # Session cookies must remain session cookies; CDP uses -1 in its output.
        if c.get("expires", -1) > time.time():
            item["expires"] = c["expires"]
        elif c.get("expires", -1) > 0:
            continue
        result.append(item)
    return result


def imported_cookies(raw):
    result = []
    for part in raw.split(";"):
        name, sep, value = part.strip().partition("=")
        if sep and name:
            result.append({"name": name, "value": value, "domain": "www.v2ex.com", "path": "/", "secure": True, "httpOnly": True})
    if not any(c["name"] == "A2" and c["value"] for c in result):
        raise ValueError("missing login cookie")
    return result


class TunnelProxy(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 8899), TunnelHandler)
        self.upstream = ""


class TunnelHandler(socketserver.StreamRequestHandler):
    """HTTPS CONNECT with optional authenticated HTTP(S) upstream; no fallback."""
    def handle(self):
        upstream = None
        try:
            self.connection.settimeout(15)
            line = self.rfile.readline(8193).decode("ascii").strip().split()
            if len(line) != 3 or line[0] != "CONNECT":
                raise ValueError("only HTTPS is allowed")
            target = urllib.parse.urlsplit("//" + line[1])
            if target.port != 443 or not allowed_host(target.hostname or "") or target.username:
                raise ValueError("destination not allowed")
            size = 0
            while True:
                header = self.rfile.readline(8193)
                size += len(header)
                if size > 32768 or not header:
                    raise ValueError("invalid headers")
                if header == b"\r\n":
                    break
            proxy = self.server.upstream
            if proxy:
                p = urllib.parse.urlsplit(proxy)
                if p.scheme not in ("http", "https") or not p.hostname:
                    raise ValueError("invalid proxy")
                upstream = socket.create_connection((p.hostname, p.port or (443 if p.scheme == "https" else 80)), 12)
                if p.scheme == "https":
                    upstream = ssl.create_default_context().wrap_socket(upstream, server_hostname=p.hostname)
                auth = ""
                if p.username is not None:
                    credentials = urllib.parse.unquote(p.username) + ":" + urllib.parse.unquote(p.password or "")
                    auth = "Proxy-Authorization: Basic " + base64.b64encode(credentials.encode()).decode() + "\r\n"
                upstream.sendall((f"CONNECT {line[1]} HTTP/1.1\r\nHost: {line[1]}\r\n" + auth + "\r\n").encode())
                reply = b""
                while b"\r\n\r\n" not in reply:
                    chunk = upstream.recv(1)
                    if not chunk or len(reply) > 32768:
                        raise ValueError("invalid proxy response")
                    reply += chunk
                if reply.split(b" ", 2)[1] != b"200":
                    raise ValueError("proxy refused connection")
            else:
                upstream = socket.create_connection((target.hostname, 443), 12)
            self.wfile.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            self.wfile.flush()
            self.connection.settimeout(120)
            upstream.settimeout(120)
            while True:
                # SSLSocket may have decrypted bytes not visible to select().
                ready = [upstream] if isinstance(upstream, ssl.SSLSocket) and upstream.pending() else select.select([self.connection, upstream], [], [], 120)[0]
                if not ready:
                    break
                for src in ready:
                    data = src.recv(65536)
                    if not data:
                        return
                    (upstream if src is self.connection else self.connection).sendall(data)
        except (OSError, ValueError, IndexError, UnicodeError):
            try:
                self.wfile.write(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
            except OSError:
                pass
        finally:
            if upstream:
                upstream.close()


class CDP:
    def __init__(self, url, page_target=""):
        import websocket
        self.socket = websocket.create_connection(url, timeout=2, suppress_origin=True, http_no_proxy=["127.0.0.1", "localhost"])
        self.lock = threading.Lock()
        self.pending = {}
        self.serial = 0
        self.closed = False
        self.page_target = page_target
        self.status = 0
        self.challenged = False
        self.login_required = False
        self.frame = ""
        self.document_loader = ""
        self.response_loader = ""
        threading.Thread(target=self.receive, daemon=True).start()

    def send(self, method, params=None, wait=True, session=None):
        with self.lock:
            self.serial += 1
            ident = self.serial
            answer = queue.Queue(maxsize=1)
            if wait:
                self.pending[ident] = answer
            message = {"id": ident, "method": method, "params": params or {}}
            if session:
                message["sessionId"] = session
            self.socket.send(json.dumps(message))
        if not wait:
            return {}
        try:
            try:
                result = answer.get(timeout=8)
            except queue.Empty as e:
                raise BrowserFailure("cdp_timeout") from e
            if "error" in result:
                raise BrowserFailure("cdp_failed")
            return result.get("result", {})
        finally:
            with self.lock:
                self.pending.pop(ident, None)

    def receive(self):
        import websocket
        while not self.closed:
            try:
                event = json.loads(self.socket.recv())
                if "id" in event:
                    with self.lock:
                        target = self.pending.get(event["id"])
                    if target:
                        target.put_nowait(event)
                elif event.get("method") == "Target.attachedToTarget":
                    p = event["params"]
                    info = p["targetInfo"]
                    if info["type"] == "page" and info["targetId"] != self.page_target:
                        self.send("Target.closeTarget", {"targetId": info["targetId"]}, wait=False)
                    else:
                        self.send("Runtime.runIfWaitingForDebugger", wait=False, session=p["sessionId"])
                elif event.get("method") == "Fetch.requestPaused":
                    p = event["params"]
                    destination = urllib.parse.urlsplit(p["request"]["url"])
                    if p.get("frameId") == self.frame and destination.netloc == "www.v2ex.com" and destination.path.rstrip("/") == "/signin":
                        self.login_required = True
                    # Prevent manual visits to notifications (which mark them read).
                    method = "Fetch.continueRequest" if allowed_document(p["request"]["url"]) else "Fetch.failRequest"
                    args = {"requestId": p["requestId"]}
                    if method == "Fetch.failRequest":
                        args["errorReason"] = "BlockedByClient"
                    self.send(method, args, wait=False)
                elif event.get("method") == "Page.frameNavigated":
                    frame = event["params"]["frame"]
                    if frame["id"] == self.frame:
                        self.document_loader = frame.get("loaderId", "")
                elif event.get("method") == "Network.responseReceived":
                    p = event["params"]
                    if p.get("type") == "Document" and p.get("frameId") == self.frame:
                        response = p["response"]
                        self.response_loader = p.get("loaderId", "")
                        self.status = int(response["status"])
                        headers = {k.lower(): str(v).lower() for k, v in response.get("headers", {}).items()}
                        self.challenged = headers.get("cf-mitigated") == "challenge"
            except websocket.WebSocketTimeoutException:
                continue
            except Exception:
                self.closed = True
        with self.lock:
            for target in self.pending.values():
                try:
                    target.put_nowait({"error": "closed"})
                except queue.Full:
                    pass

    def close(self):
        self.closed = True
        self.socket.close()


class Browser:
    def __init__(self, proxy):
        self.proxy = proxy
        self.account = ""
        self.binding = ""
        self.process = None
        self.profile = None
        self.cdp = None
        self.guard = None
        self.lock = threading.Lock()
        self.stage = "request"

    def reset(self):
        self.stage = "reset"
        if self.guard:
            self.guard.close()
            self.guard = None
        if self.cdp:
            self.cdp.close()
            self.cdp = None
        if self.process:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=3)
            self.process = None
        if self.profile:
            shutil.rmtree(self.profile, ignore_errors=True)
            self.profile = None
        self.account = self.binding = ""

    def ensure(self, data):
        if self.account == data["account"] and self.binding == data["binding"] and self.process and self.process.poll() is None and self.cdp and not self.cdp.closed:
            return
        self.reset()
        self.proxy.upstream = data["proxy"]
        self.stage = "profile"
        self.profile = tempfile.mkdtemp(prefix="v2echo-", dir=os.environ.get("BROWSER_TMP", "/dev/shm"))
        args = ["/usr/bin/chromium", "--no-sandbox", "--start-maximized", "--no-first-run", "--no-default-browser-check", "--password-store=basic",
                "--disable-dev-shm-usage", "--disable-quic", "--disable-background-networking",
                "--disable-features=MediaRouter", "--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
                "--proxy-server=http://127.0.0.1:8899", "--proxy-bypass-list=<-loopback>",
                "--remote-debugging-port=9222", "--remote-debugging-address=127.0.0.1",
                "--user-data-dir=" + self.profile, "--app=about:blank"]
        # The Selkies compositor provides the display; Chromium runs as abc.
        if os.environ.get("PIXELFLUX_WAYLAND", "").lower() == "true":
            args.append("--ozone-platform=wayland")
        environment = dict(os.environ)
        environment.pop("ECHO_BROWSER_TOKEN", None)
        self.stage = "launch"
        self.process = subprocess.Popen(args, env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        deadline = time.monotonic() + 8
        self.stage = "devtools"
        while time.monotonic() < deadline:
            try:
                with opener.open("http://127.0.0.1:9222/json/list", timeout=1) as res:
                    tabs = json.load(res)
                tab = next(t for t in tabs if t["type"] == "page")
                with opener.open("http://127.0.0.1:9222/json/version", timeout=1) as res:
                    version = json.load(res)
                self.guard = CDP(version["webSocketDebuggerUrl"], tab["id"])
                self.guard.send("Target.setAutoAttach", {"autoAttach": True, "waitForDebuggerOnStart": True, "flatten": True})
                self.cdp = CDP(tab["webSocketDebuggerUrl"])
                break
            except Exception:
                if self.process.poll() is not None:
                    raise BrowserFailure("browser_exited")
                time.sleep(0.1)
        if not self.cdp:
            raise BrowserFailure("browser_not_ready")
        self.stage = "configure"
        self.cdp.send("Page.enable")
        self.cdp.send("Network.enable")
        self.cdp.send("Network.setCacheDisabled", {"cacheDisabled": True})
        self.cdp.frame = self.cdp.send("Page.getFrameTree")["frameTree"]["frame"]["id"]
        self.cdp.send("Fetch.enable", {"patterns": [{"urlPattern": "*", "resourceType": "Document", "requestStage": "Request"}]})
        self.cdp.send("Page.setDownloadBehavior", {"behavior": "deny"})
        self.stage = "cookies"
        cookies = clean_cookies(data.get("cookies")) or imported_cookies(data["cookie"])
        self.cdp.send("Network.setCookies", {"cookies": cookies})
        self.account, self.binding = data["account"], data["binding"]

    def visit(self, data):
        self.ensure(data)
        cdp = self.cdp
        cdp.status = 0
        cdp.challenged = False
        cdp.login_required = False
        # A response must belong to the newly committed document, not the old page.
        cdp.response_loader = ""
        previous_loader = cdp.document_loader
        self.stage = "navigate"
        navigation = cdp.send("Page.navigate", {"url": HOME})
        if cdp.login_required:
            return {"url": HOME, "status": 302, "login_required": True}
        if navigation.get("errorText"):
            code = {"net::ERR_TUNNEL_CONNECTION_FAILED": "proxy_tunnel_failed",
                    "net::ERR_PROXY_CONNECTION_FAILED": "proxy_tunnel_failed",
                    "net::ERR_NAME_NOT_RESOLVED": "dns_failed",
                    "net::ERR_CERT_AUTHORITY_INVALID": "tls_failed",
                    "net::ERR_CERT_DATE_INVALID": "tls_failed"}.get(navigation["errorText"], "navigation_failed")
            raise BrowserFailure(code)
        self.stage = "homepage"
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            doc = cdp.send("Runtime.evaluate", {"expression": "JSON.stringify({url:location.href,ready:document.readyState,html:document.documentElement.outerHTML})", "returnByValue": True})
            value = doc.get("result", {}).get("value")
            if value and cdp.status and cdp.response_loader and cdp.response_loader == cdp.document_loader and cdp.document_loader != previous_loader:
                page = json.loads(value)
                if page["ready"] in ("interactive", "complete") and page["url"].startswith("https://"):
                    if len(page["html"].encode()) > 4 * 1024 * 1024:
                        raise ValueError("page too large")
                    return {"html": page["html"], "url": page["url"], "status": cdp.status,
                            "challenged": cdp.challenged,
                            "cookies": clean_cookies(cdp.send("Network.getCookies", {"urls": [HOME]})["cookies"])}
            time.sleep(0.15)
        raise BrowserFailure("homepage_timeout")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass  # Never log request bodies, credentials, browser HTML or URLs.

    def do_POST(self):
        expected = "Bearer " + self.server.token
        if not hmac.compare_digest(self.headers.get("Authorization", ""), expected):
            self.send_error(401)
            return
        stage = "request"
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= MAX_BODY:
                raise ValueError("invalid size")
            data = json.loads(self.rfile.read(length))
            if not re.fullmatch(r"[0-9a-f]{32}", data.get("account", "")):
                raise ValueError("invalid account")
            with self.server.browser.lock:
                self.server.browser.stage = "request"
                if self.path == "/reset":
                    if self.server.browser.account == data["account"]:
                        self.server.browser.reset()
                    result = {}
                elif self.path in ("/start", "/read", "/check"):
                    if self.path == "/check" and (self.server.browser.account != data["account"] or self.server.browser.binding != data.get("binding")):
                        raise ValueError("session changed; reopen verification")
                    if not re.fullmatch(r"[0-9a-f]{64}", data.get("binding", "")):
                        raise ValueError("invalid binding")
                    try:
                        result = self.server.browser.visit(data)
                    finally:
                        stage = self.server.browser.stage
                else:
                    self.send_error(404)
                    return
            raw = json.dumps(result).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
        except Exception as e:
            # Only controlled identifiers leave the process. Exception messages,
            # CDP payloads and Chromium output may contain credentials or HTML.
            stage = stage if stage in STAGES else "request"
            code = e.code if isinstance(e, BrowserFailure) else "operation_failed"
            print(f"[v2echo-browser] stage={stage} code={code}", flush=True)
            raw = json.dumps({"stage": stage, "code": code}).encode()
            self.send_response(502)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)


if __name__ == "__main__":
    token = os.environ.get("ECHO_BROWSER_TOKEN", "")
    if len(token) < 32:
        raise SystemExit("ECHO_BROWSER_TOKEN must contain at least 32 characters")
    proxy = TunnelProxy()
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    service = ThreadingHTTPServer(("0.0.0.0", 8090), Handler)
    service.token = token
    service.browser = Browser(proxy)
    service.serve_forever()
