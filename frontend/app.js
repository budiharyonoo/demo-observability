import { trace, SpanStatusCode } from '@opentelemetry/api';
import { WebTracerProvider, BatchSpanProcessor } from '@opentelemetry/sdk-trace-web';
import { resourceFromAttributes } from '@opentelemetry/resources';
import { W3CTraceContextPropagator } from '@opentelemetry/core';
import { ZoneContextManager } from '@opentelemetry/context-zone';
import { registerInstrumentations } from '@opentelemetry/instrumentation';
import { FetchInstrumentation } from '@opentelemetry/instrumentation-fetch';
import { UserInteractionInstrumentation } from '@opentelemetry/instrumentation-user-interaction';
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';

const provider = new WebTracerProvider({
  resource: resourceFromAttributes({'service.name': 'checkout-frontend', 'service.version': '1.0.0'}),
  spanProcessors: [new BatchSpanProcessor(new OTLPTraceExporter({url: '/telemetry/v1/traces', timeoutMillis: 2000}), {
    scheduledDelayMillis: 500, maxQueueSize: 256, maxExportBatchSize: 64,
  })],
});
provider.register({contextManager: new ZoneContextManager(), propagator: new W3CTraceContextPropagator()});
registerInstrumentations({instrumentations: [
  new FetchInstrumentation({ignoreUrls: [/\/telemetry\//], clearTimingResources: true}),
  new UserInteractionInstrumentation({
    eventNames: ['click'],
    shouldPreventSpanCreation: (_event, element, span) => {
      if (element.id !== 'checkout') return true;
      span.updateName('checkout button');
      return false;
    },
  }),
]});

const form = document.querySelector('#form');
const button = document.querySelector('#checkout');
const result = document.querySelector('#result');
const traceID = document.querySelector('#trace-id');
const copy = document.querySelector('#copy');
form.addEventListener('submit', event => event.preventDefault());
button.addEventListener('click', event => {
  event.preventDefault();
  if (!form.reportValidity() || button.disabled) return;
  trace.getTracer('checkout-frontend').startActiveSpan('Checkout', checkoutSpan => {
    const browserTraceID = checkoutSpan?.spanContext().traceId;
    button.disabled = true;
    copy.disabled = true;
    copy.textContent = "Copy Trace ID";
    result.textContent = 'Checkout in progress…';
    traceID.textContent = browserTraceID || '—';
    document.querySelector('#request-id').textContent = '';
    fetch(`/checkout?scenario=${encodeURIComponent(document.querySelector('#scenario').value)}`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'X-Client-Version': '1.0.0'},
      body: JSON.stringify({user_id: 1001, product_id: Number(document.querySelector('#product').value), quantity: Number(document.querySelector('#quantity').value)}),
    }).then(response => response.json().then(body => ({response, body}))).then(({response, body}) => {
      const backendTraceID = response.headers.get('X-Trace-Id') || body.trace_id;
      traceID.textContent = backendTraceID || browserTraceID || '—';
      copy.disabled = !/^[a-f0-9]{32}$/.test(traceID.textContent);
      document.querySelector('#request-id').textContent = `Request ID: ${response.headers.get('X-Request-Id') || '—'}`;
      if (backendTraceID !== browserTraceID) throw new Error('Trace propagation failed');
      if (!response.ok) {
        checkoutSpan?.recordException(new Error(body.error || 'CHECKOUT_ERROR'));
        checkoutSpan?.setStatus({code: SpanStatusCode.ERROR, message: body.error || 'CHECKOUT_ERROR'});
      }
      result.textContent = body.success ? `Checkout successful. Order ${body.order_id}.` : `Checkout failed: ${body.error}.`;
    }).catch(error => {
      checkoutSpan?.recordException(new Error('CHECKOUT_REQUEST_FAILED'));
      checkoutSpan?.setStatus({code: SpanStatusCode.ERROR, message: 'CHECKOUT_REQUEST_FAILED'});
      result.textContent = error.message === 'Trace propagation failed' ? error.message : 'No Checkout result received. An Order may have been created. Check the Trace ID before another Checkout.';
      copy.disabled = !/^[a-f0-9]{32}$/.test(traceID.textContent);
    }).finally(() => {button.disabled = false; checkoutSpan.end();});
  });
});
copy.addEventListener('click', () => {
  navigator.clipboard.writeText(traceID.textContent).then(() => {copy.textContent = 'Trace ID copied';})
    .catch(() => {copy.textContent = 'Select and copy the Trace ID above';});
});
