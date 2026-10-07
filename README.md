# airgapkit

airgapkit is a toolkit for getting software dependencies into an air-gapped network that runs Sonatype Nexus Repository 3. On a machine with internet access, the `airgap` command (a single static Go binary) downloads everything a set of projects needs: apt packages with their full dependency closure, npm tarballs from lock files, PyPI wheels selected for the target platform, and container images from Docker Hub or other registries. It stores them in a content-addressed bundle with a manifest and SHA256 checksums, and can write only the newly downloaded files to a delta folder that you carry across on a USB stick. On the air-gapped side, bundled bash scripts that need only `bash` and `curl` merge each delta into a permanent mirror, verify it, and push it incrementally to Nexus. The target system is Ubuntu 24.04 (noble), amd64, with Python 3.12. The Go code uses only the standard library, so it builds offline, and it has unit tests.

> Status: early development (single initial commit, September 2026). The repository name is `apt-npm-doc-downloader`; the tool and Go module are called `airgapkit`.

It has two parts:

| Part | Runs on | Requirements |
|---|---|---|
| `airgap` (Go binary) | the **online** machine | Go 1.24+ to build. Optional: `npm`, `uv` (for inputs that are not locked yet) |
| `scripts/*.sh` | the **air-gapped** machine | only `bash` + `curl` (no docker, python, npm or jq) |

## Features

- **apt**: full `Depends`/`Pre-Depends` closure, Ubuntu 24.04 archives built in, a catalogue of about 32 vendor repositories, suggestions for mistyped package names, SHA256 index verification and optional GPG verification, and optional local `Packages`/`Release` generation
- **npm**: reads `package-lock.json` v1 to v3 directly, checks SRI integrity, and drops optional packages for other platforms
- **PyPI**: PEP 691 Simple API with PEP 425/600 wheel selection for `cp312` + manylinux + x86_64, `uv.lock` support, and an optional local PEP 503 index
- **Docker / OCI**: talks to the Registry v2 API directly with no Docker daemon, uses a shared blob store, handles private registries, and can convert manifests to docker schema2 for older Nexus versions
- **Incremental transfer**: an append-only TSV manifest, delta folders for USB transfer, and per-Nexus push state keyed by sha256
- **Self-contained bundles**: the air-gap scripts are embedded in the binary and written into every bundle and delta

## Tech stack

Go 1.24 (standard library only) · Bash · curl · Sonatype Nexus Repository 3 REST and Docker Registry v2 APIs

## 1. Build

```bash
./build.sh              # static binary at ./airgap
./build.sh test         # unit tests only
./build.sh check        # go vet + gofmt + unit tests + bash syntax check of the scripts
./build.sh offline      # prove it builds without network access
./build.sh dist         # ./dist: binary + source tarball + SHA256SUMS
./build.sh release      # ./dist: linux + windows (amd64, arm64) binaries + source tarball + SHA256SUMS
./build.sh clean
```

### Prebuilt binaries

Every `v*` tag is built by `.github/workflows/release.yml` and published on the [Releases page](../../releases) with:

| File | For |
|---|---|
| `airgap-<version>-linux-amd64` | Ubuntu / any x86-64 Linux (static) |
| `airgap-<version>-linux-arm64` | ARM64 Linux (static) |
| `airgap-<version>-windows-amd64.exe` | Windows x64 |
| `airgap-<version>-windows-arm64.exe` | Windows on ARM |
| `airgapkit-<version>-src.tar.gz` | source, to rebuild on the air-gapped side |
| `SHA256SUMS` | checksums of all of the above |

The air-gap scripts are embedded, so the single binary is all you download. On Linux, `chmod +x` it after downloading. To cut a release: `git tag v0.1.0 && git push origin v0.1.0`.

On Windows, apt indexes compressed as `.xz`/`.bz2`/`.zst` need the matching tool on `PATH` (`.gz`, which Ubuntu publishes, works out of the box), and the scripts written into the bundle still run on the air-gapped Linux machine.

`make` also works if it is installed (it only wraps `build.sh`), but `build.sh` deliberately needs nothing more than bash and Go so that it can also be used on the air-gapped machine.

