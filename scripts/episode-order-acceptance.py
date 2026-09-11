#!/usr/bin/env python3
"""Exercise TV ordering against an isolated binary and real generated media."""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--previous-binary', type=Path, help='Start and scan with an older binary before upgrading')
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    result = {'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'checks': []}
    current_binary = args.previous_binary.resolve(strict=True) if args.previous_binary else binary
    if args.previous_binary:
        result['previous_binary_sha256'] = hashlib.sha256(current_binary.read_bytes()).hexdigest()
    with tempfile.TemporaryDirectory(prefix='flixr-episode-order-') as temporary:
        root = Path(temporary)
        films, tv = root / 'films', root / 'tv'
        films.mkdir()
        tv.mkdir()
        password = root / 'owner-password'
        password.write_text('isolated-episode-order-acceptance-only')
        password.chmod(0o600)
        fixtures = ['Signal/Season 01/Signal S01E01-E02.mp4',
                    'Signal/Season 01/Signal S01E03.mp4',
                    'Signal/Season 01/Signal S01E100.mp4',
                    'Signal/Season 00/Signal S00E01.mp4']
        for index, name in enumerate(fixtures):
            target = tv / name
            target.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(['ffmpeg', '-nostdin', '-hide_banner', '-loglevel', 'error',
                            '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=24:duration=3',
                            '-f', 'lavfi', '-i', f'sine=frequency={440 + index * 100}:duration=3',
                            '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac',
                            '-movflags', '+faststart', '-shortest', str(target)], check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        base = f'http://127.0.0.1:{port}'
        env = {k: v for k, v in os.environ.items() if not k.startswith('FLIXR_')}
        env.update(FLIXR_LISTEN_ADDR=f'127.0.0.1:{port}', FLIXR_DATA_DIR=str(root / 'config'),
                   FLIXR_SEGMENT_DIR=str(root / 'segments'), FLIXR_FILMS_ROOT=str(films),
                   FLIXR_TV_ROOT=str(tv), FLIXR_METADATA_ENABLED='false', FLIXR_DEMO='false',
                   FLIXR_OWNER_PASSWORD_FILE=str(password))
        owner = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        viewer = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        process = None
        log = open(root / 'server.log', 'wb')

        def request(client, path, body=None, method=None, expected=200):
            data = json.dumps(body).encode() if body is not None else None
            req = urllib.request.Request(base + path, data=data, method=method,
                                         headers={'Content-Type': 'application/json', 'Origin': base})
            try:
                response = client.open(req, timeout=20)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                assert response.status == expected, f'{req.get_method()} {path}: {response.status}, expected {expected}'
                return json.loads(raw) if raw else {}

        def start():
            nonlocal process
            process = subprocess.Popen([str(current_binary)], env=env, stdout=log, stderr=log)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                assert process.poll() is None, 'Isolated server exited before readiness'
                try:
                    status = request(owner, '/api/v1/setup/status')
                    assert status['claimed'] and status['readiness']['ffprobe']
                    return
                except (urllib.error.URLError, ConnectionError):
                    time.sleep(.1)
            raise AssertionError('Isolated server readiness timed out')

        def stop():
            if process and process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise AssertionError('Server did not shut down gracefully')

        def scan():
            queued = request(owner, '/api/v1/owner/scan', {}, expected=202)
            jobs = queued.get('jobs', [])
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if jobs:
                    states = [request(owner, '/api/v1/owner/scan/jobs/' + job['id'])['job']['status'] for job in jobs]
                    assert all(state in ('queued', 'running', 'succeeded') for state in states), states
                    if all(state == 'succeeded' for state in states):
                        return
                    time.sleep(.1)
                    continue
                status = request(owner, '/api/v1/owner/scan/status')['scan']
                if status['status'] != 'running':
                    assert status['status'] == 'complete', f'Scan status: {status["status"]}'
                    return
                time.sleep(.1)
            raise AssertionError('Scan timed out')

        capabilities = {'containers': ['mp4'], 'video_codecs': ['h264'], 'audio_codecs': ['aac'],
                        'supports_direct': True, 'max_width': 1920, 'max_height': 1080,
                        'max_frame_rate_milli': 60000, 'max_bit_depth': 8, 'max_audio_channels': 2}

        def next_from(catalog_id, expected_id=None, state='next', specials=False, decode=False, complete=False):
            playback = request(viewer, '/api/v1/playback/plans',
                               {'catalog_id': catalog_id, 'capabilities': capabilities}, expected=201)
            try:
                if decode:
                    with viewer.open(base + playback['media_url'], timeout=20) as media:
                        payload = media.read(2_000_000)
                    sample = root / 'served.mp4'
                    sample.write_bytes(payload)
                    subprocess.run(['ffmpeg', '-nostdin', '-v', 'error', '-i', str(sample),
                                    '-frames:v', '1', '-f', 'null', '-'], check=True)
                if complete:
                    request(viewer, playback['heartbeat_url'], {'position_ms': 3000, 'observation': 1, 'ended': True})
                suffix = '?include_specials=true' if specials else ''
                sequence = request(viewer, f'/api/v1/playback/sessions/{playback["session_id"]}/next{suffix}')
                assert sequence['state'] == state, sequence
                if expected_id:
                    assert sequence['episode']['id'] == expected_id, sequence
                return sequence
            finally:
                request(viewer, playback['stop_url'], {})

        try:
            start()
            request(owner, '/api/v1/owner/login', {'password': password.read_text()})
            profile = request(owner, '/api/v1/profiles', {'name': 'Acceptance viewer', 'pin': ''}, expected=201)
            request(viewer, f'/api/v1/profiles/{profile["id"]}/select', {'pin': ''})
            scan()
            if args.previous_binary:
                old_series = request(viewer, '/api/v1/catalog/home')['items'][0]['id']
                old_ids = {episode['id'] for season in request(viewer, '/api/v1/catalog/series/' + old_series)['seasons'] for episode in season['episodes']}
                stop()
                current_binary = binary
                start()
                upgraded_ids = {episode['id'] for season in request(viewer, '/api/v1/catalog/series/' + old_series)['seasons'] for episode in season['episodes']}
                assert upgraded_ids == old_ids, 'Upgrade changed catalog identities'
                result['checks'].append('existing installation upgrades without changing episode identities')
            listing = request(owner, '/api/v1/owner/episode-orders')
            assert len(listing['series']) == 1, listing
            series_id = listing['series'][0]['id']
            endpoint = '/api/v1/owner/episode-orders/' + series_id
            detail = request(owner, endpoint)
            entries = detail['entries']
            ids = {(entry['season'], entry['episode']): entry['catalog_id'] for entry in entries}
            first, third, hundredth, special = [ids[key] for key in [(1, 1), (1, 3), (1, 100), (0, 1)]]
            assert next(entry for entry in entries if entry['catalog_id'] == first)['episode_end'] == 2
            next_from(first, third, decode=True)
            result['checks'].append('real-media scan, E100, multi-episode span, aired Next Up and decoded served frame')
            request(viewer, endpoint, expected=403)
            result['checks'].append('profile session cannot access owner order controls')
            for entry in entries:
                start_position = {hundredth: 1, third: 2, first: 3, special: 5}[entry['catalog_id']]
                end_position = start_position + (1 if entry['catalog_id'] == first else 0)
                entry['mapping'] = {'position': start_position, 'end_position': end_position,
                                    'season': 1, 'episode': start_position, 'episode_end': end_position,
                                    'special': entry['catalog_id'] == special}
            payload = {'order': 'dvd', 'revision': detail['revision'], 'entries': entries}
            saved = request(owner, endpoint, payload, method='PUT')
            assert not saved['needs_repair'], saved
            request(owner, endpoint, payload, method='PUT', expected=409)
            next_from(hundredth, third)
            next_from(first, state='end_of_series')
            next_from(first, special, specials=True)
            result['checks'].append('DVD mapping, stale-write rejection and specials opt-in')
            broken = json.loads(json.dumps(saved['entries']))
            for entry in broken:
                if entry['catalog_id'] == third:
                    entry.pop('mapping', None)
            repair = request(owner, endpoint, {'order': 'dvd', 'revision': saved['revision'], 'entries': broken}, method='PUT')
            assert repair['needs_repair'], repair
            next_from(hundredth, state='context_unavailable')
            saved = request(owner, endpoint, {'order': 'dvd', 'revision': repair['revision'], 'entries': entries}, method='PUT')
            next_from(hundredth, third)
            result['checks'].append('incomplete order blocks guessing; explicit owner repair restores Next Up')
            next_from(hundredth, third, complete=True)
            history_before = request(viewer, '/api/v1/history')['events']
            assert history_before, 'Completed real media did not record viewing history'
            stop()
            start()
            reopened = request(owner, endpoint)
            assert reopened == saved, 'Saved order changed across restart'
            scan()
            rescanned = request(owner, endpoint)
            assert rescanned == saved, 'Saved order or identities changed across rescan'
            history_after = request(viewer, '/api/v1/history')['events']
            assert history_after == history_before, 'Restart/rescan changed viewing history'
            next_from(hundredth, third, decode=True)
            result['checks'].append('offline restart/rescan preserves identity, order, viewing history and decoded playback')
            absolute = request(owner, endpoint, {'order': 'absolute', 'revision': saved['revision'], 'entries': entries}, method='PUT')
            next_from(hundredth, third)
            restored = request(owner, endpoint, {'order': 'aired', 'revision': absolute['revision'], 'entries': []}, method='PUT')
            assert restored['order'] == 'aired' and not restored['needs_repair'], restored
            next_from(first, third)
            result['checks'].append('absolute selection and restoring parsed aired order update Next Up')
            empty = request(owner, endpoint, {'order': 'dvd', 'revision': restored['revision'], 'entries': []}, method='PUT')
            assert empty['needs_repair'], 'An empty selected DVD order must require repair'
            next_from(first, state='context_unavailable')
            result['checks'].append('an empty alternate mapping never silently falls back to aired order')
            result['status'] = 'passed'
        finally:
            stop()
            log.close()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
