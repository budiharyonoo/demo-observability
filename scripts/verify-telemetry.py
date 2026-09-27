#!/usr/bin/env python3
"""Check real HTTP results and telemetry stored by the local SigNoz stack.

The applications send OTLP only. These read-only database queries are test probes.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request

BASE = os.environ.get("DEMO_URL", "http://localhost:18080")
CASES = {
    "normal": (200, None),
    "slow-product": (200, None),
    "slow-db": (200, None),
    "redis-slow": (200, None),
    "product-error": (502, "PRODUCT_ERROR"),
    "out-of-stock": (409, "OUT_OF_STOCK"),
    "order-error": (502, "ORDER_ERROR"),
}


def clickhouse(query):
    result = subprocess.check_output([
        "docker", "compose", "exec", "-T", "demo-signoz-telemetrystore-clickhouse-0-0",
        "clickhouse-client", "--query", query + " FORMAT JSONEachRow"], text=True)
    return [json.loads(line) for line in result.splitlines() if line]


def orders():
    return int(subprocess.check_output([
        "docker", "compose", "exec", "-T", "mysql", "mysql", "-udemo", "-pdemo-local",
        "-N", "checkout", "-e", "SELECT COUNT(*) FROM orders"], text=True, stderr=subprocess.DEVNULL))


def checkout(scenario, body=None):
    body = body or {"user_id": 1001, "product_id": 10, "quantity": 2, "price": 1}
    request_id = "verify-" + scenario
    req = urllib.request.Request(BASE + "/checkout?scenario=" + scenario,
        data=json.dumps(body).encode(), headers={"Content-Type": "application/json",
        "X-Request-Id": request_id, "Authorization": "DO_NOT_LOG_AUTHORIZATION", "Cookie": "DO_NOT_LOG_COOKIE"})
    start = time.monotonic()
    try:
        response = urllib.request.urlopen(req, timeout=12)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        result = json.load(response)
        assert result["trace_id"] == response.headers["X-Trace-Id"]
        assert response.headers["X-Request-Id"] == request_id
        assert re.fullmatch("[0-9a-f]{32}", result["trace_id"])
        return {"scenario": scenario, "status": response.status, "result": result,
                "elapsed_ms": round((time.monotonic() - start) * 1000), "request_id": request_id}


def telemetry(trace_id, service_count):
    for _ in range(35):
        spans = clickhouse("SELECT resources_string['service.name'] AS service, name, span_id, "
            "parent_span_id, duration_nano / 1000000 AS duration_ms, status_code, "
            "attributes_string AS attributes, attributes_bool AS booleans "
            "FROM signoz_traces.signoz_index_v3 WHERE trace_id='" + trace_id + "' ORDER BY timestamp")
        logs = clickhouse("SELECT resources_string['service.name'] AS service, trace_id, span_id, "
            "severity_text, attributes_string AS strings, attributes_number AS numbers "
            "FROM signoz_logs.logs_v2 WHERE trace_id='" + trace_id + "'")
        if len(logs) >= service_count and len({s["service"] for s in spans}) >= service_count:
            return spans, logs
        time.sleep(1)
    raise AssertionError("Incomplete telemetry for " + trace_id)


def verify(record, spans, logs):
    scenario = record["scenario"]
    by_id = {s["span_id"]: s for s in spans}
    cart = next(s for s in spans if s["service"] == "cart-service" and s["name"] == "POST /checkout")
    assert not cart["parent_span_id"], cart
    for span in spans:
        if span is not cart:
            assert span["parent_span_id"] in by_id, ("orphan span", span)
        assert "request.body" not in span["attributes"]
    assert not any(marker in json.dumps([spans, logs]) for marker in ["DO_NOT_LOG", "unhandled:"])
    for log in logs:
        assert log["span_id"] in by_id
        assert log["trace_id"] == record["result"]["trace_id"]
        for key in ["trace_id", "span_id", "request_id", "http.method", "http.route"]:
            assert log["strings"][key], ("missing log field", key)
        assert log["numbers"]["duration_ms"] >= 0
        assert log["numbers"]["http.status_code"] >= 200
    assert len({log["strings"]["request_id"] for log in logs}) == len(logs)
    cart_log = next(log for log in logs if log["service"] == "cart-service")
    assert cart_log["strings"]["request_id"] == record["request_id"]
    if record["status"] >= 400:
        assert cart["status_code"] == 2
        assert cart_log["strings"]["error.type"] == record["result"]["error"]
    if scenario in {"normal", "slow-product", "slow-db", "redis-slow"}:
        assert any(s["name"] == "get" and s["service"] == "cart-service" for s in spans)
        assert any(s["attributes"].get("db.query.text", "").startswith("SELECT") and s["service"] == "product-service" for s in spans)
        assert any(s["attributes"].get("db.query.text", "").startswith("INSERT") and s["service"] == "order-service" for s in spans)
    if scenario == "slow-product":
        assert any(s["service"] == "product-service" and s["name"].startswith("GET") and s["duration_ms"] >= 1400 for s in spans)
    if scenario == "slow-db":
        assert any(s["service"] == "order-service" and s["attributes"].get("db.query.text") == "SELECT SLEEP(1.5)" and s["duration_ms"] >= 1400 for s in spans)
    if scenario == "redis-slow":
        assert any(s["name"] == "get" and s["duration_ms"] >= 1400 and s["booleans"].get("demo.delay.injected") for s in spans)
    if scenario == "product-error":
        assert any(s["service"] == "product-service" and s["status_code"] == 2 for s in spans)
    if scenario in {"product-error", "out-of-stock"}:
        assert not any(s["service"] == "order-service" for s in spans)
    if scenario == "order-error":
        assert any(s["service"] == "order-service" and s["attributes"].get("db.query.text", "").startswith("INSERT") and s["status_code"] == 2 for s in spans)


def main():
    records = []
    for scenario, (status, error) in CASES.items():
        before = orders()
        record = checkout(scenario)
        assert record["status"] == status, record
        assert record["result"].get("error") == error, record
        assert orders() - before == int(status == 200), ("wrong Order count", record)
        if status == 200:
            order_id = int(record["result"]["order_id"])
            values = subprocess.check_output([
                "docker", "compose", "exec", "-T", "mysql", "mysql", "-udemo", "-pdemo-local",
                "-N", "checkout", "-e", f"SELECT o.price, o.total_amount, p.stock FROM orders o JOIN products p ON p.id=o.product_id WHERE o.id={order_id}"],
                text=True, stderr=subprocess.DEVNULL).split()
            assert values == ["100000", "200000", "10"], ("Price must come from Product and Stock must stay fixed", values)
        records.append(record)
    for record in records:
        count = 2 if record["scenario"] in {"product-error", "out-of-stock"} else 3
        spans, logs = telemetry(record["result"]["trace_id"], count)
        verify(record, spans, logs)
        record.update(spans=spans, logs=logs)
        print(f"PASS {record['scenario']}: HTTP {record['status']}, {record['elapsed_ms']} ms, trace {record['result']['trace_id']}")
    private = checkout("normal", {"user_id": 1001, "product_id": 10, "quantity": 2,
        "note": "keep all non-sensitive fields", "nested": [{field: "DO_NOT_LOG_BODY" for field in
        ["password", "token", "access_token", "refresh_token", "authorization", "api_key", "credit_card", "card_number", "cvv", "cookie", "session"]}]})
    spans, logs = telemetry(private["result"]["trace_id"], 3)
    verify(private, spans, logs)
    body = json.loads(next(l for l in logs if l["service"] == "cart-service")["strings"]["request.body"])
    assert body["note"] == "keep all non-sensitive fields"
    assert all(value == "[REDACTED]" for value in body["nested"][0].values())
    invalid = checkout("unknown")
    assert invalid["status"] == 400 and invalid["result"]["error"] == "INVALID_SCENARIO"
    metric_rows = clickhouse("SELECT DISTINCT metric_name, attrs FROM signoz_metrics.time_series_v4 "
        "WHERE startsWith(metric_name, 'demo.')")
    # Series metadata uses time buckets. Use samples to check recent ingestion.
    sample_rows = clickhouse("SELECT DISTINCT metric_name FROM signoz_metrics.samples_v4 "
        "WHERE startsWith(metric_name, 'demo.') AND unix_milli > (toUnixTimestamp(now()) - 300) * 1000")
    names = {row["metric_name"] for row in sample_rows}
    assert {"demo.http.requests", "demo.http.errors", "demo.http.duration.count",
            "demo.checkout.count", "demo.checkout.success", "demo.checkout.failure"} <= names, names
    allowed = {"http.request.method", "http.route", "http.response.status_code", "le", "__temporality__"}
    for row in metric_rows:
        assert set(row["attrs"]) <= allowed, row
    print("PASS: six metric families with bounded labels; Product Price and fixed Stock")
    Path("artifacts").mkdir(exist_ok=True)
    Path("artifacts/scenarios.json").write_text(json.dumps(records, indent=2))
    print("PASS: all Scenarios, Order counts, span parents, log correlation, body redaction, and header exclusion")


if __name__ == "__main__":
    main()
