airgapkit untuk Windows
=======================

airgap.exe adalah sisi ONLINE: ia mengunduh apt / npm / PyPI / container image
untuk target Ubuntu 24.04 (amd64, Python 3.12), apa pun OS mesin yang
menjalankannya. Sisi airgap tetap Ubuntu; script-nya (bash) ikut dibuat ke
dalam bundle. Tidak ada yang perlu di-install di Windows: satu file .exe.

1. Jalankan (PowerShell atau cmd)
---------------------------------

PowerShell:

  Unblock-File .\airgap.exe            # file dari zip/unduhan diblokir SmartScreen
  .\airgap.exe version

  Copy-Item airgap.example.json airgap.json    # lalu sesuaikan
  .\airgap.exe fetch all -config airgap.json -out C:\airgap\bundle -delta-out C:\airgap\delta-2026-09-21

  .\airgap.exe fetch apt    -pkg nginx,postgresql-16 -gen-repo -out C:\airgap\bundle
  .\airgap.exe fetch npm    -lock .\app\package-lock.json -out C:\airgap\bundle
  .\airgap.exe fetch pypi   -req .\requirements.txt -out C:\airgap\bundle
  .\airgap.exe fetch docker -image nginx:1.27 -out C:\airgap\bundle
  .\airgap.exe repos                   # katalog repo apt pihak ketiga

cmd.exe (tanpa PowerShell) - sama saja, hanya sintaksnya:

  rem buka blokir SmartScreen: klik kanan airgap.exe > Properties > Unblock, atau
  powershell -NoProfile -Command "Unblock-File .\airgap.exe"
  airgap.exe version
  copy airgap.example.json airgap.json
  airgap.exe fetch all -config airgap.json -out C:\airgap\bundle -delta-out C:\airgap\delta-2026-09-21
  airgap.exe fetch apt -pkg nginx,postgresql-16 -gen-repo ^
    -out C:\airgap\bundle

  Di cmd pakai kutip GANDA (bukan tunggal), dan path ber-spasi harus dikutip:
  -out "C:\My Files\bundle". Pemecah baris adalah ^ (di PowerShell: `).

Hasilnya identik dengan yang dibuat di Linux: target selalu Ubuntu 24.04,
bukan Windows. Wheel PyPI yang dipilih adalah cp312 manylinux x86_64, dan
paket npm untuk win32/darwin dibuang.

2. Yang perlu diperhatikan
--------------------------

* Pakai folder output PENDEK (mis. C:\airgap\bundle). Path artefak apt/docker
  panjang; Explorer/robocopy bermasalah di atas 260 karakter.
* Di airgap.json, backslash harus digandakan atau pakai slash:
  "C:/airgap/bundle" atau "C:\\airgap\\bundle".
* File input boleh CRLF, UTF-8 dengan BOM, atau UTF-16 (hasil `>` di
  PowerShell 5.1). Semuanya dibaca benar.
* Opsional, hanya bila dipakai. Cari di PATH:
    npm   untuk `fetch npm -manifest package.json` (belum ada package-lock)
    uv    untuk `fetch pypi -req` yang belum ter-pin / `-project pyproject.toml`
    gpgv  untuk `-keyring` (ikut Gpg4win)
  Hasil uv sudah dikunci ke Linux (--python-platform), jadi dependensi khusus
  Windows tidak ikut terbawa.
* Symlink tidak dibuat di Windows. Tidak masalah: merge.sh di Ubuntu
  memulihkannya.

3. Membawa ke airgap
--------------------

Salin folder delta ke flash disk (NTFS atau exFAT; FAT32 tidak bisa menyimpan
file >4 GB, mis. layer image besar). Di mesin Ubuntu airgap, panggil script
dengan `bash` karena flash disk sering ter-mount noexec:

  bash /media/usb/delta-2026-09-21/scripts/merge.sh /media/usb/delta-2026-09-21 /srv/airgap-mirror
  bash /srv/airgap-mirror/scripts/verify.sh
  bash /srv/airgap-mirror/scripts/push.sh

Detail lengkap: README.md di repo.
