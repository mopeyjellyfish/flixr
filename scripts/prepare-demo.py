#!/usr/bin/env python3
"""Download a private development catalog and artwork; never download media files."""
import hashlib
import html
import sys
import json
import os
from pathlib import Path
import re
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent / '.demo'
MOVIES = '''The Shawshank Redemption|The Godfather|The Dark Knight|The Godfather Part II|12 Angry Men|Schindler's List|The Lord of the Rings: The Return of the King|Pulp Fiction|The Lord of the Rings: The Fellowship of the Ring|Forrest Gump|Fight Club|Inception|The Lord of the Rings: The Two Towers|The Empire Strikes Back|The Matrix|Goodfellas|One Flew Over the Cuckoo's Nest|Interstellar|Se7en|It's a Wonderful Life|The Silence of the Lambs|Saving Private Ryan|The Green Mile|Terminator 2: Judgment Day|Back to the Future|The Lion King|The Prestige|Gladiator|The Departed|Whiplash|Alien|Apocalypse Now|Memento|Raiders of the Lost Ark|Django Unchained|WALL-E|The Shining|Avengers: Infinity War|Spider-Man: Into the Spider-Verse|Aliens|Coco|Toy Story|Braveheart|Inglourious Basterds|Amadeus|Good Will Hunting|Requiem for a Dream|Eternal Sunshine of the Spotless Mind|2001: A Space Odyssey|The Truman Show'''.split('|')
MOVIES += 'Heat|The Usual Suspects|Jurassic Park|The Thing|Blade Runner|The Sixth Sense|No Country for Old Men|There Will Be Blood|A Beautiful Mind|The Grand Budapest Hotel|Up|Inside Out|Ratatouille|Finding Nemo|Monsters, Inc.|The Incredibles|Oppenheimer|The Dark Knight Rises|Casablanca|The Wizard of Oz'.split('|')
MOVIE_YEARS = {'Gladiator': 2000, 'Whiplash': 2014, 'Braveheart': 1995, 'Heat': 1995, 'The Thing': 1982, 'The Lion King': 1994, '12 Angry Men': 1957}
SHOWS = '''Breaking Bad|Better Call Saul|The Wire|The Sopranos|Game of Thrones|Band of Brothers|Chernobyl|Severance|The Last of Us|Succession|The Bear|Stranger Things|Dark|Fargo|True Detective|Sherlock|Mr. Robot|The Office|Friends|Seinfeld|The Simpsons|South Park|Rick and Morty|BoJack Horseman|Avatar: The Last Airbender|The Legend of Korra|Arcane: League of Legends|Attack on Titan|Fullmetal Alchemist: Brotherhood|Cowboy Bebop|The Mandalorian|Andor|The Boys|Fallout|House of the Dragon|The Crown|Peaky Blinders|Black Mirror|The Queen's Gambit|The Haunting of Hill House|Twin Peaks|Lost|Dexter|House|The West Wing|Mad Men|Fleabag|Ted Lasso|The White Lotus|Slow Horses'''.split('|')


def download(url, headers=None, limit=32 << 20):
    request = urllib.request.Request(url, headers={'User-Agent': 'FlixrDevDemo/1.0 (local metadata preview)', **(headers or {})})
    for attempt in range(4):
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                data = response.read(limit + 1)
                if len(data) > limit:
                    raise ValueError(f'Download exceeds size limit: {url}')
                return data, response.headers.get_content_type()
        except urllib.error.HTTPError as error:
            if error.code not in (429, 502, 503, 504) or attempt == 3:
                raise RuntimeError(f'Download failed ({error.code}): {url}') from error
            time.sleep(2 ** (attempt + 1))
    raise RuntimeError('Download failed')


def get_json(url, headers=None):
    cache = ROOT / 'responses' / (hashlib.sha256(url.encode()).hexdigest() + '.json')
    if not cache.exists():
        data, _ = download(url, headers)
        json.loads(data)
        cache.write_bytes(data)
        time.sleep(.25)
    return json.loads(cache.read_text())


def asset(url):
    if not url:
        return ''
    if 'upload.wikimedia.org/' in url and '/thumb/' in url:
        url = re.sub(r'/\d+px-', '/330px-', url)
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != 'https' or parsed.hostname not in ('upload.wikimedia.org', 'static.tvmaze.com', 'image.tmdb.org'):
        raise ValueError(f'Unexpected artwork host: {url}')
    filename = hashlib.sha256(url.encode()).hexdigest() + '.img'
    target = ROOT / 'assets' / filename
    if not target.exists():
        data, kind = download(url, limit=8 << 20)
        if kind not in ('image/jpeg', 'image/png', 'image/webp', 'image/gif'):
            raise ValueError(f'Not artwork: {url}')
        target.write_bytes(data)
    return filename


