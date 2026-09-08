![FlixR — your media, your network.](docs/images/flixr-banner.png)

# FlixR

FlixR is a free, open-source media server for your movies and TV shows.
Run it at home, choose a profile, and watch in your browser. Your accounts,
library and playback stay on your server and work without an internet connection.
Internet access is needed to download FlixR and optional metadata or artwork.

Official images are designed to include FlixR-owned TMDB application access, so
households do not need a metadata-provider account. Automatic access is enabled
only after the maintainer completes the provider registration and distribution
review described in [the release guide](docs/releases.md#tmdb-application-access).
Source builds without that application credential say so in setup and can use an
optional personal override.

**Early development (0.x).** Browser playback and per-profile audio selection are
available; native TV/mobile apps, Cast, AirPlay and subtitle selection are still
being built.

## Get started in five minutes

You need Docker with Compose 2.24 or newer, and a folder of movies or TV shows.
The first download and library scan may take longer.

### 1. Save the Compose file

Save [compose.release.yml](compose.release.yml) as **`compose.yaml`** in a new folder.
It includes FlixR, FFmpeg, persistent storage and a read-only media mount.

Create a **`.env`** file beside it:

```dotenv
FLIXR_MEDIA_DIR=/absolute/path/to/your/media
FLIXR_BIND_ADDR=0.0.0.0
```

Replace the media path with an existing folder that the container can read.
This enables HTTP access on your trusted home network. For HTTPS or access only
from this computer, see [network setup](docs/docker.md#https-on-the-lan).

### 2. Start FlixR

Open a terminal in that folder and run:

```sh
docker compose up -d --wait
docker compose logs flixr
```

The image currently requires GHCR access. If the pull is denied, use the
[source-build setup](docs/docker.md#build-locally).

### 3. Open the setup screen

Visit **http://localhost:8787**, or **http://YOUR-SERVER-IP:8787** from another device.
Copy the one-time setup token from the logs, create your owner account, choose
library folders and add a profile.

Use paths inside the container: if your media folder contains `films/` and `tv/`,
enter **`/media/films`** and **`/media/tv`** in setup.

Your settings and history are saved in Docker volumes. Your media stays read-only.
Use `docker compose down` to stop; **do not add `-v`** unless you want to delete
FlixR's saved data.

## More information

- [Docker configuration, volumes, updates and backups](docs/docker.md)
- [Build from source, run the demo and contribute](docs/development.md)
- [Contributing](CONTRIBUTING.md), [support](SUPPORT.md), and [security reporting](SECURITY.md)
- [Release process and versioning](docs/releases.md)
- [Roadmap](https://github.com/mopeyjellyfish/flixr/issues/104)
- [MIT license](LICENSE) and [third-party notices](frontend/THIRD_PARTY_LICENSES.md)

The banner shows the demo, which contains artwork and metadata for 50 movies and
50 TV shows, without video files. See the [demo guide](docs/development.md#development-demo).
