# airgapkit

Tool untuk **mengunduh seluruh dependensi** (apt, npm, PyPI, container image) dari
mesin ber-internet, **membawanya ke lingkungan airgap**, lalu **mendorongnya ke
Sonatype Nexus Repository 3** di sana.

Target sistem: **Ubuntu 24.04 (noble), amd64, Python 3.12**.

Dua bagian:

| Bagian | Dijalankan di | Kebutuhan |
|---|---|---|
| `airgap` (binary Go) | mesin **online** | Go 1.24+ untuk build. Opsional: `npm`, `uv` (untuk input yang belum ter-lock) |
| `scripts/*.sh` | mesin **airgap** | hanya `bash` + `curl` (tanpa docker, python, npm, atau jq) |

---

## 1. Build

```bash
./build.sh              # binary statis di ./airgap
./build.sh check        # go vet + gofmt + unit test + syntax check script bash
./build.sh offline      # buktikan bisa di-build tanpa jaringan
./build.sh dist         # ./dist: binary + source tarball + SHA256SUMS
```

`make` juga jalan kalau terpasang (cuma pembungkus `build.sh`), tapi `build.sh`
sengaja hanya butuh bash + Go supaya bisa dipakai juga di mesin airgap.

Hasilnya binary **statis** (CGO mati, `-trimpath`, stripped) berukuran ~7,5 MB:

```
$ file airgap
airgap: ELF 64-bit LSB executable, x86-64, statically linked, stripped
$ ./airgap version
airgap dev (built 2026-09-20T12:57:20Z, go1.27.1 linux/amd64)
```

Tidak ada satu pun dependensi Go eksternal (stdlib saja) — terbukti dengan
`./build.sh offline`, yang build ulang dengan `GOPROXY=off` dan cache kosong
lalu melaporkan `external modules needed: 0`. Karena itu `dist/` juga memuat
source tarball: sisi airgap bisa membangun sendiri binary-nya daripada
mempercayai binary yang menyeberang.

## 1b. Apa saja yang perlu dibawa

Satu file `airgap` (statis, ~7,5 MB) sudah cukup untuk hampir semua hal. Diuji
dengan `PATH=/nonexistent` — jadi benar-benar tanpa tool lain:

