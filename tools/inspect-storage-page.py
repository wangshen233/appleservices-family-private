"""Read-only CDP diagnostics for the official iCloud storage WebView.

Never emit cookies, credentials, full URLs, request bodies, or account IDs.
The optional reload only reloads the current page; it does not select a plan.
"""
import argparse
import json
import re
import time
import urllib.request
from urllib.parse import urlsplit, parse_qsl

import websocket


def url_meta(value):
    u = urlsplit(value)
    return {
        "host": u.hostname,
        "path": re.sub(r"[0-9]{5,}|[0-9a-fA-F-]{24,}", "[id]", u.path),
        "query_fields": [k for k, _ in parse_qsl(u.query, keep_blank_values=True)],
    }


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--port", type=int, default=18745)
    p.add_argument("--reload", action="store_true")
    args = p.parse_args()
    targets = json.load(urllib.request.urlopen(f"http://127.0.0.1:{args.port}/json/list", timeout=5))
    target = next(t for t in targets if t.get("type") == "page" and urlsplit(t["url"]).hostname == "buy.itunes.apple.com")
    ws = websocket.create_connection(target["webSocketDebuggerUrl"], timeout=2, suppress_origin=True)
    seq = 0
    events = []

    def call(method, params=None):
        nonlocal seq
        seq += 1
        ws.send(json.dumps({"id": seq, "method": method, "params": params or {}}))
        while True:
            data = json.loads(ws.recv())
            if data.get("id") == seq:
                if "error" in data:
                    raise RuntimeError(data["error"].get("message", "CDP error"))
                return data.get("result", {})
            events.append(data)

    try:
        call("Network.enable")
        call("Runtime.enable")
        call("Page.enable")
        print(json.dumps({"page": url_meta(target["url"])}, ensure_ascii=False))
        fields = [(k, {"length": len(v), "empty": not bool(v), "unexpanded": bool(re.search(r"\$[a-zA-Z_]+\$", v))}) for k,v in parse_qsl(urlsplit(target["url"]).query, keep_blank_values=True)]
        print(json.dumps({"query_shape": fields}))
        state = call("Runtime.evaluate", {"expression": "JSON.stringify({ready:document.readyState,title:document.title.includes('buy.itunes.apple.com')?'commerce':document.title.includes('无法')?'error':'other',http400:document.body.innerText.includes('HTTP ERROR 400'),webview:typeof chrome.webview,hostObjects:typeof chrome.webview?.hostObjects})", "returnByValue": True})
        print(json.dumps({"document": json.loads(state["result"].get("value", "{}"))}))
        if args.reload:
            call("Page.reload", {"ignoreCache": True})
            deadline = time.monotonic() + 18
            while time.monotonic() < deadline:
                try:
                    events.append(json.loads(ws.recv()))
                except websocket.WebSocketTimeoutException:
                    pass
                except websocket.WebSocketConnectionClosedException:
                    print(json.dumps({"debugger_closed": True}))
                    break
        for event in events:
            method, data = event.get("method"), event.get("params", {})
            if method == "Network.requestWillBeSent":
                req = data["request"]
                print(json.dumps({"request": url_meta(req["url"]), "method": req["method"], "type": data.get("type"), "header_names": sorted(req.get("headers", {})), "body_length": len(req.get("postData", ""))}))
            elif method == "Network.responseReceived":
                resp = data["response"]
                print(json.dumps({"response": url_meta(resp["url"]), "status": resp["status"], "mime": resp["mimeType"], "header_names": sorted(resp.get("headers", {}))}))
                if resp["status"] >= 400:
                    try:
                        body = call("Network.getResponseBody", {"requestId": data["requestId"]}).get("body", "")
                    except (websocket.WebSocketException, RuntimeError):
                        print(json.dumps({"error_body_unavailable": True}))
                        continue
                    print(json.dumps({"error_body": {"length": len(body), "empty": not body, "html": "<html" in body.lower(), "known_markers": [x for x in ["Bad Request", "Unauthorized", "invalid", "token", "storefront", "terms", "DSID"] if x in body]}}))
            elif method == "Network.loadingFailed":
                print(json.dumps({"load_failed": {k:data.get(k) for k in ["type", "errorText", "blockedReason", "canceled"]}}))
    finally:
        ws.close()


if __name__ == "__main__":
    main()
