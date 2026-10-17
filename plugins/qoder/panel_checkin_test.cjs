const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, 'panel.html'), 'utf8');
function sourceBetween(start, end) {
  const from = html.indexOf(start);
  const to = html.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing panel behavior: ${start}`);
  return html.slice(from, to);
}

// Run the actual panel result functions. These cases cover presentation and
// button decisions, not browser layout, clipboard support, or live APIs.
function panelFixture() {
  const calls = [];
  const context = vm.createContext({ toast: (...args) => calls.push(args) });
  vm.runInContext([
    sourceBetween('function asText(', 'function progressHTML('),
    sourceBetween('function checkinRedemptionCodes(', 'async function checkin(idx,'),
    sourceBetween('function checkinBatchToast(', 'async function checkinAll('),
  ].join('\n'), context);
  return { calls, context };
}

test('Intl empty activities stay a normal empty result', () => {
  const { calls, context } = panelFixture();
  const result = { success: true, skipped: true, reason: 'none', message: '活动列表为空。国际版活动身份来源：按账号获取的 UMID' };
  context.checkinResultToast(result);
  assert.match(calls[0][0], /今日无可签领权益/);
  assert.doesNotMatch(calls[0][0], /无需签到|不支持/);
  assert.equal(context.isCheckinDoneResult(result), false);
});

test('a message mentioning Intl cannot override a successful grant', () => {
  const { calls, context } = panelFixture();
  const result = { success: true, reward_credits: 100, message: '国际版奖励已确认' };
  context.checkinResultToast(result);
  assert.match(calls[0][0], /签到成功/);
  assert.match(calls[0][2], /\+100 积分/);
  assert.equal(context.isCheckinDoneResult(result), true);
});

test('mixed rewards display every code and stay available for copying', () => {
  const { calls, context } = panelFixture();
  context.checkinResultToast({ success: true, reward_credits: 100, redemption_codes: ['FICTIONAL-ONE', 'FICTIONAL-TWO'], redemption_code: 'FICTIONAL-ONE' });
  assert.match(calls[0][2], /\+100 积分/);
  assert.match(calls[0][2], /FICTIONAL-ONE、FICTIONAL-TWO/);
  assert.equal(calls[0][2].match(/FICTIONAL-ONE/g).length, 1);
  assert.doesNotMatch(calls[0][2], /连续 \?/);
  assert.equal(calls[0][3], true);
});

test('coupon-only and pending rewards are not presented as zero credits', () => {
  for (const result of [
    { success: true, reward_credits: 0, redemption_code: 'FICTIONAL-COUPON' },
    { success: true, reward_credits: 0, message: '兑换码发放中' },
  ]) {
    const { calls, context } = panelFixture();
    context.checkinResultToast(result);
    assert.match(calls[0][0], /奖励已领取/);
    assert.doesNotMatch(calls[0][2], /\+0 积分/);
    assert.match(calls[0][2], /FICTIONAL-COUPON|兑换码发放中/);
  }
});

test('partial grants retain errors and leave check-in available', () => {
  const { calls, context } = panelFixture();
  const result = { success: true, partial: true, reward_credits: 100, claim_errors: ['券活动：请求超时'] };
  context.checkinResultToast(result);
  assert.match(calls[0][0], /部分领取成功/);
  assert.equal(calls[0][1], 'warn');
  assert.match(calls[0][2], /\+100 积分/);
  assert.match(calls[0][2], /请求超时/);
  assert.equal(context.isCheckinDoneResult(result), false);
});

test('only structured already results mark skipped accounts done', () => {
  const { calls, context } = panelFixture();
  const result = { success: true, skipped: true, reason: 'not_eligible', message: '国际版今日名额不足，其他账号已签' };
  context.checkinResultToast(result);
  assert.match(calls[0][0], /暂不可领取/);
  assert.equal(context.isCheckinDoneResult(result), false);
  assert.equal(context.isCheckinDoneResult({ success: true, skipped: true, reason: 'already' }), true);
  assert.equal(context.isCheckinDoneResult({ error: '领取失败' }), false);
});

test('batch reports separate skipped, partial, and failed accounts with rewards', () => {
  const { calls, context } = panelFixture();
  context.checkinBatchToast({
    summary: { success: 1, partial: 1, already: 0, skipped: 1, fail: 1 },
    results: [
      { nickname: 'reward-fixture', success: true, reward_credits: 100, redemption_codes: ['FICTIONAL-CODE'] },
      { nickname: 'partial-fixture', success: true, partial: true, claim_errors: ['请求超时'] },
      { nickname: 'empty-fixture', skipped: true, reason: 'none', message: '今日无活动' },
      { nickname: 'failed-fixture', error: '活动身份获取失败' },
    ],
  });
  assert.match(calls[0][0], /1 成功 \/ 1 部分成功 \/ 1 跳过 \/ 1 失败/);
  assert.doesNotMatch(calls[0][0], /已签|无需签到/);
  assert.match(calls[0][2], /FICTIONAL-CODE/);
  assert.match(calls[0][2], /请求超时/);
  assert.match(calls[0][2], /活动身份获取失败/);
  assert.equal(calls[0][3], true);
});

test('batch marks only completed account buttons across both regions', async () => {
  const { context } = panelFixture();
  const marked = [];
  const cards = ['intl-done', 'cn-skipped', 'cn-partial'].map(id => ({
    dataset: { auth: id, region: id.startsWith('intl') ? 'intl' : 'cn' },
    querySelector: () => id,
  }));
  context.document = { querySelectorAll: () => cards };
  context.markCheckinDone = button => marked.push(button);
  context.busy = () => {};
  context.load = async () => {};
  context.setTimeout = callback => { callback(); return 0; };
  context.api = async () => ({
    summary: { success: 1, partial: 1, already: 0, skipped: 1, fail: 0 },
    results: [
      { auth_index: 'intl-done', success: true, reward_credits: 100 },
      { auth_index: 'cn-skipped', success: true, skipped: true, reason: 'none' },
      { auth_index: 'cn-partial', success: true, partial: true },
    ],
  });
  vm.runInContext(sourceBetween('async function checkinAll(', 'async function toggleAuto('), context);
  await context.checkinAll(null);
  assert.deepEqual(marked, ['intl-done']);
});