| Yang dikerjakan | Butuh tool lain? |
|---|---|
| `fetch apt` (termasuk 22 sumber dari katalog) | tidak |
| `fetch docker` (Docker Hub, MCR, ghcr, registry privat) | tidak |
| `fetch npm -lock package-lock.json` | tidak |
| `fetch pypi -uv-lock uv.lock` | tidak |
| `fetch pypi -req <sudah ter-pin>` | tidak |
| `verify`, `index`, `scripts`, `repos`, `tags` | tidak |
| `fetch npm -manifest package.json` (belum ter-lock) | **npm** |
| `fetch pypi -req <belum ter-pin>` / `-project pyproject.toml` | **uv** |
| verifikasi tanda tangan `Release` (`-keyring`) | **gpgv** |
| repo apt yang hanya menerbitkan `Packages.xz`/`.zst` | **xz**/**zstd** |

Empat baris terakhir itu opsional dan pesan errornya menyebutkan persis apa
yang kurang beserta alternatifnya. Tidak ada satu pun repo di katalog bawaan
yang butuh `xz`/`zstd` (semuanya menyediakan `.gz` atau `Packages` polos).

Di **sisi airgap** binary ini tidak diperlukan sama sekali: bundle membawa
`scripts/` sendiri, yang hanya memakai `bash`, `curl`, dan utilitas bawaan
Ubuntu (`awk`, `sed`, `tar`, `sha256sum`, `sort`, `grep`, `mktemp`). Tidak ada
Go, Docker, Python, npm, atau `jq`.

## 2. Alur kerja

```
  [ mesin online ]                    [ USB ]                [ mesin airgap ]

  airgap fetch all                    delta/                 merge.sh  -> mirror permanen
    -out ./bundle          ───────▶   (artefak   ───────▶    verify.sh -> cek sha256
    -delta-out ./delta                 baru saja)            push.sh   -> upload ke Nexus
```

- `./bundle` di mesin online adalah **mirror lengkap yang terus bertambah**.
  Run kedua, ketiga, dan seterusnya hanya mengunduh yang belum ada.
- `--delta-out` menghasilkan folder berisi **hanya artefak baru** untuk dibawa
  via USB — tidak perlu menyalin ulang seluruh mirror tiap kali.
- Di sisi airgap, `merge.sh` menggabungkan delta ke satu **mirror permanen**
  milik Anda, dan `push.sh` mendorong ke Nexus secara **incremental**.

## 3. Mengunduh (mesin online)

```bash
# Semua ekosistem dari satu file konfigurasi
./airgap fetch all -config airgap.json -delta-out ./delta-2026-09-21

# Atau per ekosistem, lewat flag
./airgap fetch apt    -pkg nginx,postgresql-16 -sources ubuntu.sources -gen-repo
./airgap fetch npm    -lock app/package-lock.json
./airgap fetch npm    -manifest app/package.json          # di-lock dulu pakai npm
./airgap fetch pypi   -req requirements.txt -gen-index
./airgap fetch pypi   -uv-lock uv.lock
./airgap fetch pypi   -project pyproject.toml             # di-lock dulu pakai uv
./airgap fetch docker -image nginx:1.27 -image ghcr.io/org/app:v1
./airgap tags postgres                                   # tag apa saja yang tersedia

# Lihat dulu tanpa mengunduh
./airgap plan apt -pkg nginx
```

Salin `airgap.example.json` menjadi `airgap.json` lalu sesuaikan.

### apt

- **Repo Ubuntu 24.04 sudah terpasang default** (`noble`, `noble-updates`,
  `noble-security`; main/universe/restricted/multiverse), jadi
  `./airgap fetch apt -pkg doublecmd-gtk` langsung jalan tanpa konfigurasi
  apa pun. Set `"default_sources": false` di config bila ingin memakai mirror
  internal saja.
- Closure **penuh**: `Depends` + `Pre-Depends` diikuti rekursif sampai habis,
  termasuk paket base/essential — aman untuk target minimal maupun container.
  Tambahkan `-recommends` bila `Recommends` juga diperlukan.
- **Salah ketik nama paket bukan jalan buntu**: seluruh index sudah ada di
  memori, jadi nama yang mirip langsung ditawarkan sebelum tool bertanya soal
  repo.
  ```
  requested package "postgressql-16" not found in any configured repository
  Packages with a similar name:
     1) postgresql-16
  Package: 1
    closure: 84 packages, 84.1 MiB
  ```
- **Kalau paket tidak ketemu, tool langsung menawarkan menambah repo** (kecuali
  dijalankan dengan `-yes` atau tanpa terminal):
  ```
  requested package "google-chrome-stable" not found in any configured repository
  Add another repository? [Y/n]: y
  Repository (e.g. ppa:alexx2000/doublecmd, or: deb [arch=amd64] https://host/repo noble main):
  ```
  Formatnya bebas: `ppa:owner/nama`, baris `deb ...` lengkap, atau sekadar
  `URI suite komponen`. Setelah unduhan berhasil, tool menawarkan menyimpan
  repo itu ke `airgap.json` supaya run berikutnya tidak perlu ditanya lagi.
- **Katalog repo pihak ketiga bawaan.** `./airgap repos` menampilkan ~32 repo
  vendor resmi (Docker, Kubernetes, NVIDIA CUDA, PGDG, NGINX, Redis,
  HashiCorp, Grafana, Elastic, Cloudflare, Caddy, Tailscale, NodeSource,
  Microsoft, dan seterusnya) yang tinggal dipanggil dengan kuncinya:
  ```bash
  ./airgap repos
  ./airgap fetch apt -repo docker,pgdg,nginx -pkg docker-ce,postgresql-18,nginx
  ```
  URI, suite, dan komponen tiap entri sudah diverifikasi langsung ke file
  `Release` masing-masing (mis. komponen NGINX adalah `nginx`, bukan `main`;
  Kubernetes dan CUDA adalah repo *flat*). Kunci katalog juga diterima pada
  prompt interaktif dan bisa ditulis di `airgap.json` sebagai
  `"apt": { "repos": ["docker", "pgdg"] }`.
- Repo pihak ketiga juga bisa ditulis langsung di `apt.sources` pada
  `airgap.json`, atau lewat file `sources.list` / deb822 dengan `-sources`.
  ```
  deb [arch=amd64] https://download.docker.com/linux/ubuntu noble stable
  ```
- Menambah repo pihak ketiga = menambah ketergantungan *supply chain*. Yang
  ada di katalog adalah repo upstream vendor kecuali yang ditandai
  "community PPA". Untuk memverifikasi tanda tangan repo (bukan sekadar
  checksum), isi `"keyring"` pada source itu di `airgap.json` — tiap vendor
  punya kunci sendiri, jadi keyring diatur per-source, bukan global.
- Index diverifikasi SHA256 terhadap `Release`. Tanda tangan GPG opsional:
  `-keyring /usr/share/keyrings/ubuntu-archive-keyring.gpg` (butuh `gpgv`).
- `-gen-repo` membuat `Packages`/`Release` lokal sehingga mirror langsung bisa
  dipakai tanpa Nexus:
  ```
  deb [trusted=yes] file:///srv/airgap-mirror/apt stable main
  ```

### npm

- `package-lock.json` (v1/v2/v3) dibaca langsung. Entry tanpa tarball registry
  (root, workspace `link`, dependensi git) dilewati dengan sendirinya.
- Integritas diverifikasi dari field `integrity` (SRI `sha512-<base64>`).
- Hanya punya `package.json`? Tool memanggil
  `npm install --package-lock-only --ignore-scripts` **di salinan sementara**
  proyek Anda, jadi working tree tidak tersentuh.
- Paket opsional untuk OS/CPU lain (darwin, win32, android, arm) **dibuang
  secara default** karena targetnya selalu linux/amd64 — untuk `vite` ini
  memangkas dari 40 menjadi 17 tarball. Pakai `-all-platforms` bila memang
  perlu semuanya. `-no-dev` melewati devDependencies.

### PyPI

- Memakai **Simple API PEP 691** dan memilih wheel dengan aturan PEP 425/600
  untuk `cp312` + `manylinux` (glibc ≤ 2.39) + `x86_64`. Prioritas:
  manylinux terbaru → `abi3` → `linux_x86_64` → `py3-none-any`.
  `musllinux`, `win*`, `macosx*`, arsitektur lain ditolak.
- `uv.lock` bersifat universal (berisi wheel semua platform), jadi tool tetap
  memfilternya; nama file diturunkan dari URL karena `uv.lock` tidak menyimpannya.
- `requirements.txt` dan daftar `-pkg` dilewatkan `uv pip compile` supaya
  **dependensi transitif ikut terbawa**. Tanpa `uv`, file harus sudah ter-pin penuh.
- Paket yang tidak punya wheel cocok akan diambil sdist-nya dan **ditandai
  warning** (di airgap artinya butuh toolchain build). `-no-sdist` untuk menolak.
- `-gen-index` membuat index PEP 503 lokal:
  ```
  pip install --index-url file:///srv/airgap-mirror/pypi/simple requests
  ```

### Docker / OCI

- Bicara langsung ke Registry v2 API — **tidak butuh docker daemon**.
- Disimpan sebagai OCI layout dengan **blob store bersama**
  (`docker/blobs/sha256/`), sehingga `nginx:1.27` dan `nginx:1.28` berbagi layer
  yang sama dan mirror tidak membengkak.
- Registry privat: isi `docker.auths` di `airgap.json`.
- Untuk manifest list, yang diambil adalah varian sesuai `platform`
  (default `linux/amd64`).
- Nexus **di bawah 3.71** belum mendukung manifest OCI. Untuk kasus itu jalankan
  dengan `-convert-v2` agar manifest ditulis ulang ke docker schema2.
- **Kalau nama image salah, Docker Hub membalas 401, bukan 404** (Hub tidak mau
  membocorkan apakah sebuah repo privat atau tidak ada). Tool menerjemahkannya
  jadi pesan yang benar: nama tidak ada / tag tidak ada / repo privat butuh
  kredensial. Untuk melihat tag yang tersedia: `./airgap tags postgres`.
- **Salah nama bukan jalan buntu**: tool menampilkan kandidat dan Anda tinggal
  pilih nomornya, lalu proses lanjut sendiri.
  ```
  $ ./airgap fetch docker -image pgsql:17-alpine
    docker.io/library/pgsql:17-alpine: no such image on Docker Hub
    Try one of these instead:
       1) docker.io/library/postgres:17-alpine     <- tag Anda ikut dibawa
       2) docker.io/bahmni/pgsql:17-alpine
       or type another name, or press Enter to skip
    Image: 1
      docker.io/library/postgres:17-alpine -> linux/amd64 (11 blobs, 111.8 MiB)
  ```
  Kalau yang salah **tag**-nya, yang ditawarkan adalah daftar tag yang benar-benar
  ada (terbaru dulu). Dengan `-yes` atau tanpa terminal, kandidatnya tetap
  dicetak di pesan error.
- Salah nama yang umum langsung diarahkan ke nama yang benar, termasuk image
  yang memang **tidak ada di Docker Hub**:
  ```
  $ ./airgap tags mssql
  error: docker.io/library/mssql: no such repository on Docker Hub
         it is published on another registry, not Docker Hub; use:
           airgap fetch docker -image mcr.microsoft.com/mssql/server:<tag>
  $ ./airgap tags pgsql
         the official image is called "postgres"
  ```
  Registry lain (MCR, ghcr.io, quay.io, Nexus) didukung penuh: `airgap tags
  mcr.microsoft.com/mssql/server` dan `fetch -image mcr.microsoft.com/...`
  berjalan lewat Registry v2 API yang sama.
- Satu image gagal tidak membatalkan image lain dalam satu perintah; yang gagal
  dilaporkan di akhir dan exit code jadi non-nol.

## 4. Struktur bundle

```
bundle/
├── MANIFEST.tsv      # sumber kebenaran: eco, sha256, size, path, url, waktu
├── SHA256SUMS
├── README.txt        # ringkasan langkah untuk yang menerima bundle ini
├── scripts/          # push.sh, merge.sh, verify.sh + lib/ (ikut otomatis)
├── apt/pool/...                       + apt/dists/stable/...   (-gen-repo)
├── npm/tarballs/<paket>/<file>.tgz
├── pypi/packages/<paket>/<file>.whl   + pypi/simple/...        (-gen-index)
└── docker/
    ├── blobs/sha256/<digest>          # dipakai bersama semua image
    ├── images/<registry>/<repo>/<tag>/{manifest.json,index.json,blobs.tsv}
    └── IMAGES.tsv
```

Manifest sengaja berformat **TSV append-only**: menggabungkan dua mirror cukup
`sort -u`, dan `push.sh` bisa membacanya tanpa `jq`.

**Bundle bersifat self-contained.** Script sisi airgap ditanam di dalam binary
(`go:embed`) dan otomatis ditulis ke `<bundle>/scripts/` setiap kali `fetch`
dijalankan — juga ke folder `-delta-out`. Karena isinya salinan, sidik jari isinya
dicap di `scripts/.airgap-version` dan dicetak di baris pertama `push.sh`:

```
push.sh:     airgap dev (content 5831c1813739d821)
```

Bandingkan dengan binary untuk tahu apakah salinan itu terkini — yang
dibandingkan hash isi, bukan tanggal build, karena tanggal berubah tiap kali
di-build walau script-nya tidak:

```bash
./airgap version            # ... bundled air-gap scripts: 5831c1813739d821
./airgap scripts -out ./bundle
#   ./bundle/scripts: already up to date (content 5831c1813739d821)
```

Kalau salinan itu tertinggal dari binary (opsi baru belum ada di sana),
`push.sh` bilang apa adanya saat menemui argumen tak dikenal dan menyebutkan
`airgap scripts -out <bundle>` untuk menyegarkannya. Di sisi airgap tidak perlu
diurus manual: setiap delta baru membawa script versi terbaru, dan `merge.sh`
menimpanya ke mirror. Jadi USB yang Anda bawa sudah
membawa `push.sh` sendiri; repo ini tidak perlu ikut menyeberang. Untuk
menulis ulang script saja (misalnya setelah update tool):

```bash
./airgap scripts -out /srv/airgap-mirror
```

## 5. Di sisi airgap

Script dipanggil dari dalam bundle/mirror itu sendiri. Tanpa `--bundle`,
script otomatis memakai folder induknya, jadi ini sudah cukup:

```bash
# 1. gabungkan delta ke mirror permanen
/media/usb/delta-2026-09-21/scripts/merge.sh /media/usb/delta-2026-09-21 /srv/airgap-mirror

# 2. pastikan tidak ada file rusak setelah transfer
/srv/airgap-mirror/scripts/verify.sh

# 3. dorong ke Nexus (interaktif saat pertama kali)
/srv/airgap-mirror/scripts/push.sh
/srv/airgap-mirror/scripts/push.sh apt npm      # sebagian saja
/srv/airgap-mirror/scripts/push.sh --dry-run
/srv/airgap-mirror/scripts/push.sh --retry-failed
```

`push.sh` menanyakan URL Nexus, username, password, dan nama repo tiap
ekosistem, lalu menyimpannya ke `~/.nexus-push.conf` (mode 600, **termasuk
password**). Run berikutnya tinggal menekan Enter. Variabel lingkungan
(`NEXUS_URL`, `NEXUS_USER`, `NEXUS_PASS`, `NEXUS_REPO_APT`, `NEXUS_REPO_NPM`,
`NEXUS_REPO_PYPI`, `NEXUS_REPO_DOCKER`, `NEXUS_DOCKER_HOST`) selalu menang, dan
dengan `--yes` script tidak pernah menulis password ke disk.

**Nama repo dicek di depan.** Sebelum satu file pun diunggah, script menanyakan
`/service/rest/v1/repositories` ke Nexus dan memastikan repo yang dipakai benar
ada dan formatnya cocok. Kalau salah nama, daftar repo yang benar-benar ada
langsung ditawarkan untuk dipilih — tanpa ini, satu salah ketik berarti ratusan
error 404 berturut-turut. Nama yang sudah dikoreksi itulah yang disimpan ke
`~/.nexus-push.conf`.

**Memaksa kirim ulang (`--force`).** Secara default artefak yang state-nya sudah
`ok` dilewati. Dengan `--force` semuanya dikirim ulang, state lokal diabaikan,
dan pengecekan blob di registry docker juga dilewati. Apakah server benar-benar
menimpa file lama tergantung *deployment policy* repo itu:

| Deployment policy | Tanpa `--force` | Dengan `--force` |
|---|---|---|
| Allow redeploy | dilewati (sudah ok) | diunggah ulang, file di server ditimpa |
| Disable redeploy | dilewati (sudah ok) | ditolak 400, dilaporkan **gagal** + cara mengubah policy-nya |

Jadi untuk benar-benar menimpa isi server: set *Deployment policy* repo itu ke
**Allow redeploy** di Nexus, lalu jalankan `--force`.

**Incremental:** status tiap artefak dicatat di
`<mirror>/.push-state/<host>-<repo>.tsv` berdasarkan sha256, jadi push kedua
hanya mengirim yang baru. Yang dilewati hanya artefak yang **berhasil**; yang
pernah gagal selalu dicoba lagi otomatis, karena penyebab kegagalan biasanya
ada di server dan sudah diperbaiki sejak itu. Tiap ekosistem menutup dengan
satu baris ringkasan (`pypi: 0 uploaded, 7 already on the server, 0 failed`)
supaya run yang tidak mengirim apa-apa tidak terlihat seperti menggantung.

**Kredensial ditolak = berhenti, bukan lanjut.** Kalau Nexus membalas 401
(password salah) atau 429 (Nexus membatasi percobaan login yang gagal),
script langsung berhenti dengan pesan itu, bukan meneruskan ratusan upload
yang pasti gagal. Perbaiki dengan `./push.sh --reconfigure`. State di-key per Nexus, sehingga satu mirror bisa
didorong ke lebih dari satu Nexus. Kalau state hilang pun aman: Nexus membalas
`400 ... does not allow updating assets` untuk artefak yang sudah ada dan itu
dihitung sebagai *skip*, bukan error.

## 6. Catatan konfigurasi Nexus

- **apt hosted** — Nexus menandatangani metadata sendiri, jadi repo harus dibuat
  dengan **GPG keypair**. Unggah memakai Components API (`apt.asset`).
- **npm / pypi hosted** — cukup repo hosted biasa (`npm.asset`, `pypi.asset`).
- **docker hosted, path based routing** — Nexus mengembalikan header `Location`
  saat memulai upload blob yang **relatif terhadap akar registry**
  (`/v2/<nama>/blobs/uploads/<uuid>`), tanpa awalan `/repository/<repo>`.
  Kalau awalan itu tidak dipasang kembali, PUT-nya mendarat di luar registry
  dan Nexus menjawab **405 Method Not Allowed** untuk setiap blob. `push.sh`
  menangani ini; kalau Anda menulis skrip sendiri, itu jebakan utamanya.
- **rate limiting** — setelah beberapa login gagal Nexus membalas **429** dan
  mencatat `Rate limiting key 'user::admin': retryAfter=30s`. Tunggu setengah
  menit; ini bukan password yang salah.
- **docker hosted** — `push.sh` mencoba **basic auth** lebih dulu, dan pada
  Nexus 3.96 Community itu sudah cukup untuk push (diuji: `POST
  .../blobs/uploads/` menjawab 202 dengan basic auth saja). *Docker Bearer
  Token Realm* baru diperlukan kalau instance Anda menolak basic auth; tanpa
  realm itu Nexus tetap mengiklankan bearer di `WWW-Authenticate` padahal
  `/v2/token` menjawab 404, dan `push.sh` menyebut persis setelan yang harus
  diubah kalau sampai ke situ. Endpoint registry bisa berupa
  connector `host:port` (isi `NEXUS_DOCKER_HOST`) atau path based routing
  (`NEXUS_DOCKER_HOST` dikosongkan, dipakai `$NEXUS_URL/repository/<repo>/v2`,
  Nexus 3.83+).
- **Deployment policy** — `Allow redeploy` membuat upload ulang menimpa;
  `Disable redeploy` membuatnya dijawab 400 dan di-skip. Keduanya aman.
- Nama repo di Nexus: `docker.io/library/nginx:1.27` didorong menjadi
  `library/nginx:1.27`. Set `NEXUS_DOCKER_KEEP_REGISTRY=yes` untuk menyimpan
  nama registry asal sebagai prefix.

## 7. Pengujian

```bash
go test ./...        # perbandingan versi dpkg, pemilihan wheel, parser lock
go vet ./...
```

Uji integrasi yang sudah dilakukan: closure `apt` dicocokkan dengan
`apt-cache depends --recurse`, repo `file://` hasil `-gen-repo` dibaca
`apt-get update`, index PyPI lokal dipakai `uv pip install`, dan image hasil
unduhan didorong ke registry v2 lewat `push.sh` lalu ditarik kembali dengan
`docker pull`.
