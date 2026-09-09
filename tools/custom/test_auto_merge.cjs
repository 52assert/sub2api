const { readFileSync } = require('node:fs');
const { resolve } = require('node:path');
const { test } = require('node:test');
const assert = require('node:assert/strict');

// Execute the actual inline workflow script with an isolated GitHub API stub.
const workflow = readFileSync(resolve(__dirname, '../../.github/workflows/fork-ci.yml'), 'utf8');
const source = workflow.split('\n  auto-merge-upstream:\n')[1].split('\n  image:\n')[0]
  .split('          script: |\n')[1].replace(/^            /gm, '');
const run = new (Object.getPrototypeOf(async function () {}).constructor)(
  'github', 'context', 'core', 'process', source);

async function exercise(change = {}, mergeResult = { merged: true, sha: 'merged-sha' }) {
  const calls = [];
  const failures = [];
  const pr = {
    state: 'open', draft: false,
    head: { repo: { full_name: '52assert/sub2api' }, ref: 'main', sha: 'tested-head' },
    base: { ref: 'custom', sha: 'tested-base' },
    ...change,
  };
  const github = { rest: {
    pulls: {
      get: async () => ({ data: pr }),
      merge: async (args) => { calls.push(['merge', args]); return { data: mergeResult }; },
    },
    actions: { createWorkflowDispatch: async (args) => calls.push(['dispatch', args]) },
  } };
  const core = { info() {}, setFailed(message) { failures.push(message); } };
  let error;
  try {
    await run(github, { repo: { owner: '52assert', repo: 'sub2api' } }, core,
      { env: { PR_NUMBER: '3', HEAD_SHA: 'tested-head', BASE_SHA: 'tested-base' } });
  } catch (cause) { error = cause; }
  return { calls, failures, error };
}

test('validated upstream PR merges with expected SHA and dispatches custom publishing', async () => {
  const { calls, failures, error } = await exercise();
  assert.equal(error, undefined);
  assert.deepEqual(failures, []);
  assert.deepEqual(calls, [
    ['merge', { owner: '52assert', repo: 'sub2api', pull_number: 3, sha: 'tested-head', merge_method: 'merge' }],
    ['dispatch', { owner: '52assert', repo: 'sub2api', workflow_id: 'fork-ci.yml', ref: 'custom' }],
  ]);
});

test('feature and external PRs never merge or publish automatically', async () => {
  for (const head of [
    { repo: { full_name: '52assert/sub2api' }, ref: 'feature/test', sha: 'tested-head' },
    { repo: { full_name: 'someone/sub2api' }, ref: 'main', sha: 'tested-head' },
  ]) assert.deepEqual((await exercise({ head })).calls, []);
});

test('changed base/head, closed PR and draft PR cannot use stale validation', async () => {
  for (const change of [
    { base: { ref: 'custom', sha: 'new-base' } },
    { head: { repo: { full_name: '52assert/sub2api' }, ref: 'main', sha: 'new-head' } },
    { state: 'closed' }, { draft: true },
  ]) {
    const result = await exercise(change);
    assert.deepEqual(result.calls, []);
    assert.equal(result.failures.length, 1);
  }
});

test('GitHub rejection prevents image publication', async () => {
  const result = await exercise({}, { merged: false });
  assert.match(result.error.message, /did not merge/);
  assert.deepEqual(result.calls.map(([name]) => name), ['merge']);
});