The result is a **static** binary (CGO disabled, `-trimpath`, stripped) of about 7.5 MB:

```text
$ file airgap
airgap: ELF 64-bit LSB executable, x86-64, statically linked, stripped
$ ./airgap version
airgap dev (built 2026-09-20T12:57:20Z, go1.27.1 linux/amd64)
```

There are no external Go dependencies at all (standard library only). `./build.sh offline` proves this: it rebuilds with `GOPROXY=off` and an empty cache and reports `external modules needed: 0`. That is why `dist/` also contains a source tarball: the air-gapped side can build its own binary instead of trusting one that crossed over.

### Commands

```text
airgap fetch  [apt|npm|pypi|docker|all] [flags]   download into the bundle
airgap plan   [apt|npm|pypi|docker|all] [flags]   show what would be downloaded
airgap index  [flags]                             regenerate SHA256SUMS and local repo metadata
airgap verify [flags]                             re-hash the bundle and report drift
airgap scripts [flags]                            (re)write the air-gap scripts into the bundle
airgap repos                                      list the built-in third-party repositories
airgap tags IMAGE                                 list the tags a registry publishes for an image
airgap version                                    print the version
```

Common flags: `-config FILE` (default `airgap.json` when present), `-out DIR` (default `./bundle`), `-delta-out DIR`, `-j N` (parallel downloads, default 8), `-yes` (never ask questions) and `-v` (verbose HTTP logging). Run `./airgap` with no arguments for the full flag list.

## 1b. What you need to bring

The single `airgap` file (static, about 7.5 MB) is enough for almost everything. It was tested with `PATH=/nonexistent`, so truly without any other tools:

