const assert = require('node:assert/strict');
const path = require('node:path');

module.exports = async function verifyRequestLogs(context, page, base) {
  const envelope = data => ({ code: 0, message: 'success', data });
  const call = {
    id: 'qa-async-video-call',
    request_id: 'qa-async-video-request',
    user_id: 1,
    token_id: 1,
    endpoint: '/v1/videos/generations',
    operation: 'video.generate',
    model: 'seedance-2.0-480p',
    status: 'in_progress',
    is_stream: false,
    background: true,
    store: false,
    resource_type: 'video',
    resource_id: 'qa-video-1',
    conversation_id: 0,
    final_attempt_id: 0,
    attempt_count: 1,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    cached_input_tokens: 0,
    reasoning_output_tokens: 0,
    usage: null,
    reserved_amount: '1.00',
    final_cost: '0.00',
    refunded_amount: '0.00',
    http_status: 202,
    error_type: '',
    error_code: '',
    error_message: '',
    error_retryable: false,
    started_at: '2026-09-06T10:20:00Z',
    first_byte_at: null,
    completed_at: null,
    duration_ms: 30000,
    ttft_ms: 0,
    client_disconnected: false,
    created_at: '2026-09-06T10:20:00Z',
    updated_at: '2026-09-06T10:20:00Z',
  };
  let fail = false;
  await context.route('**/api/calls**', async route => {
    if (!route.request().headers().authorization) return route.continue();
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/qa-async-video-call')) {
      return route.fulfill({ json: envelope({ call, gateway_call_id: 7, attempts: [], billing_logs: [], payloads: [] }) });
    }
    return route.fulfill({ json: envelope({ items: [call], total: 1, page: 1, page_size: 20, snapshot_at: '2026-09-06T10:20:00Z' }) });
  });
  await context.route('**/api/admin/unified-gateway/calls**', async route => {
    if (!route.request().headers().authorization) return route.continue();
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/requests')) {
      if (fail) return route.fulfill({ status: 500, json: { code: 500, message: '请求日志读取失败' } });
      const pageNo = Number(url.searchParams.get('page'));
      const pageSize = Number(url.searchParams.get('page_size'));
      const items = Array.from({ length: 53 }, (_, i) => ({
        id: 53 - i,
        request_seq: 53 - i,
        attempt_no: 1,
        action: 'query',
        status: i === 0 ? 'unknown' : 'response_recorded',
        http_status: i === 0 ? null : 200,
        duration_ms: i === 0 ? 30000 : 187,
        error_code: i === 0 ? 'provider_exchange_unknown' : '',
        request_complete: i !== 0,
        response_complete: i !== 0,
        request_payload_available: false,
        response_payload_available: false,
        created_at: '2026-09-06T10:22:00Z',
        completed_at: null,
        async_execution_id: 1,
        async_state: 'manual_review',
      }));
      return route.fulfill({ json: envelope({ items: items.slice((pageNo - 1) * pageSize, pageNo * pageSize), total: items.length, page: pageNo, page_size: pageSize }) });
    }
    return route.continue();
  });
  try {
    await page.goto(base + '/#/calls');
    await page.getByText(call.request_id, { exact: true }).waitFor();
    await page.locator('tbody tr').filter({ hasText: call.request_id }).click();
    const drawer = page.getByRole('dialog', { name: '调用详情' });
    await drawer.getByRole('button', { name: '上游 HTTP', exact: true }).click();
    const region = drawer.getByRole('region', { name: '上游请求', exact: true });
    await region.getByText('查询 #53', { exact: true }).waitFor();
    assert.equal(await region.getByText('provider_exchange_unknown', { exact: true }).count(), 1);
    await page.screenshot({ path: path.resolve('../.temp/qa_async_requests_desktop_20260906.png') });
    await region.getByRole('button', { name: '下一页', exact: true }).click();
    await region.getByText('查询 #33', { exact: true }).waitFor();
    await region.getByRole('button', { name: '上一页', exact: true }).click();
    await region.getByText('查询 #53', { exact: true }).waitFor();
    fail = true;
    await region.getByRole('button', { name: '刷新请求', exact: true }).click();
    await region.getByText('请求日志读取失败', { exact: true }).waitFor();
    fail = false;
    await region.getByRole('button', { name: '重试', exact: true }).click();
    await region.getByText('查询 #53', { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'request log drawer fits mobile viewport');
    const pagination = await region.getByRole('navigation', { name: '分页', exact: true }).boundingBox();
    assert(pagination && pagination.y + pagination.height <= 845, 'request pagination remains visible at the bottom');
    await page.screenshot({ path: path.resolve('../.temp/qa_async_requests_mobile_20260906.png') });
    await page.keyboard.press('Escape');
    await drawer.waitFor({ state: 'hidden' });
    const denied = await context.request.get(base + '/api/admin/unified-gateway/calls/7/requests');
    assert.equal(denied.status(), 401, 'request logs require administrator authentication');
  } finally {
    await context.unroute('**/api/calls**');
    await context.unroute('**/api/admin/unified-gateway/calls**');
  }
};
