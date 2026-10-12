"""Check the actual web binary's routes; bounded retry for server startup."""

import sys
import time
import urllib.error
import urllib.request

base = sys.argv[1]
for attempt in range(20):
    try:
        with urllib.request.urlopen(base + "/healthz", timeout=2) as response:
            assert response.status == 200 and response.read() == b"ok\n"
        break
    except (urllib.error.URLError, TimeoutError):
        if attempt == 19:
            raise
        time.sleep(0.25)

for route in ("/", "/sample/report.html", "/sample/matrix.csv", "/template.csv"):
    with urllib.request.urlopen(base + route, timeout=5) as response:
        assert response.status == 200
        body = response.read().decode()
        if route == "/":
            assert "$49" not in body and "synthetic" in body and "unvalidated" in body
        if route.endswith(".csv"):
            assert "synthetic" in body and "attachment" in response.headers["Content-Disposition"]
        if route == "/sample/report.html":
            assert "Not checked" in body and "data:image/png;base64," in body
for route in ("/upload", "/login", "/billing"):
    try:
        urllib.request.urlopen(base + route, timeout=2)
        raise AssertionError(f"unexpected M2 endpoint: {route}")
    except urllib.error.HTTPError as error:
        assert error.code == 404
print("Read-only non-root synthetic web routes passed; no price button or upload endpoint.")