| Task | Needs another tool? |
|---|---|
| `fetch apt` (including the 22 sources from the catalogue) | no |
| `fetch docker` (Docker Hub, MCR, ghcr, private registries) | no |
| `fetch npm -lock package-lock.json` | no |
| `fetch pypi -uv-lock uv.lock` | no |
| `fetch pypi -req <already pinned>` | no |
| `verify`, `index`, `scripts`, `repos`, `tags` | no |
| `fetch npm -manifest package.json` (not locked yet) | **npm** |
| `fetch pypi -req <not pinned>` / `-project pyproject.toml` | **uv** |
| verifying the `Release` signature (`-keyring`) | **gpgv** |
| apt repositories that only publish `Packages.xz`/`.zst` | **xz**/**zstd** |

The last four rows are optional, and their error messages say exactly what is missing and what the alternatives are. None of the repositories in the built-in catalogue need `xz`/`zstd` (they all provide `.gz` or plain `Packages`).

On the **air-gapped side** the binary is not needed at all: the bundle carries its own `scripts/`, which only use `bash`, `curl` and standard Ubuntu utilities (`awk`, `sed`, `tar`, `sha256sum`, `sort`, `grep`, `mktemp`). No Go, Docker, Python, npm or `jq`.

## 2. Workflow

```text
  [ online machine ]                  [ USB ]                [ air-gapped machine ]

  airgap fetch all                    delta/                 merge.sh  -> permanent mirror
    -out ./bundle          ───────▶   (new       ───────▶    verify.sh -> check sha256
    -delta-out ./delta                 artifacts             push.sh   -> upload to Nexus
                                       only)
```

- `./bundle` on the online machine is a **complete mirror that keeps growing**. The second, third and later runs only download what is not there yet.
- `-delta-out` produces a folder with **only the new artifacts** to carry over on USB, so you do not need to copy the whole mirror every time.
- On the air-gapped side, `merge.sh` merges the delta into a single **permanent mirror** that you own, and `push.sh` pushes to Nexus **incrementally**.

## 3. Downloading (online machine)

```bash
# Every ecosystem from one configuration file
./airgap fetch all -config airgap.json -delta-out ./delta-2026-09-21

# Or one ecosystem at a time, with flags
./airgap fetch apt    -pkg nginx,postgresql-16 -sources ubuntu.sources -gen-repo
./airgap fetch npm    -lock app/package-lock.json
./airgap fetch npm    -manifest app/package.json          # locked with npm first
./airgap fetch pypi   -req requirements.txt -gen-index
./airgap fetch pypi   -uv-lock uv.lock
./airgap fetch pypi   -project pyproject.toml             # locked with uv first
./airgap fetch docker -image nginx:1.27 -image ghcr.io/org/app:v1
./airgap tags postgres                                   # which tags are available

# Look first, without downloading
./airgap plan apt -pkg nginx
```

Copy `airgap.example.json` to `airgap.json` and adjust it.

### apt

- **The Ubuntu 24.04 repositories are included by default** (`noble`, `noble-updates`, `noble-security`; main/universe/restricted/multiverse), so `./airgap fetch apt -pkg doublecmd-gtk` works with no configuration at all. Set `"default_sources": false` in the config if you only want to use an internal mirror.
- **Full** closure: `Depends` + `Pre-Depends` are followed recursively to the end, including base/essential packages, so it is safe for minimal targets and containers. Add `-recommends` if `Recommends` are needed too.
- **A mistyped package name is not a dead end**: the whole index is already in memory, so similar names are offered straight away, before the tool asks about repositories.
  ```text
  requested package "postgressql-16" not found in any configured repository
  Packages with a similar name:
     1) postgresql-16
  Package: 1
    closure: 84 packages, 84.1 MiB
  ```
- **If a package is not found, the tool offers to add a repository** (unless it is run with `-yes` or without a terminal):
  ```text
  requested package "google-chrome-stable" not found in any configured repository
  Add another repository? [Y/n]: y
  Repository (e.g. ppa:alexx2000/doublecmd, or: deb [arch=amd64] https://host/repo noble main):
  ```
  The format is flexible: `ppa:owner/name`, a full `deb ...` line, or just `URI suite component`. After a successful download, the tool offers to save that repository to `airgap.json` so the next run does not ask again.
- **Built-in catalogue of third-party repositories.** `./airgap repos` lists about 32 official vendor repositories (Docker, Kubernetes, NVIDIA CUDA, PGDG, NGINX, Redis, HashiCorp, Grafana, Elastic, Cloudflare, Caddy, Tailscale, NodeSource, Microsoft and more) that you reference by key:
  ```bash
  ./airgap repos
  ./airgap fetch apt -repo docker,pgdg,nginx -pkg docker-ce,postgresql-18,nginx
  ```
  The URI, suite and components of each entry were checked against that repository's `Release` file (for example, the NGINX component is `nginx`, not `main`; Kubernetes and CUDA are *flat* repositories). Catalogue keys are also accepted at the interactive prompt and can be written in `airgap.json` as `"apt": { "repos": ["docker", "pgdg"] }`.
- Third-party repositories can also be written directly in `apt.sources` in `airgap.json`, or passed as a `sources.list` / deb822 file with `-sources`.
  ```text
  deb [arch=amd64] https://download.docker.com/linux/ubuntu noble stable
  ```
- Adding a third-party repository adds a *supply chain* dependency. The catalogue entries are upstream vendor repositories unless marked "community PPA". To verify a repository's signature (not just its checksums), set `"keyring"` on that source in `airgap.json`. Each vendor has its own key, so the keyring is set per source, not globally.
- Indexes are verified by SHA256 against `Release`. GPG signature checking is optional: `-keyring /usr/share/keyrings/ubuntu-archive-keyring.gpg` (needs `gpgv`).
- `-gen-repo` writes local `Packages`/`Release` files so the mirror can be used directly without Nexus:
  ```text
  deb [trusted=yes] file:///srv/airgap-mirror/apt stable main
  ```

### npm

- `package-lock.json` (v1/v2/v3) is read directly. Entries without a registry tarball (the root, workspace `link` entries, git dependencies) are skipped automatically.
- Integrity is verified from the `integrity` field (SRI `sha512-<base64>`).
- Only have a `package.json`? The tool runs `npm install --package-lock-only --ignore-scripts` **in a temporary copy** of your project, so your working tree is not touched.
- Optional packages for other OSes/CPUs (darwin, win32, android, arm) are **dropped by default** because the target is always linux/amd64. For `vite` this cuts 40 tarballs down to 17. Use `-all-platforms` if you really need them all. `-no-dev` skips devDependencies.

### PyPI

- Uses the **PEP 691 Simple API** and selects wheels by the PEP 425/600 rules for `cp312` + `manylinux` (glibc ≤ 2.39) + `x86_64`. Priority: newest manylinux → `abi3` → `linux_x86_64` → `py3-none-any`. `musllinux`, `win*`, `macosx*` and other architectures are rejected. `-python` and `-glibc` change the target.
- `uv.lock` is universal (it lists wheels for every platform), so the tool still filters it. File names are derived from the URLs because `uv.lock` does not store them.
- `requirements.txt` and `-pkg` lists are passed through `uv pip compile` so that **transitive dependencies come along**. Without `uv`, the file must already be fully pinned.
- Packages without a matching wheel fall back to their sdist and are **flagged with a warning** (on the air-gapped side that means a build toolchain is needed). Use `-no-sdist` to refuse them.
- `-gen-index` writes a local PEP 503 index:
  ```bash
  pip install --index-url file:///srv/airgap-mirror/pypi/simple requests
  ```

### Docker / OCI

- Talks directly to the Registry v2 API, so **no docker daemon is needed**.
- Images are stored as an OCI layout with a **shared blob store** (`docker/blobs/sha256/`), so `nginx:1.27` and `nginx:1.28` share their common layers and the mirror does not balloon.
- Private registries: fill in `docker.auths` in `airgap.json`.
- For a manifest list, the variant matching `platform` (default `linux/amd64`) is fetched.
- Nexus **before 3.71** does not support OCI manifests. In that case run with `-convert-v2` so manifests are rewritten to docker schema2.
- **If an image name is wrong, Docker Hub answers 401, not 404** (Hub will not reveal whether a repository is private or does not exist). The tool translates this into the right message: the name does not exist, the tag does not exist, or the repository is private and needs credentials. To see the available tags: `./airgap tags postgres`.
- **A wrong name is not a dead end**: the tool shows candidates, you pick a number, and it carries on.
  ```text
  $ ./airgap fetch docker -image pgsql:17-alpine
    docker.io/library/pgsql:17-alpine: no such image on Docker Hub
    Try one of these instead:
       1) docker.io/library/postgres:17-alpine     <- your tag is kept
       2) docker.io/bahmni/pgsql:17-alpine
       or type another name, or press Enter to skip
    Image: 1
      docker.io/library/postgres:17-alpine -> linux/amd64 (11 blobs, 111.8 MiB)
  ```
  If the **tag** is wrong, the tool offers the list of tags that actually exist (newest first). With `-yes` or without a terminal, the candidates are still printed in the error message.
- Common wrong names are redirected to the right one, including images that are **not on Docker Hub at all**:
  ```text
  $ ./airgap tags mssql
  error: docker.io/library/mssql: no such repository on Docker Hub
         it is published on another registry, not Docker Hub; use:
           airgap fetch docker -image mcr.microsoft.com/mssql/server:<tag>
  $ ./airgap tags pgsql
         the official image is called "postgres"
  ```
  Other registries (MCR, ghcr.io, quay.io, Nexus) are fully supported: `airgap tags mcr.microsoft.com/mssql/server` and `fetch -image mcr.microsoft.com/...` go through the same Registry v2 API.
- One failed image does not cancel the others in the same command. Failures are reported at the end and the exit code is non-zero.

## 4. Bundle layout

```text
bundle/
├── MANIFEST.tsv      # source of truth: eco, sha256, size, path, url, time
├── SHA256SUMS
├── README.txt        # summary of the steps for whoever receives this bundle
├── scripts/          # push.sh, merge.sh, verify.sh + lib/ (added automatically)
├── apt/pool/...                       + apt/dists/stable/...   (-gen-repo)
├── npm/tarballs/<package>/<file>.tgz
├── pypi/packages/<package>/<file>.whl + pypi/simple/...        (-gen-index)
└── docker/
    ├── blobs/sha256/<digest>          # shared by every image
    ├── images/<registry>/<repo>/<tag>/{manifest.json,index.json,blobs.tsv}
    └── IMAGES.tsv
```

The manifest is deliberately an **append-only TSV**: merging two mirrors is just `sort -u`, and `push.sh` can read it without `jq`.

**Bundles are self-contained.** The air-gap scripts are embedded in the binary (`go:embed`) and written to `<bundle>/scripts/` every time `fetch` runs, and also to the `-delta-out` folder. Because they are copies, a fingerprint of their content is stamped in `scripts/.airgap-version` and printed on the first line of `push.sh`:

```text
push.sh:     airgap dev (content 5831c1813739d821)
```

Compare it with the binary to tell whether the copy is current. The comparison uses a content hash, not the build date, because the date changes on every build even when the scripts do not:

```bash
./airgap version            # ... bundled air-gap scripts: 5831c1813739d821
./airgap scripts -out ./bundle
#   ./bundle/scripts: already up to date (content 5831c1813739d821)
```

If that copy is behind the binary (a new option is missing), `push.sh` says so when it meets an unknown argument and tells you to refresh it with `airgap scripts -out <bundle>`. On the air-gapped side you do not need to manage this by hand: every new delta carries the latest scripts, and `merge.sh` copies them over the mirror. So the USB stick you carry already has its own `push.sh`; this repository does not need to cross over. To rewrite only the scripts (for example after updating the tool):

```bash
./airgap scripts -out /srv/airgap-mirror
```

## 5. On the air-gapped side

The scripts are run from inside the bundle or mirror itself. Without `--bundle`, a script uses its parent folder, so this is enough:

```bash
# 1. merge the delta into the permanent mirror
/media/usb/delta-2026-09-21/scripts/merge.sh /media/usb/delta-2026-09-21 /srv/airgap-mirror

# 2. make sure no file was damaged in transfer
/srv/airgap-mirror/scripts/verify.sh

# 3. push to Nexus (interactive the first time)
/srv/airgap-mirror/scripts/push.sh
/srv/airgap-mirror/scripts/push.sh apt npm      # only some ecosystems
/srv/airgap-mirror/scripts/push.sh --dry-run
/srv/airgap-mirror/scripts/push.sh --retry-failed
```

`push.sh` asks for the Nexus URL, username, password and the repository name for each ecosystem, then saves them to `~/.nexus-push.conf` (mode 600, **including the password**). Later runs only need Enter. Environment variables always win: `NEXUS_URL`, `NEXUS_USER`, `NEXUS_PASS`, `NEXUS_REPO_APT`, `NEXUS_REPO_NPM`, `NEXUS_REPO_PYPI`, `NEXUS_REPO_DOCKER`, `NEXUS_DOCKER_HOST`. With `--yes` the script never writes the password to disk. Other options: `--config`, `--reconfigure`, `--force`, `--help`.

**Repository names are checked up front.** Before a single file is uploaded, the script queries `/service/rest/v1/repositories` on Nexus and makes sure the repositories exist and have the right format. If a name is wrong, the list of repositories that actually exist is offered for you to choose from. Without this, one typo would mean hundreds of 404 errors in a row. The corrected name is what gets saved to `~/.nexus-push.conf`.

**Forcing a re-send (`--force`).** By default, artifacts whose state is already `ok` are skipped. With `--force` everything is sent again, local state is ignored, and the blob check against the docker registry is skipped too. Whether the server really overwrites old files depends on the repository's *deployment policy*:

| Deployment policy | Without `--force` | With `--force` |
|---|---|---|
| Allow redeploy | skipped (already ok) | uploaded again, the file on the server is overwritten |
| Disable redeploy | skipped (already ok) | rejected with 400, reported as **failed** plus how to change the policy |

So to really overwrite what is on the server: set the repository's *Deployment policy* to **Allow redeploy** in Nexus, then run with `--force`.

**Incremental:** the status of each artifact is recorded in `<mirror>/.push-state/<host>-<repo>.tsv` by sha256, so a second push only sends what is new. Only **successful** artifacts are skipped; failed ones are always retried automatically, because the cause is usually on the server side and has been fixed since. Each ecosystem ends with a one-line summary (`pypi: 0 uploaded, 7 already on the server, 0 failed`) so a run that sends nothing does not look like it is hanging.

**Rejected credentials stop the run.** If Nexus answers 401 (wrong password) or 429 (Nexus is throttling failed login attempts), the script stops with that message instead of carrying on with hundreds of uploads that are bound to fail. Fix it with `./push.sh --reconfigure`. State is keyed per Nexus, so one mirror can be pushed to more than one Nexus. Losing the state is also safe: Nexus answers `400 ... does not allow updating assets` for artifacts that already exist, and that counts as a *skip*, not an error.

## 6. Nexus configuration notes

- **apt hosted**: Nexus signs the metadata itself, so the repository must be created with a **GPG keypair**. Uploads use the Components API (`apt.asset`).
- **npm / pypi hosted**: a plain hosted repository is enough (`npm.asset`, `pypi.asset`).
- **docker hosted, path based routing**: when a blob upload starts, Nexus returns a `Location` header that is **relative to the registry root** (`/v2/<name>/blobs/uploads/<uuid>`), without the `/repository/<repo>` prefix. If that prefix is not put back, the PUT lands outside the registry and Nexus answers **405 Method Not Allowed** for every blob. `push.sh` handles this; if you write your own script, this is the main trap. `test/docker-push-test.sh` covers it against a mock registry (`test/mock-registry.py`).
- **rate limiting**: after a few failed logins Nexus answers **429** and logs `Rate limiting key 'user::admin': retryAfter=30s`. Wait half a minute; the password is not necessarily wrong.
- **docker hosted**: `push.sh` tries **basic auth** first, and on Nexus 3.96 Community that is enough to push (tested: `POST .../blobs/uploads/` answers 202 with basic auth alone). The *Docker Bearer Token Realm* is only needed if your instance rejects basic auth. Without that realm, Nexus still advertises bearer auth in `WWW-Authenticate` even though `/v2/token` answers 404, and `push.sh` names the exact setting to change if it gets that far. The registry endpoint can be a `host:port` connector (set `NEXUS_DOCKER_HOST`) or path based routing (leave `NEXUS_DOCKER_HOST` empty and `$NEXUS_URL/repository/<repo>/v2` is used, Nexus 3.83+).
- **Deployment policy**: `Allow redeploy` makes a re-upload overwrite; `Disable redeploy` makes it answer 400 and the artifact is skipped. Both are safe.
- Repository names in Nexus: `docker.io/library/nginx:1.27` is pushed as `library/nginx:1.27`. Set `NEXUS_DOCKER_KEEP_REGISTRY=yes` to keep the source registry name as a prefix.

## 7. Testing

```bash
go test ./...        # dpkg version comparison, wheel selection, lock file parsers
go vet ./...
./build.sh check     # the above plus gofmt and bash syntax checks
```

Integration tests that have been done: the `apt` closure was compared with `apt-cache depends --recurse`, the `file://` repository written by `-gen-repo` was read by `apt-get update`, the local PyPI index was used by `uv pip install`, and downloaded images were pushed to a v2 registry with `push.sh` and pulled back with `docker pull`.

## Project structure

```text
cmd/airgap/          CLI entry point and one file per subcommand/ecosystem
internal/apt/        Packages/Release parsing, dpkg version comparison, dependency closure
internal/npm/        package-lock parsing and lock generation
internal/pypi/       Simple API client, wheel tag selection, requirements and uv.lock parsers
internal/oci/        Registry v2 client, auth, Docker Hub helpers, OCI layout
internal/config/     airgap.json loading and saving, sources, repository catalogue
internal/dl/         HTTP download client and hashing
internal/fetch/      skips artifacts already in the mirror, bounded parallel downloads
internal/store/      on-disk bundle layout, append-only manifest and delta output
scripts/             air-gap side: merge.sh, verify.sh, push.sh and lib/ (embedded via scripts.go)
test/                docker push test against a mock registry
airgap.example.json  sample configuration
```
