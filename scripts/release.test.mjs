import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { analyzeCommits } from '@semantic-release/commit-analyzer';
import { generateNotes } from '@semantic-release/release-notes-generator';
import config from '../release.config.cjs';
const logger = { log() {} };
test('Conventional Commits determine versions and generated notes', async () => {
  for (const [message, expected] of [
    ['fix(player): recover sessions', 'patch'], ['perf(catalog): bound queries', 'patch'],
    ['feat(demo): showcase library', 'minor'], ['feat(api)!: change protocol', 'major'],
    ['fix(storage): migrate\n\nBREAKING CHANGE: migrate the data volume', 'major'],
    ['docs: explain deployment', null], ['chore: update tooling', null],
  ]) {
    const commits = [{ message, hash: 'a'.repeat(40) }];
    assert.equal(await analyzeCommits(config.plugins[0][1], { commits, logger }), expected, message);
  }
  const notes = await generateNotes(config.plugins[1][1], {
    commits: [{ message: 'fix(player): recover sessions', hash: 'a'.repeat(40) }],
    logger, options: { repositoryUrl: 'https://github.com/mopeyjellyfish/flixr' },
    lastRelease: { gitTag: 'v1.0.0' }, nextRelease: { gitTag: 'v1.0.1', version: '1.0.1' },
  });
  assert.match(notes, /recover sessions/);
  assert.match(notes, /1\.0\.1/);
});
test('release publisher refuses malformed versions and non-main events before Docker runs', () => {
  for (const [version, env] of [['1.2.3;echo bad', {}], ['1.2.3', { GITHUB_REF: 'refs/heads/feature', GITHUB_EVENT_NAME: 'push' }], ['1.2.3', { GITHUB_REF: 'refs/heads/main', GITHUB_EVENT_NAME: 'pull_request' }]]) {
    const result = spawnSync('bash', ['scripts/release-image.sh', 'prepare', version], { env: { ...process.env, ...env }, encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.doesNotMatch(result.stdout, /bad|buildx/);
  }
});
