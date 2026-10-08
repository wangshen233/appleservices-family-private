"""Direct HTTPS transport for the purchase skill; payloads stay on private pipes."""
import base64
import json
import re
import sys
from urllib.parse import urlsplit

import requests


def main():
    message = json.load(sys.stdin)
    endpoint = urlsplit(message["url"])
    allowed = endpoint.hostname in {
        "auth.itunes.apple.com", "buy.itunes.apple.com", "speedysub.itunes.apple.com"
    } or re.fullmatch(r"p[0-9]+-buy\.itunes\.apple\.com", endpoint.hostname or "")
    if endpoint.scheme != "https" or not allowed or endpoint.username or endpoint.fragment:
        raise ValueError("unsupported endpoint")
    headers = message["headers"]
    headers.pop("Content-Length", None)
    headers.pop("Accept-Encoding", None)
    session = requests.Session()
    session.trust_env = False
    with session.request(
        message["method"], message["url"], headers=headers,
        data=base64.b64decode(message["body"] or ""), allow_redirects=False,
        timeout=20, stream=True,
    ) as response:
        parts = []
        length = 0
        for part in response.iter_content(16384):
            length += len(part)
            if length > 2 * 1024 * 1024:
                raise ValueError("response too large")
            parts.append(part)
        result_headers = {
            name: response.raw.headers.getlist(name) for name in response.raw.headers
            if name.lower() not in {"content-length", "content-encoding", "transfer-encoding"}
        }
        json.dump({
            "status": response.status_code, "headers": result_headers,
            "body": base64.b64encode(b"".join(parts)).decode("ascii"),
        }, sys.stdout)
    session.close()


if __name__ == "__main__":
    try:
        main()
    except Exception:
        print('{"error":"direct-https-transport-failed"}')
        sys.exit(1)
