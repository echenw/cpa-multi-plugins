const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, 'panel.html'), 'utf8');
const start = html.indexOf('let loginRegion="intl";');
const end = html.indexOf('/* ===== 模型禁用', start);
assert.ok(start >= 0 && end > start, 'missing panel login behavior');
const loginSource = html.slice(start, end);

const intlURL = 'https://qoder.com/device/selectAccounts?challenge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
  + '&challenge_method=S256&client_id=1c5e33e1-364d-4ce6-b02c-acaa81274a5c'
  + '&machine_id=00000000-0000-4000-8000-000000000002&nonce=00000000-0000-4000-8000-000000000001'
  + '&redirect_uri=qoder-work%3A%2F%2F';
// The host's htmlsanitize.JSONBody HTML-escapes string values after the
// plugin management handler returns. Keep that representation in the fixture.
const escapedIntlURL = 'https://qoder.com/device/selectAccounts?challenge=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
  + '&amp;challenge_method=S256&amp;client_id=1c5e33e1-364d-4ce6-b02c-acaa81274a5c'
  + '&amp;machine_id=00000000-0000-4000-8000-000000000002&amp;nonce=00000000-0000-4000-8000-000000000001'
  + '&amp;redirect_uri=qoder-work%3A%2F%2F';
const cnURL = intlURL.replace('qoder.com/', 'qoder.com.cn/').replace('qoder-work%', 'qoder-work-cn%');
const escapedCNURL = escapedIntlURL.replace('qoder.com/', 'qoder.com.cn/').replace('qoder-work%', 'qoder-work-cn%');

// Exercise the actual start/copy/open handlers with an upstream management
// response. This checks URL handoff, not browser permissions or live OAuth.
function panelFixture(region, responseURL, fallback) {
  const copied = [];
  const opened = [];
  let selected = false;
  const elements = {
    loginAuthURL: { value: '', select: () => { selected = true; } },
    loginStatus: { textContent: '' },
  };
  const context = vm.createContext({
    document: {
      getElementById: id => elements[id],
      querySelectorAll: () => [],
      execCommand: command => {
        assert.equal(command, 'copy');
        assert.equal(selected, true);
        copied.push(elements.loginAuthURL.value);
        return true;
      },
    },
    navigator: fallback ? {} : { clipboard: { writeText: async value => { copied.push(value); } } },
    window: { open: (...args) => opened.push(args) },
    api: async (route, options) => {
      assert.equal(route, '/login/start');
      assert.equal(options.method, 'POST');
      assert.equal(JSON.parse(options.body).region, region);
      return { auth_url: responseURL, state: 'qw-fixture', expires_at: '2099-01-01T00:00:00Z', region };
    },
    busy: () => {},
    toast: () => {},
    regionLabel: value => value,
    setInterval: () => 1,
    clearInterval: () => {},
  });
  vm.runInContext(loginSource, context);
  context.pickLoginRegion(region);
  return { context, elements, copied, opened };
}

for (const scenario of [
  { name: 'Intl host-escaped link', region: 'intl', responseURL: escapedIntlURL, expectedURL: intlURL },
  { name: 'Intl plain link', region: 'intl', responseURL: intlURL, expectedURL: intlURL },
  { name: 'Intl host-escaped link with clipboard fallback', region: 'intl', responseURL: escapedIntlURL, expectedURL: intlURL, fallback: true },
  { name: 'CN host-escaped link', region: 'cn', responseURL: escapedCNURL, expectedURL: cnURL },
]) {
  test(`${scenario.name} preserves every authorization parameter when copied or opened`, async () => {
    const { context, elements, copied, opened } = panelFixture(scenario.region, scenario.responseURL, scenario.fallback);
    await context.startLoginModal(null);
    assert.equal(elements.loginAuthURL.value, scenario.expectedURL);
    await context.copyLoginURL(null);
    context.openLoginURL();
    assert.deepEqual(copied, [scenario.expectedURL]);
    assert.deepEqual(opened, [[scenario.expectedURL, '_blank', 'noopener']]);
    const params = new URL(copied[0]).searchParams;
    assert.deepEqual([...params.keys()], ['challenge', 'challenge_method', 'client_id', 'machine_id', 'nonce', 'redirect_uri']);
    assert.equal(params.get('client_id'), '1c5e33e1-364d-4ce6-b02c-acaa81274a5c');
    assert.equal(params.get('nonce'), '00000000-0000-4000-8000-000000000001');
    assert.equal(params.get('redirect_uri'), scenario.region === 'cn' ? 'qoder-work-cn://' : 'qoder-work://');
  });
}
