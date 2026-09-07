# Contributing to FlixR

Thanks for improving FlixR. Search existing issues before opening one. For help,
see [SUPPORT.md](SUPPORT.md); for security vulnerabilities, see [SECURITY.md](SECURITY.md).

## Setup and checks

Use Go 1.27.1, Node.js 22.19 or newer, or Node.js 24.10 or newer, with npm,
and FFmpeg/ffprobe. The root release tooling does not support Node.js 23; CI runs
its release check on Node.js 24. From a fresh checkout:

```sh
npm --prefix frontend ci
npm ci --ignore-scripts
cd backend && go test ./... && go test -race ./... && go vet ./... && go build ./...
cd ..
npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run build
npm --prefix frontend run test:bundle
npm run test:release
```

The CI workflow also runs FFmpeg media integration, Docker smoke, an offline
acceptance flow, and the browser matrix. Run those when your change affects the
relevant area; its exact commands and prerequisites are in [.github/workflows/ci.yml](.github/workflows/ci.yml).

For a local server, build the embedded frontend and then start the backend:

```sh
npm --prefix frontend run build:embed
cd backend && go build -o flixr . && ./flixr
```

See [docs/development.md](docs/development.md) for the development demo, browser
checks, and the offline acceptance command. The demo has no video files, so do
not use it as evidence that media playback works.

## Sending a change

Keep changes focused, include the check that covers changed behavior, and describe
the local commands you ran. Pull-request titles use Conventional Commits because
CI validates them for squash merges; see [docs/releases.md](docs/releases.md).

FlixR is MIT-licensed under [LICENSE](LICENSE). Frontend font notices are in
[frontend/THIRD_PARTY_LICENSES.md](frontend/THIRD_PARTY_LICENSES.md), and vendored
Interior components retain their notice in
[frontend/src/vendor/interior/README.md](frontend/src/vendor/interior/README.md).
