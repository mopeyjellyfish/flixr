const title = process.env.PR_TITLE ?? '';
if (!/^(feat|fix|perf|refactor|docs|test|build|ci|chore|revert)(\([^\r\n()]+\))?!?: \S[^\r\n]*$/.test(title)) {
  console.error('Use a Conventional Commit PR title, for example: feat(player): add subtitle selection');
  process.exitCode = 1;
}
