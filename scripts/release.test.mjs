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
    ['feat(demo): showcase library', 'minor'], ['feat(api)!: change protocol', 'minor'],
    ['fix(storage): migrate\n\nBREAKING CHANGE: migrate the data volume', 'patch'],
    ['docs!: explain deployment\n\nBREAKING CHANGE: docs only', null], ['docs: explain deployment', null], ['chore: update tooling', null],
  ]) {
    const commits = [{ message, hash: 'a'.repeat(40) }];
    assert.equal(await analyzeCommits(config.plugins[0][1], { commits, logger }), expected, message);
  }
  const notes = await generateNotes(config.plugins[1][1], {
    commits: [{ message: 'fix(player): recover sessions', hash: 'a'.repeat(40) }],
    logger, options: { repositoryUrl: 'https://github.com/mopeyjellyfish/flixr' },
    lastRelease: { gitTag: 'v0.1.0' }, nextRelease: { gitTag: 'v0.1.1', version: '0.1.1' },
  });
  assert.match(notes, /recover sessions/);
  assert.match(notes, /0\.1\.1/);
});

test('pre-v1 notes keep conventional sections and ignore squash bullets and breaking footers', async () => {
  // aa481d9, a GitHub squash merge whose ordinary body bullets were misparsed as notes.
  const squash = 'feat(accounts): add local owner recovery (#145)\n\n* feat(accounts): add local owner recovery\n\n* fix(accounts): harden owner recovery preconditions';
  const notes = await generateNotes(config.plugins[1][1], {
    commits: [
      { message: squash, hash: 'a'.repeat(40) },
      { message: 'fix(storage): retain settings\n\nBREAKING CHANGE: migrate data\n\nBREAKING CHANGES: reconfigure cache', hash: 'b'.repeat(40) },
    ],
    logger, options: { repositoryUrl: 'https://github.com/mopeyjellyfish/flixr' },
    lastRelease: { gitTag: 'v0.2.0' }, nextRelease: { gitTag: 'v0.3.0', version: '0.3.0' },
  });
  assert.match(notes, /### Features/);
  assert.match(notes, /add local owner recovery/);
  assert.match(notes, /### Bug Fixes/);
  assert.match(notes, /retain settings/);
  assert.doesNotMatch(notes, /BREAKING CHANGES/);
  assert.doesNotMatch(notes, /harden owner recovery preconditions|migrate data|reconfigure cache/);
});
test('release publisher refuses malformed versions and non-main events before Docker runs', () => {
  for (const [version, env] of [['0.1.0;echo bad', {}], ['1.0.0', {}], ['0.1.0', { GITHUB_REF: 'refs/heads/feature', GITHUB_EVENT_NAME: 'push' }], ['0.1.0', { GITHUB_REF: 'refs/heads/main', GITHUB_EVENT_NAME: 'pull_request' }]]) {
    const result = spawnSync('bash', ['scripts/release-image.sh', 'prepare', version], { env: { ...process.env, ...env }, encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.doesNotMatch(result.stdout, /bad|buildx/);
  }
});

test('real release calculation starts at 0.1.0 and keeps breaking markers below v1', async () => {
  const { mkdtempSync, rmSync } = await import('node:fs');
  const { tmpdir } = await import('node:os');
  const { join, resolve } = await import('node:path');
  const { pathToFileURL } = await import('node:url');
  const fixture = mkdtempSync(join(tmpdir(), 'flixr-release-'));
  const cwd = join(fixture, 'work');
  const remote = join(fixture, 'remote.git');
  const env = { PATH: process.env.PATH };
  const run = (command, args, directory = cwd) => {
    const result = spawnSync(command, args, { cwd: directory, env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    return result.stdout.trim();
  };
  try {
    run('git', ['init', '--bare', '--initial-branch=main', remote], fixture);
    run('git', ['clone', remote, cwd], fixture);
    run('git', ['config', 'user.name', 'Release test']);
    run('git', ['config', 'user.email', 'test@example.invalid']);
    run('git', ['commit', '--allow-empty', '-m', 'chore: initialize']);
    run('bash', [resolve('scripts/release-baseline.sh')]);
    const baseline = run('git', ['rev-parse', 'v0.0.0']);
    const calculate = async () => {
      run('git', ['push', 'origin', 'main']);
      // semantic-release hooks stdout; isolate it from node:test's reporting channel.
      const options = { dryRun: true, noCi: true, repositoryUrl: pathToFileURL(remote).href };
      const result = run(process.execPath, ['--input-type=module', '-e', `
        import { Writable } from 'node:stream';
        const { default: release } = await import(process.argv[1]);
        const { default: config } = await import(process.argv[3]);
        const quiet = new Writable({ write(_chunk, _encoding, done) { done(); } });
        const result = await release({ ...config, ...JSON.parse(process.argv[2]), plugins: config.plugins.slice(0, 2) }, {
          cwd: process.cwd(), env: process.env, stdout: quiet, stderr: quiet,
        });
        process.stdout.write(JSON.stringify(result));
      `, import.meta.resolve('semantic-release'), JSON.stringify(options), new URL('../release.config.cjs', import.meta.url).href]);
      return JSON.parse(result);
    };
    run('git', ['commit', '--allow-empty', '-m', 'feat!: first release']);
    assert.equal((await calculate()).nextRelease.version, '0.1.0');
    run('git', ['tag', 'v0.1.0']);
    run('bash', [resolve('scripts/release-baseline.sh')]);
    assert.equal(run('git', ['rev-parse', 'v0.0.0']), baseline);
    run('git', ['commit', '--allow-empty', '-m', 'fix!: repair startup', '-m', 'BREAKING CHANGE: a legacy marker']);
    const patch = await calculate();
    assert.equal(patch.nextRelease.version, '0.1.1');
    assert.doesNotMatch(patch.nextRelease.notes, /BREAKING CHANGES/);
    run('git', ['tag', 'v0.1.1']);
    run('git', ['commit', '--allow-empty', '-m', 'feat!: another feature']);
    assert.equal((await calculate()).nextRelease.version, '0.2.0');
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});
