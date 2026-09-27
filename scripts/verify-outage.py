#!/usr/bin/env python3
"""Stop only the demo Collector, check Checkout, and restore the Collector."""
import json
import subprocess
import time
import urllib.request


def main():
    subprocess.run(["./scripts/wait-ready.sh"], check=True)
    try:
        subprocess.run(["docker", "compose", "stop", "otel-collector"], check=True)
        start = time.monotonic()
        request = urllib.request.Request("http://localhost:18080/checkout",
            data=b'{"user_id":1001,"product_id":10,"quantity":2}',
            headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=10) as response:
            result = json.load(response)
            assert response.status == 200 and result["success"], result
        print(f"PASS: Collector stopped, Checkout succeeded in {time.monotonic() - start:.2f}s")
        # Let an asynchronous export attempt run while the Collector is stopped.
        time.sleep(4)
    finally:
        subprocess.run(["docker", "compose", "start", "otel-collector"], check=True)
        subprocess.run(["./scripts/wait-ready.sh"], check=True)


if __name__ == "__main__":
    main()
