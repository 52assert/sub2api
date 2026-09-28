const { readFileSync } = require('node:fs');
const { resolve } = require('node:path');
const { test } = require('node:test');
const assert = require('node:assert/strict');

// Execute the actual inline resolve script with an isolated GitHub API stub.
const workflow = readFileSync(resolve(__dirname, '../../.github/workflows/fork-ci.yml'), 'utf8');
const source = workflow.split('\n  resolve:\n')[1].split('\n  checks:\n')[0]
  .split('          script: |\n')[1].replace(/^            /gm, '');
const run = new (Object.getPrototypeOf(async function () {}).constructor)(
  'github', 'context', 'core', 'process', source);

const VALIDATED = { state: 'success', statuses: [{ context: 'custom/validation', state: 'success' }] };

async function exercise({
  inputPr = '',
  parents = ['base-sha', 'head-sha'],
  status = VALIDATED,
  statusError = '',
  pullRequest,
} = {}) {
  const outputs = {};
  const warnings = [];
  const calls = [];
  const repo = { owner: '52assert', repo: 'sub2api' };
  const github = { rest: {
    repos: {
      getCommit: async (args) => {
        calls.push(['getCommit', args]);
        return { data: { parents: parents.map(sha => ({ sha })) } };
      },
      getCombinedStatusForRef: async (args) => {
        calls.push(['getCombinedStatusForRef', args]);
        if (statusError) throw new Error(statusError);
        return { data: status };
      },
      createCommitStatus: async (args) => calls.push(['createCommitStatus', args]),
    },
    pulls: {
      get: async (args) => {
        calls.push(['pullsGet', args]);
        return { data: pullRequest };
      },
    },
  } };
  const core = {
    setOutput: (key, value) => { outputs[key] = value; },
    warning: (message) => warnings.push(message),
  };
  const context = {
    repo, sha: 'merge-sha', ref: 'refs/heads/custom', runId: 1,
    serverUrl: 'https://github.com', payload: {},
  };
  let error;
  try {
    await run(github, context, core, { env: { INPUT_PR: inputPr } });
  } catch (cause) { error = cause; }
  return { outputs, warnings, calls, error };
}

test('merge commit with a validated second parent skips repeated checks', async () => {
  const { outputs, error } = await exercise();
  assert.equal(error, undefined);
  assert.equal(outputs.skip_checks, 'true');
  // Only the merged-in branch counts; the previous custom tip never has to pass again.
  assert.deepEqual(outputs.ref, 'merge-sha');
});

test('unvalidated or non-merge commits still run the full suite', async () => {
  for (const scenario of [
    { parents: ['base-sha', 'head-sha'], status: { state: 'pending', statuses: [] } },
    { parents: ['base-sha', 'head-sha'], status: { state: 'failure', statuses: [{ context: 'custom/validation', state: 'failure' }] } },
    { parents: ['base-sha', 'head-sha'], status: { state: 'success', statuses: [{ context: 'other', state: 'success' }] } },
    { parents: ['only-parent'] },
  ]) {
    const { outputs, error } = await exercise(scenario);
    assert.equal(error, undefined);
    assert.equal(outputs.skip_checks, 'false');
  }
});

test('API failures fall back to running the full suite', async () => {
  const { outputs, warnings, error } = await exercise({ statusError: 'boom' });
  assert.equal(error, undefined);
  assert.equal(outputs.skip_checks, 'false');
  assert.equal(warnings.length, 1);
});

test('PR validation runs never skip checks', async () => {
  const { outputs, error } = await exercise({
    inputPr: '7',
    pullRequest: {
      state: 'open', mergeable: true, merge_commit_sha: 'merge-sha',
      head: { repo: { full_name: '52assert/sub2api' }, sha: 'head-sha' },
      base: { ref: 'custom', sha: 'base-sha' },
    },
  });
  assert.equal(error, undefined);
  assert.equal(outputs.skip_checks, 'false');
  assert.equal(outputs.valid, 'true');
  assert.equal(outputs.ref, 'merge-sha');
});
