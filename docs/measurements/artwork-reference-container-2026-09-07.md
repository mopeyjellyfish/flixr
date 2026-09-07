# Container-limited artwork measurement — 2026-09-07

This records the remaining performance evidence for issue #56 using an owned,
disposable Docker container. It is a **4-CPU, 8 GiB container-limited reference
environment**, not a claim that the physical host is a 4-core/8 GiB machine.

## Environment and isolation

- Product source: `095e24da69fb4f3f183aa74c93d628c059844ebf`.
  The measurement harness was at `60a3b1eee459acba4ec191ac567ff9d11d5f4644`;
  it made no `backend` or `frontend` product changes relative to that source.
- Physical host recorded for context: Linux 6.8.0-138-generic, Intel Core
  i7-10700K, 16 online CPUs, 32 GiB RAM. It had 26.8 GiB available before the
  run. These facts do not qualify the host itself as the requested reference
  hardware.
- Docker 29.7.2 on cgroup v2 started the owned container with
  `--cpus=4 --memory=8g --memory-swap=8g --pids-limit=512`, verified after the
  run as `NanoCpus=4000000000`, `Memory=8589934592`, `MemorySwap=8589934592`,
  and `PidsLimit=512`.
- The container used `--network=none`, a read-only root filesystem, a 2 GiB
  private `/tmp`, a 1 GiB shared-memory allocation, `--cap-drop=ALL`, and
  `no-new-privileges`. Its only host mount was a newly-created empty results
  directory. It did not inspect or mount a home library, an existing Flixr
  container, a service port, a Docker volume, credentials, or a user demo.

The container generated 100 deterministic local 1200 × 1800 PNG posters and a
valid temporary demo snapshot. It copied the repository's committed
`Long Duration Seek 2026.mkv` fixture, then made 300 temporary fixture copies
for the concurrent scan. Before the cold visit it removed only its own
`<mktemp>/data/artwork/derivatives` directory; generated original artwork and
all other temporary data remained present.

## Combined result

Chromium, Flixr, FFmpeg, and FFprobe ran inside the same constrained cgroup.
The browser counted an artwork URL only after `HTMLImageElement.decode()` and
viewport intersection. Both records observed 46 visible decoded artwork images.

| Visit | First visible decoded | 30 visible decoded | Visible decoded / visible artwork |
| --- | ---: | ---: | ---: |
| Cold derivative cache | 408.3 ms | 1877.0 ms | 46 / 46 |
| Warm repeat | 360.5 ms | 968.7 ms | 46 / 46 |

The owner scan and the exact fixture's active fMP4 remux were asserted at the
completion of **each** browser visit. The second scan completed with
`scanned=301`, `failed=0`, and `unmatched=0`.

Cgroup v2 counters sampled every 100 ms across all processes in the container
reported a peak of **512.3% CPU** and **1,299,384 KiB memory.current**. The
brief CPU sample can exceed 400% at a CFS quota-period boundary; Docker's
recorded 4-CPU quota above is the enforceable allocation. `memory.current` is
container memory, not a process RSS estimate.

The complete sanitized summary is checked in as
[`artwork-reference-container-2026-09-07.json`](artwork-reference-container-2026-09-07.json).
Its SHA-256 is
`1183991469596891e31839904934c78465a4352f78eebc7083332003de14bad0`.

## Reproduce on an authorized Linux Docker host

Run from an exact checkout. The command creates a uniquely named image and
container, removes both on exit, and writes only its result directory.

```sh
FLIXR_MEASURE_RESULT_DIR="$PWD/artwork-reference-results" \
  bash scripts/run-artwork-reference-container.sh
```

Do not run this command against a host unless it has the capacity for the
separate 8 GiB allocation. The script records the host context and Docker
limits in `environment.txt`, saves Docker's `container-inspect.json`, and
emits the timing, concurrency, scan, and cgroup resource records in
`summary.json` and `resources.log`.
