#!/usr/bin/env bash
# Exercise real Compose env_file, secret mounts, volume persistence and boot provisioning.
set -euo pipefail
cd "$(dirname "$0")/.."
image="${1:?usage: compose-smoke.sh IMAGE}"
name="flixr-compose-check-$$"
fresh_name="$name-fresh"
fixture="$(mktemp -d)"
cleanup() {
  docker compose -p "$fresh_name" -f compose.release.yml -f "$fixture/fresh-override.yml" down -v >/dev/null 2>&1 || true
  docker compose -p "$name" -f compose.release.yml -f "$fixture/override.yml" down -v >/dev/null 2>&1 || true
  rm -rf "$fixture"
}
trap cleanup EXIT
mkdir -p "$fixture/media/films" "$fixture/media/tv"
chmod 755 "$fixture" "$fixture/media" "$fixture/media/films" "$fixture/media/tv"
# Complete the ordinary setup contract against untouched Compose volumes first.
cat > "$fixture/fresh-override.yml" <<YAML
services:
  flixr:
    image: $image
    pull_policy: never
YAML
export FLIXR_MEDIA_DIR="$fixture/media" FLIXR_ENV_FILE="$fixture/fresh.env" FLIXR_PORT=0
fresh_compose=(docker compose -p "$fresh_name" -f compose.release.yml -f "$fixture/fresh-override.yml")
"${fresh_compose[@]}" up -d --wait >/dev/null
fresh_port="$("${fresh_compose[@]}" port flixr 8787 | sed 's/.*://')"
setup_token="$("${fresh_compose[@]}" logs flixr 2>&1 | sed -n 's/.*Flixr setup token: //p' | tail -1)"
test -n "$setup_token"
python3 - "$fresh_port" "$setup_token" <<'PY'
import sys,json,urllib.request,http.cookiejar
base='http://127.0.0.1:'+sys.argv[1]
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,data=None,method=None):
    body=None if data is None else json.dumps(data).encode()
    req=urllib.request.Request(base+'/api/v1'+path,body,{'Content-Type':'application/json','Origin':base},method=method)
    return json.load(client.open(req))
request('/setup/claim',{'token':sys.argv[2],'password':'fresh-compose-password'})
assert request('/owner/setup',{'step':'libraries'},'PATCH')['step'] == 'libraries'
setup=request('/owner/setup?films=%2Fmedia%2Ffilms&tv=%2Fmedia%2Ftv')
checks={check['id']:check for check in setup['checks']}
for key in ['data','cache','films','tv','ffprobe','ffmpeg']:
    assert checks[key]['state'] == 'ready', checks[key]
assert checks['data']['path'] == '/config'
assert checks['cache']['path'] == '/cache/segments'
assert checks['films']['path'] == '/media/films'
assert checks['tv']['path'] == '/media/tv'
assert request('/owner/setup',{'step':'profile'},'PATCH')['step'] == 'profile'
PY
"${fresh_compose[@]}" up -d --force-recreate --wait >/dev/null
fresh_port="$("${fresh_compose[@]}" port flixr 8787 | sed 's/.*://')"
python3 - "$fresh_port" <<'PY'
import sys,json,urllib.request,http.cookiejar
base='http://127.0.0.1:'+sys.argv[1]
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,data=None):
    body=None if data is None else json.dumps(data).encode()
    req=urllib.request.Request(base+'/api/v1'+path,body,{'Content-Type':'application/json','Origin':base})
    return json.load(client.open(req))
request('/owner/login',{'password':'fresh-compose-password'})
assert request('/owner/setup')['step'] == 'profile'
assert request('/owner/roots') == {'films':'','tv':''}
assert request('/profiles')['profiles'] == []
PY
"${fresh_compose[@]}" down -v >/dev/null

printf '%s\n' 'compose-test-password' > "$fixture/password"
chmod 644 "$fixture/password"
cat > "$fixture/settings.env" <<'ENV'
FLIXR_FILMS_ROOT=/media/films
FLIXR_TV_ROOT=/media/tv
FLIXR_OWNER_PASSWORD_FILE=/run/secrets/owner_password
FLIXR_INITIAL_PROFILE=Home
FLIXR_GLOBAL_BYTES=1073741824
FLIXR_MAX_GENERATIONS=4
ENV
cat > "$fixture/override.yml" <<YAML
services:
  flixr:
    image: $image
    pull_policy: never
    secrets: [owner_password]
secrets:
  owner_password:
    file: $fixture/password
YAML
export FLIXR_MEDIA_DIR="$fixture/media" FLIXR_ENV_FILE="$fixture/settings.env" FLIXR_PORT=0
compose=(docker compose -p "$name" -f compose.release.yml -f "$fixture/override.yml")
"${compose[@]}" up -d --wait >/dev/null
port="$("${compose[@]}" port flixr 8787 | sed 's/.*://')"
python3 - "$port" <<'PY'
import sys,json,urllib.request,http.cookiejar
base='http://127.0.0.1:'+sys.argv[1]
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,data=None):
    body=None if data is None else json.dumps(data).encode()
    req=urllib.request.Request(base+'/api/v1'+path,body,{'Content-Type':'application/json','Origin':base})
    return json.load(client.open(req))
assert request('/setup/status')['claimed'] is True
request('/owner/login',{'password':'compose-test-password'})
assert request('/owner/roots') == {'films':'/media/films','tv':'/media/tv'}
assert request('/owner/settings/playback')['max_generations'] == 4
assert request('/profiles')['profiles'][0]['name'] == 'Home'
PY
# Recreating with a different initial password must not reset the owner.
printf '%s\n' 'must-not-reset-password' > "$fixture/password"
"${compose[@]}" up -d --force-recreate --wait >/dev/null
port="$("${compose[@]}" port flixr 8787 | sed 's/.*://')"
python3 - "$port" <<'PY'
import sys,json,urllib.request
base='http://127.0.0.1:'+sys.argv[1]
req=urllib.request.Request(base+'/api/v1/owner/login',json.dumps({'password':'compose-test-password'}).encode(),{'Content-Type':'application/json','Origin':base})
assert urllib.request.urlopen(req).status == 200
PY
# Exercise the shipped TLS override with a temporary certificate trusted by this test.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -addext subjectAltName=IP:127.0.0.1 -keyout "$fixture/server.key" -out "$fixture/server.crt" >/dev/null 2>&1
chmod 644 "$fixture/server.key" "$fixture/server.crt"
export FLIXR_TLS_CERT_FILE="$fixture/server.crt" FLIXR_TLS_KEY_FILE="$fixture/server.key"
compose+=(-f compose.https.yml)
"${compose[@]}" up -d --force-recreate --wait >/dev/null
port="$("${compose[@]}" port flixr 8787 | sed 's/.*://')"
python3 - "$port" "$fixture/server.crt" <<'PYTHON'
import sys,json,ssl,urllib.request
base='https://127.0.0.1:'+sys.argv[1]
req=urllib.request.Request(base+'/api/v1/owner/login',json.dumps({'password':'compose-test-password'}).encode(),{'Content-Type':'application/json','Origin':base})
with urllib.request.urlopen(req, context=ssl.create_default_context(cafile=sys.argv[2])) as response:
    cookie=response.headers['Set-Cookie']
    assert response.status == 200 and all(flag in cookie for flag in ['Secure','HttpOnly','SameSite=Strict'])
PYTHON
echo 'Fresh resumable setup, Compose env_file, secrets, provisioning, persistence and trusted HTTPS checks passed.'
