const parserOpts = {
  headerPattern: /^(\w*)(?:\((.*)\))?!?: (.*)$/,
  breakingHeaderPattern: /^(\w*)(?:\((.*)\))?!: (.*)$/,
  noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
};
module.exports = {
  branches: ['main'],
  tagFormat: 'v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', { parserOpts }],
    ['@semantic-release/release-notes-generator', { parserOpts }],
    ['@semantic-release/exec', {
      prepareCmd: 'bash scripts/release-image.sh prepare ${nextRelease.version}',
      successCmd: 'bash scripts/release-image.sh promote ${nextRelease.version}',
    }],
    ['@semantic-release/github', {
      successComment: false, failComment: false, releasedLabels: false, failTitle: false,
    }],
  ],
};
