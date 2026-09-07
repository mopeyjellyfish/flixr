const parserOpts = {
  headerPattern: /^(\w*)(?:\((.*)\))?!?: (.*)$/,
  breakingHeaderPattern: /^(\w*)(?:\((.*)\))?!: (.*)$/,
  noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
};
module.exports = {
  branches: ['main'],
  tagFormat: 'v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', { parserOpts, releaseRules: [
      { breaking: true, release: false },
      { type: 'feat', release: 'minor' },
      { type: 'fix', release: 'patch' },
      { type: 'perf', release: 'patch' },
    ] }],
    ['@semantic-release/release-notes-generator', { parserOpts: { ...parserOpts, breakingHeaderPattern: null, noteKeywords: null } }],
    ['@semantic-release/exec', {
      prepareCmd: 'bash scripts/release-image.sh prepare ${nextRelease.version}',
      successCmd: 'bash scripts/release-image.sh promote ${nextRelease.version}',
    }],
    ['@semantic-release/github', {
      successComment: false, failComment: false, releasedLabels: false, failTitle: false,
    }],
  ],
};