def curated():
    source = get_json('https://raw.githubusercontent.com/prust/wikipedia-movie-data/master/movies.json')
    films = []
    for name in MOVIES:
        matches = [item for item in source if item['title'].casefold() == name.casefold() and item.get('thumbnail') and (name not in MOVIE_YEARS or item['year'] == MOVIE_YEARS[name])]
        if not matches:
            print(f'Skipping unavailable movie metadata: {name}', flush=True)
            continue
        item = min(matches, key=lambda item: item['year'])
        try:
            poster = asset(item['thumbnail'])
        except RuntimeError as error:
            print(f'Skipping unavailable poster for {name}: {error}', flush=True)
            continue
        films.append(dict(title=item['title'], year=item['year'], synopsis=item.get('extract', ''), genres=item['genres'], poster=poster, backdrop=''))
        print(f'Movie {len(films)}/50: {name}', flush=True)
        if len(films) == 50:
            break
    shows = []
    for name in SHOWS:
        matches = get_json('https://api.tvmaze.com/search/shows?q=' + urllib.parse.quote(name))
        exact = [result['show'] for result in matches if result['show']['name'].casefold() == name.casefold()]
        if name == 'The Office':
            exact = [show for show in exact if show['premiered'].startswith('2005')]
        if not exact:
            raise ValueError(f'No exact TV match for {name}')
        show = exact[0]
        detail = get_json(f'https://api.tvmaze.com/shows/{show["id"]}?embed=episodes')
        images = get_json(f'https://api.tvmaze.com/shows/{show["id"]}/images')
        backgrounds = [image for image in images if image['type'] == 'background']
        episodes = [dict(title=ep['name'], season=ep['season'], number=ep['number']) for ep in detail.get('_embedded', {}).get('episodes', []) if ep.get('season') and ep.get('number') and ep['season'] <= 2][:24]
        shows.append(dict(title=show['name'], year=int(show['premiered'][:4]), synopsis=html.unescape(re.sub('<[^>]+>', '', show.get('summary') or '')), genres=show['genres'], poster=asset(show['image']['original']), backdrop=asset(backgrounds[0]['resolutions']['original']['url']) if backgrounds else '', episodes=episodes))
        print(f'TV {len(shows)}/50: {name}', flush=True)
    return dict(source='Curated showcase, not a live ranking. Film metadata: Wikipedia via prust/wikipedia-movie-data (CC BY-SA); TV metadata: TVmaze (CC BY-SA). Artwork belongs to its respective owners. Local development preview only.', films=films, series=shows)


def tmdb(token):
    headers = {'Authorization': 'Bearer ' + token}
    def api(path):
        return get_json('https://api.themoviedb.org/3/' + path, headers)
    groups = {}
    for kind, key in [('movie', 'films'), ('tv', 'series')]:
        genres = {g['id']: g['name'] for g in api(f'genre/{kind}/list?language=en-US')['genres']}
        items = sum([api(f'{kind}/top_rated?language=en-US&page={page}')['results'] for page in range(1, 4)], [])[:50]
        groups[key] = []
        for item in items:
            episodes = []
            if kind == 'tv':
                season = api(f'tv/{item["id"]}/season/1?language=en-US')
                episodes = [dict(title=ep['name'], season=1, number=ep['episode_number']) for ep in season.get('episodes', [])][:24]
            groups[key].append(dict(title=item.get('title') or item['name'], year=int((item.get('release_date') or item['first_air_date'])[:4]), synopsis=item['overview'], genres=[genres[g] for g in item['genre_ids'] if g in genres], poster=asset('https://image.tmdb.org/t/p/w500' + item['poster_path']) if item.get('poster_path') else '', backdrop=asset('https://image.tmdb.org/t/p/w1280' + item['backdrop_path']) if item.get('backdrop_path') else '', episodes=episodes))
            print(f'{kind} {len(groups[key])}/50: {groups[key][-1]["title"]}', flush=True)
    return dict(source='TMDB top-rated snapshot. This product uses the TMDB API but is not endorsed or certified by TMDB. Artwork belongs to its respective owners. Local development preview only.', **groups)


if __name__ == '__main__':
    for folder in ('assets', 'responses'):
        (ROOT / folder).mkdir(parents=True, exist_ok=True)
    manifest = ROOT / 'catalog.json'
    if '--refresh' in sys.argv:
        for response in (ROOT / 'responses').glob('*.json'):
            response.unlink()
    if manifest.exists() and '--refresh' not in sys.argv:
        print('Using cached demo snapshot. No network required.')
    else:
        token = os.environ.get('TMDB_READ_ACCESS_TOKEN')
        result = tmdb(token) if token else curated()
        assert len(result['films']) == len(result['series']) == 50
        temporary = ROOT / 'catalog.json.tmp'
        temporary.write_text(json.dumps(result, ensure_ascii=False))
        temporary.replace(manifest)
        print('Demo prepared: 50 movies, 50 TV shows, artwork, and episode metadata; no media files.')
