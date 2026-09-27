import { test } from 'node:test';
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { mkdirSync, writeFileSync } from 'node:fs';

const compose = (query) => execFileSync('docker', ['compose', 'exec', '-T', 'demo-signoz-telemetrystore-clickhouse-0-0', 'clickhouse-client', '--query', query], {cwd: '..', encoding: 'utf8'});

test('real browser Checkout keeps click, fetch, services, MySQL, and Redis in one trace', {timeout: 60000}, async () => {
  const browser = await chromium.launch({headless: true});
  try {
    const ctx = await browser.newContext({permissions: ['clipboard-read', 'clipboard-write']});
    const page = await ctx.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(process.env.FRONTEND_URL || 'http://localhost:18000');
    const checkoutResponse = page.waitForResponse(r => r.url().includes('/checkout?'));
    const browserSpans = [];
    page.on('response', response => {
      if (response.url().endsWith('/telemetry/v1/traces') && response.status() === 200) {
        const data = response.request().postDataJSON();
        browserSpans.push(...data.resourceSpans.flatMap(r => r.scopeSpans.flatMap(s => s.spans)));
      }
    });
    await page.getByRole('button', {name: 'Checkout', exact: true}).click();
    const response = await checkoutResponse;
    const body = await response.json();
    assert.equal(response.status(), 200);
    assert.equal(body.success, true);
    const traceID = body.trace_id;
    assert.match(traceID, /^[0-9a-f]{32}$/);
    const parent = (await response.request().allHeaders()).traceparent;
    assert.equal(parent.split('-')[1], traceID);
    await page.waitForFunction(() => document.querySelector('#result').textContent.startsWith('Checkout successful'));
    assert.equal(await page.locator('#trace-id').textContent(), traceID);
    await page.getByRole('button', {name: 'Copy Trace ID'}).click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), traceID);
    for (let n = 0; n < 40; n++) {
      if (browserSpans.some(s => s.name === 'checkout button' && s.traceId === traceID) &&
          browserSpans.some(s => s.kind === 3 && s.traceId === traceID)) break;
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    assert(browserSpans.some(s => s.name === 'checkout button' && s.traceId === traceID));
    assert(browserSpans.some(s => s.kind === 3 && s.traceId === traceID));
    let spans = [];
    for (let n = 0; n < 25; n++) {
      const rows = compose(`SELECT resources_string['service.name'] AS service, name, span_id, parent_span_id, attributes_string['db.query.text'] AS query FROM signoz_traces.signoz_index_v3 WHERE trace_id='${traceID}' ORDER BY timestamp FORMAT JSONEachRow`);
      spans = rows.trim().split('\n').filter(Boolean).map(JSON.parse);
      if (spans.some(s => s.name === 'Checkout') && spans.some(s => s.query.startsWith('INSERT')) &&
          spans.every(s => !s.parent_span_id || spans.some(parent => parent.span_id === s.parent_span_id))) break;
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    assert.deepEqual([...new Set(spans.map(s => s.service))].sort(), ['cart-service', 'checkout-frontend', 'order-service', 'product-service']);
    const root = spans.find(s => s.name === 'checkout button');
    assert(root && !root.parent_span_id);
    const ids = new Set(spans.map(s => s.span_id));
    for (const span of spans) if (span !== root) assert(ids.has(span.parent_span_id), `orphan span: ${span.name}`);
    assert(spans.some(s => s.name === 'get' && s.service === 'cart-service'));
    assert(spans.some(s => s.query.startsWith('SELECT') && s.service === 'product-service'));
    assert(spans.some(s => s.query.startsWith('INSERT') && s.service === 'order-service'));
    assert.deepEqual(errors, []);
    mkdirSync('../artifacts', {recursive: true});
    writeFileSync('../artifacts/browser-trace.json', JSON.stringify({traceID, spans}, null, 2));
    await page.screenshot({path: '../artifacts/checkout.png', fullPage: true});
    console.log(`Verified browser trace ${traceID}: ${spans.length} stored spans`);
  } finally {
    await browser.close();
  }
});

test('failed browser Checkout shows a Trace ID and an error Checkout span', {timeout: 60000}, async () => {
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage();
    await page.goto(process.env.FRONTEND_URL || 'http://localhost:18000');
    await page.locator('#scenario').selectOption('out-of-stock');
    const responsePromise = page.waitForResponse(r => r.url().includes('/checkout?'));
    await page.getByRole('button', {name: 'Checkout', exact: true}).click();
    const response = await responsePromise;
    const result = await response.json();
    assert.equal(response.status(), 409);
    assert.equal(result.error, 'OUT_OF_STOCK');
    await page.waitForFunction(() => document.querySelector('#result').textContent.includes('OUT_OF_STOCK'));
    assert.equal(await page.locator('#trace-id').textContent(), result.trace_id);
    assert(await page.getByRole('button', {name: 'Copy Trace ID'}).isEnabled());
    let rows = [];
    for (let n = 0; n < 25; n++) {
      const raw = compose(`SELECT name, status_code FROM signoz_traces.signoz_index_v3 WHERE trace_id='${result.trace_id}' AND resources_string['service.name']='checkout-frontend' FORMAT JSONEachRow`);
      rows = raw.trim().split('\n').filter(Boolean).map(JSON.parse);
      if (rows.some(s => s.name === 'Checkout')) break;
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    assert(rows.some(s => s.name === 'Checkout' && s.status_code === 2));
    console.log(`Verified browser error trace ${result.trace_id}`);
  } finally {
    await browser.close();
  }
});
