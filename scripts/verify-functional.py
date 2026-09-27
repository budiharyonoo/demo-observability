#!/usr/bin/env python3
"""Run HTTP checks against the real Compose application."""
import json
import os
import urllib.error
import urllib.request

BASE = os.environ.get("DEMO_URL", "http://localhost:18080")


def checkout(body):
    req = urllib.request.Request(BASE + "/checkout", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, json.load(response), response.headers


def main():
    body = {"user_id": 1001, "product_id": 10, "quantity": 2}
    status, first, _ = checkout(body)
    assert status == 200 and first["success"], (status, first)
    status, second, _ = checkout(body)
    assert status == 200 and second["order_id"] != first["order_id"], second
    for field in ["user_id", "product_id", "quantity"]:
        status, result, _ = checkout({**body, field: 0})
        assert status == 400 and not result["success"], (field, status, result)
    status, result, _ = checkout({**body, "quantity": 11})
    assert status == 409 and result["error"] == "OUT_OF_STOCK", (status, result)
    print("PASS: Checkout, separate Orders, input validation, and insufficient Stock")


if __name__ == "__main__":
    main()
