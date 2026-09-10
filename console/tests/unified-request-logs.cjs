const assert = require('node:assert/strict');
const path = require('node:path');

module.exports = async function verifyRequestLogs(context, page, base) {
  const envelope = data => ({ code: 0, message: 'success', data });
  const call = { id: 7, public_id: 'qa-async-video-call', status: 'in_progress', quoted_amount: '1.00', price_currency: 'CNY', delivery_mode: 'reference', created_at: '2026-09-06T10:20:00Z', catalog_release_id: 1, sku_id: 1 };
  let fail = false;
  await context.route('**/api/admin/unified-gateway/calls**', async route => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/requests')) {
      if (fail) return route.fulfill({ status: 500, json: { code: 500, message: '请求日志读取失败' } });
      const page = Number(url.searchParams.get('page'));
      const pageSize = Number(url.searchParams.get('page_size'));
      const items = Array.from({ length: 53 }, (_, i) => ({ id: 53 - i, request_seq: 53 - i, attempt_no: 1, action: 'query', status: i === 0 ? 'unknown' : 'response_recorded', http_status: i === 0 ? null : 200, duration_ms: i === 0 ? 30000 : 187, error_code: i === 0 ? 'provider_exchange_unknown' : '', request_complete: i !== 0, response_complete: i !== 0, created_at: '2026-09-06T10:22:00Z', completed_at: null, async_execution_id: 1, async_state: 'manual_review' }));
      return route.fulfill({ json: envelope({ items: items.slice((page - 1) * pageSize, page * pageSize), total: items.length, page, page_size: pageSize }) });
    }
    if (url.pathname.endsWith('/7')) return route.fulfill({ json: envelope({ call, request_logs_available: true, attempts: { items: [], total: 0, page: 1, page_size: 20 } }) });
    return route.fulfill({ json: envelope({ items: [call], total: 1, page: 1, page_size: 20 }) });
  });
  await page.getByRole('tab', { name: '调用记录', exact: true }).click();
  await page.getByRole('button', { name: call.public_id, exact: true }).click();
  const drawer = page.getByRole('dialog');
  await drawer.getByRole('tab', { name: '上游请求', exact: true }).click();
  await drawer.getByText('查询 #53', { exact: true }).waitFor();
  assert.equal(await drawer.getByText('provider_exchange_unknown', { exact: true }).count(), 1);
  await page.screenshot({ path: path.resolve('../.temp/qa_async_requests_desktop_20260906.png') });
  await drawer.getByRole('button', { name: '下一页', exact: true }).click();
  await drawer.getByText('查询 #33', { exact: true }).waitFor();
  await drawer.getByRole('button', { name: '上一页', exact: true }).click();
  await drawer.getByText('查询 #53', { exact: true }).waitFor();
  fail = true;
  await drawer.getByRole('button', { name: '刷新请求', exact: true }).click();
  await drawer.getByText('请求日志读取失败', { exact: true }).waitFor();
  fail = false;
  await drawer.getByRole('button', { name: '重试', exact: true }).click();
  await drawer.getByText('查询 #53', { exact: true }).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'request log drawer fits mobile viewport');
  const pagination = await drawer.getByRole('navigation', { name: '分页', exact: true }).boundingBox();
  assert(pagination && pagination.y + pagination.height <= 845, 'request pagination remains visible at the bottom');
  await page.screenshot({ path: path.resolve('../.temp/qa_async_requests_mobile_20260906.png') });
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });
  const denied = await context.request.get(base + '/api/admin/unified-gateway/calls/7/requests');
  assert.equal(denied.status(), 401, 'request logs require administrator authentication');
  await context.unroute('**/api/admin/unified-gateway/calls**');
};
