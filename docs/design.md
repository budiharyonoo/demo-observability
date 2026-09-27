# Demo design decisions

## Fixed Stock

Each successful Checkout creates an Order. It does not reduce Stock.
The seeded Product has Stock of 10. The `out-of-stock` Scenario returns insufficient Stock without changing the stored Product.

This keeps repeated demos consistent. A requested Quantity greater than the available Stock must still fail.

## Cart values

The Checkout request defines the Product and Quantity. Cart Service validates these values, stores the Cart in Redis at `cart:<user_id>`, and reads it for Checkout.

A seeded Cart must not override the submitted values. The trace will include both the Redis write and the Redis read. Concurrent Checkouts for the same User must not exchange Cart values between the write and the read.

## Price and total amount

Price and total amount use whole Indonesian rupiah (IDR), stored as integers. A Price of `100000` and a Quantity of `2` give a total amount of `200000`.

Validate Quantity and Price before multiplication. Reject values that would exceed the integer limit.

## Telemetry export failure

Checkout does not depend on successful telemetry export. Use bounded telemetry queues and export timeouts. Write export failures to a local log without sending that diagnostic through the failed exporter.

A Collector outage must not change a successful Checkout into a failure. Telemetry can be lost when queues are full or export retries expire. Required application operations, such as Redis access and Order storage, must still report their failures.

## Repeated Checkout requests

Each successful Checkout request creates a separate Order. Disable the Checkout button while a request is in progress. Do not automatically retry `POST /checkout` or `POST /orders`.

A later click starts a new Checkout. If the connection fails after an Order is stored, the caller might not receive the result. The demo does not detect duplicate Checkout requests.

## Insufficient Stock

Checkout returns HTTP 409 with error code `OUT_OF_STOCK` when Quantity exceeds Stock. The `out-of-stock` Scenario uses this same error path. No Order is created.

Include the Trace ID in the error response and the `X-Trace-Id` response header. Record the application error and set the Checkout span status to error. Insufficient Stock is separate from an internal Product Service or Order Service failure.

## Request-body logs

Show all fields from a valid JSON request body. Recursively replace sensitive values with `[REDACTED]`, including values in nested objects and arrays. Do not apply a safe-field allowlist to the body. Keep the safe-header allowlist.

Reject oversized bodies. Do not log malformed JSON as raw text, because redaction cannot be guaranteed. Store the sanitized body in a structured log, never in a span attribute.
