# Mekanisme Koneksi Wi‑Fi dan Perilaku Captive Portal di Windows, macOS, dan Arch Linux (iwd/iwctl)

## Ringkasan Eksekutif  
Koneksi Wi-Fi di Windows dikelola oleh *WLAN AutoConfig* (layanan `wlansvc`) dan *Network List Service*, dengan alat userland seperti *Settings* atau `netsh wlan`【30†L35-L43】. Windows melakukan deteksi captive portal lewat NCSI (Network Connectivity Status Indicator): ia mengirim permintaan DNS/HTTP ke situs uji Microsoft (misal `www.msftconnecttest.com`) dan memeriksa konten “Microsoft Connect Test”【41†L77-L85】. Jika hasilnya tidak sesuai (seperti dialihkan portal), Windows menampilkan notifikasi “Sign in to network” dan membuka halaman login di browser.  

Di macOS/iOS, sistem menggunakan komponen bawaan (seperti *configd*/CoreWLAN) untuk mengelola Wi-Fi. Saat terhubung, macOS/iOS otomatis mengirim permintaan HTTP ke `captive.apple.com` untuk memeriksa konektivitas【45†L136-L144】. Jika respons tidak sesuai harapan (atau DHCP/RA khusus captive portal ditemukan), sistem membuka *Captive Network Assistant* (jendela mini Safari) untuk login.  

Di Linux modern, daemon iwd (iNet Wireless Daemon) menggantikan `wpa_supplicant` dan dapat diintegrasikan dengan NetworkManager. Iwd sendiri mengurus otentikasi WPA/WPA2 menggunakan API kernel (nl80211) dan kriptografi kernel (tanpa OpenSSL)【50†L47-L50】. Namun **iwd tidak memiliki deteksi captive portal built‑in**. Sebaliknya, NetworkManager (dengan backend iwd) dapat melakukan pengecekan konektivitas secara berkala dan membuka portal secara otomatis jika gagal (dikonfigurasi di `/etc/NetworkManager/NetworkManager.conf`)【61†L17-L25】【61†L28-L32】.  

**Masalah umum:** Tanpa dukungan deteksi, pengguna iwd kerap harus membuka browser secara manual. Beberapa laporan menyebutkan solusi seperti paket *captive-browser* (AUR) atau pengaturan Firefox (`network.captive-portal-service.enabled`)【71†L142-L150】【71†L158-L165】. Selain itu, beberapa driver Wi-Fi (misal Broadcom `wl`) tidak mendukung langkah “OWE handshake” yang diperlukan pada portal captive—mengakibatkan portal tidak pernah muncul. Beralih ke iwd (yang menangani handshake tersebut) dilaporkan memecahkan masalah ini【72†L77-L85】【72†L88-L96】.  

**Debugging:** Lakukan *packet capture* dengan `tcpdump -i <iface> port 80 or port 53` atau `iwmon --write capture.pcap` untuk melihat lalu lintas HTTP/DNS【69†L99-L101】. Aktifkan logging iwd dengan `sudo iwd -d` atau `iwctl loglevel verbose` dan pantau `journalctl -u iwd -f`【69†L47-L55】. Cek IP, gateway, DNS (`ip addr`, `ip route`, `resolvectl status`), lalu uji konektivitas dengan `ping` dan `curl` ke situs umum (misalnya `neverssl.com`).  

**Rekomendasi perbaikan:** Untuk Arch+iwd, solusi praktis termasuk menggunakan NM dengan backend iwd dan mengaktifkan pemeriksaan konektivitas (misal menambahkan:  
```ini
[connectivity]
uri=http://connectivitycheck.example.com
response=NetworkManager is online
interval=300
```  
di `/etc/NetworkManager/NetworkManager.conf`【73†L142-L150】). Ini memungkinkan GNOME/MATE/KDE membuka portal otomatis. Alternatif lain, gunakan skrip kecil yang membuka halaman HTTP (misalnya `http://neverssl.com`) setelah koneksi. Jika menggunakan DNSMASQ/iptables, redirect sementara nama uji Microsoft ke lokal atau matikan DNS-over-TLS/VPN sementara agar portal dapat diterobos.  

**Checklist pengujian:** Uji di berbagai OS (Windows 10/11, macOS 12+, Linux GNOME/KDE) dan versi browser (Chrome/Firefox) dengan portal berbasis HTTP vs HTTPS; juga uji respons jika hanya DNS yang di-*redirect*. Pastikan *auto-join* dan notifikasi jaringan aktif.  

> **Contoh skrip diagnostik (bash):**
> ```bash
> iface=wlan0
> ssid="MyPortalWiFi"
> # Hubungkan ke Wi-Fi
> iwctl station $iface connect "$ssid"
> sleep 5
> # Tampilkan alamat IP dan rute
> ip addr show $iface
> ip route
> # Uji ping dan DNS
> ping -c2 8.8.8.8
> dig +short example.com
> # Uji HTTP ke situs terbuka
> if curl -I --connect-timeout 5 http://neverssl.com | grep -q "HTTP"; then
>     echo "Internet ok"; 
> else 
>     echo "Mungkin captive portal (atau no internet)"; 
> fi
> # Cek log iwd
> journalctl -u iwd --since "5 minutes ago" | tail -20
> ```
> *Skrip di atas menghubungkan Wi-Fi, mengecek IP, jalur dan DNS, serta melakukan permintaan HTTP sebagai pemicu portal.*

## Arsitektur Koneksi Wi-Fi di Windows  
Di Windows (10/11), **Wi-Fi** dikelola oleh layanan *WLAN AutoConfig* (`wlansvc`) serta *Network Location Awareness* (NLA) melalui komponen *Network List Service*. Pengguna dapat menggunakan GUI *Network & Internet* atau perintah `netsh wlan` untuk konfigurasi SSID, profil, dan VPN. API Windows menyediakan fungsi Wlan* bagi aplikasi dan driver. Untuk deteksi captive portal, Windows mengandalkan NCSI: begitu terhubung ke SSID, Windows mengirim serangkaian *active probes*—termasuk permintaan DNS dan HTTP ke server Microsoft—untuk memverifikasi akses internet. Misalnya, sistem melakukan DNS query ke `www.msftconnecttest.com`, lalu HTTP GET ke `http://www.msftconnecttest.com/connecttest.txt`; jika teks “Microsoft Connect Test” tidak diterima, portal dianggap aktif【41†L77-L85】【19†L174-L183】. Windows juga memeriksa DNS ke `dns.msftncsi.com`【41†L85-L93】. Hasil semua tes tersebut menentukan status. Bila konektivitas gagal, Taskbar menampilkan ikon terbatas dan opsi **“Sign in to network”**; mengkliknya membuka browser (Edge) ke halaman login captive portal. Secara ringkas, Windows menggunakan tes HTTP/DNS aktif (NCSI) untuk mendeteksi portal dan menyediakan UI otomatis (toast/halaman login) bagi pengguna【41†L77-L85】【19†L174-L183】.

## Arsitektur Koneksi Wi-Fi di macOS  
Pada macOS/iOS, koneksi Wi-Fi diatur oleh *SystemConfiguration* (dengan daemon `configd`) dan framework CoreWLAN/AP. Alat userland mencakup *System Preferences > Network* dan utilitas baris perintah (`networksetup`, `airport`). Apple menyediakan API (dulu `CaptiveNetwork`, kini **NEHotspotHelper**) untuk aplikasi menangani hotspot. Setelah bergabung SSID, sistem mengirim probe HTTP ke situs tertentu (misalnya `captive.apple.com/hotspot-detect.html`) dengan konten yang telah ditentukan. Pada dokumen Apple Enterprise disebutkan `captive.apple.com` digunakan untuk “Internet connectivity validation for networks that use captive portals”【45†L136-L144】. Jika respons tidak sesuai (atau ada opsi DHCP/RA captive portal), macOS/iOS memunculkan *Captive Network Assistant*—jendela mini berbasis Safari—yang menampilkan halaman login portal. Setelah pengguna berhasil autentikasi, sistem mengulang pemeriksaan captive.apple.com dan jika valid, koneksi ditandai “online” sepenuhnya. Dengan kata lain, Apple memadukan probe HTTP (dan juga teknik DNS fallback) dan menampilkan UI login otomatis ketika mendeteksi intercept【45†L136-L144】.  

## Arsitektur iwd/iwctl vs NetworkManager/wpa_supplicant  
*Iwd* (iNet Wireless Daemon) adalah daemon Wi-Fi modern di Linux yang mengelola otentikasi 802.1X (PEAP, TLS, dll.) dan enkripsi WPA secara penuh. Iwd berjalan di userland sebagai layanan systemd, berkomunikasi dengan kernel via Netlink (nl80211) dan D-Bus. Tidak seperti `wpa_supplicant` (lama), iwd **tidak** bergantung pada OpenSSL/GnuTLS—semua operasi kriptografi dilimpahkan ke kernel【50†L47-L50】. Untuk penggunaan praktis, `iwctl` adalah CLI yang berinteraksi dengan iwd melalui D-Bus. Iwd dapat dipakai *standalone* (misalnya bersama systemd-networkd) atau sebagai backend Wi-Fi di NetworkManager (setel `wifi.backend=iwd`). Sementara `wpa_supplicant` hanya mengurus asosiasi dan handshake WPA, iwd juga mencakup fitur seperti manajemen profil tersimpan. Perbedaan utama: iwd lebih ringan, terintegrasi di ekosistem systemd, dan memiliki keamanan lebih baik pada stack TLS【50†L47-L50】.  

NetworkManager (NM) adalah daemon pengelola koneksi umum (Ethernet, Wi-Fi, VPN). NM dapat menggunakan iwd atau wpa_supplicant sebagai *supplicant* untuk Wi-Fi. Kelebihan NM: ia menyediakan deteksi konektivitas dan captive-portal (melalui pengaturan connectivity check), UI (nm-applet atau GUI DE), dan pengaturan otomatis (NetworkManager menyimpan profil Wi-Fi secara terpusat). Iwd sendiri **tidak menyediakan deteksi captive portal**; fitur semacam itu hanya ada di lapisan atas (NM/GNOME/KDE) atau aplikasi. Dengan kata lain, NM + iwd backend akan meng-handle skenario captive portal, sedangkan iwd/iwctl saja tidak.  

## Mekanisme Deteksi Captive Portal per OS  
**Windows:** NCSI menjalankan *active probing*. Setelah interface baru aktif, Windows mengirim DNS ke `www.msftconnecttest.com` dan HTTP GET ke `http://www.msftconnecttest.com/connecttest.txt` yang berisi “Microsoft Connect Test”【41†L77-L85】. Jika konten tidak sesuai atau DNS gagal (misal dialihkan portal), Windows menganggap koneksi di belakang captive portal【41†L77-L85】. Hasil pengecekan ini juga memicu ikon *“No internet”* jika gagal. Secara UX, Windows kemudian menampilkan notifikasi “Sign in to network”; jika diklik, Edge atau browser default terbuka ke URL portal.  

**macOS/iOS:** Ketika bergabung SSID, iOS/macOS membuat permintaan HTTP ke `captive.apple.com` (port 80/443) dan mengevaluasi responsnya. Dokumen Apple menyebut `captive.apple.com` dipakai untuk validasi konektivitas jaringan dengan captive portal【45†L136-L144】. Jika respons tidak diterima atau ada opsi RA/DHCP captive portal, OS menganggap ada intercept. UI-nya berupa *Captive Network Assistant*: pada iOS muncul notifikasi yang membuka sheet Safari, pada macOS muncul jendela kecil Safari otomatis【39†L432-L440】 (seperti yang digambarkan di forum pengguna). Setelah login, sistem mengecek ulang ke captive.apple.com.  

**Linux (dengan desktop):** Banyak DE modern (GNOME 3.14+, KDE Plasma) mendukung captive portal via NetworkManager. NM mengirim permintaan ke URL uji (misalnya `http://connectivitycheck.example.com`) secara berkala. Anda dapat mengkonfigurasi URL dan teks respons di `/etc/NetworkManager/NetworkManager.conf`【61†L17-L25】【73†L142-L150】. Jika pemeriksaan gagal (tidak mendapat teks “online”), NM secara otomatis membuka dialog login portal【61†L17-L25】【61†L28-L32】. Contoh konfigurasi NM:  
```ini
[connectivity]
uri=https://connectivitycheck.ubuntu.com
response=NetworkManager is online
interval=300
```  
(Waktu interval dan teks disesuaikan【73†L142-L150】.) Dengan pengaturan tersebut, GNOME/KDE akan membuka halaman login ketika tes koneksi gagal【61†L17-L25】【61†L28-L32】.  

**Linux (iwd mandiri):** Iwd/iwctl tidak memiliki deteksi portal sendiri. Jika hanya menggunakan iwd (tanpa NM), pengguna biasanya melakukan trik manual: membuka halaman HTTP publik (contoh `http://neverssl.com`), menggunakan browser biasa, atau utilitas seperti *captive-browser*. Tanpa skrip eksternal, sistem tidak memberi peringatan otomatis. Sebagai contoh, pengguna Firefox dapat mengaktifkan `network.captive-portal-service.enabled` di about:config untuk deteksi Portal【71†L142-L150】.  

## Masalah Umum & Laporan Bug (iwd/iwctl dan Captive Portal)  
Banyak pengguna melaporkan iwd tidak memunculkan portal login secara otomatis. Karena iwd sendiri hanya menyediakan koneksi layer link/data, desktop (NM/GNOME) yang seharusnya mendeteksi captive portal kadang mengabaikannya. Di Arch forum dan Reddit, saran umum adalah menggunakan helper eksternal: misalnya paket *captive-browser* (AUR) yang membuka Chromium/Vivaldi untuk login【71†L158-L165】, atau menonaktifkan sementara iwd dan beralih ke NM default. Sebagian pengguna Windows dan macOS terbiasa punya notifikasi portal, sehingga menghadapi iwd standalone terasa “kurang otomatis”.  

Selain itu, ada laporan khusus: driver Wi-Fi Broadcom legacy (termasuk modul `wl`) **tidak mendukung handshake OWE (Opportunistic Wireless Encryption)** yang digunakan oleh banyak hotspot publik. Akibatnya, meski SSID terbuka, koneksi tidak benar-benar selesai, sehingga portal tidak pernah muncul. Seorang pengguna EndeavourOS mencatat bahwa setelah mengganti wpa_supplicant dengan iwd, perangkatnya (Broadcom BCM4360) akhirnya berhasil melakukan handshake dan baru kemudian portal login muncul【72†L77-L85】【72†L88-L96】. Ini menunjukkan bahwa *masalah kartu jaringan* kadang-kadang, bukan hanya perangkat lunak, dapat menghambat captive portal.  

## Langkah Debugging Praktis (Arch + iwd)  
1. **Periksa Koneksi Fisik dan IP:** Gunakan `iwctl device list` dan `iwctl station <iface> get-networks` untuk memastikan SSID terlihat. Sambungkan dengan `iwctl station <iface> connect <SSID>`. Setelah itu, cek `ip addr show <iface>`, `ip route`, dan `systemd-resolve status` untuk memeriksa alamat IP, gateway, dan DNS.  
2. **Cek DNS & Ping:** Tes konektivitas dasar dengan `ping 8.8.8.8` dan `dig example.com`. Jika ping ke IP berhasil tapi ke domain gagal, masalah mungkin DNS atau captive. Perhatikan DNS default (kadang portal menetapkan DNS lokal).  
3. **HTTP Probes Manual:** Jalankan `curl -I http://neverssl.com` atau `curl -I http://www.msftconnecttest.com/connecttest.txt`. Respons tidak terduga (redirect ke portal, kode 30x, atau tidak ada respons) menunjukkan captive portal. Hindari HTTPS (portal sering sengaja hanya hijack HTTP).  
4. **Packet Capture:** Gunakan `sudo tcpdump -i <iface> port 80 or port 53 -w capture.pcap` untuk merekam lalu lintas ke portal. Di PC, analisis paket ini dapat mengungkap paket yang dialihkan ke router portal (IP lokal). Atau gunakan `iwmon --write` sebelum menjalankan `iwctl`; `iwmon` menangkap frame Wi-Fi/EAPOL serta traffic layer atas【69†L99-L101】.  
5. **Log iwd:** Tingkatkan verbositas iwd dengan menjalankan `sudo iwd -d` (atau `iwctl loglevel debug`) dan lihat output di terminal. Atau pantau `journalctl -u iwd -f` setelah koneksi untuk pesan kesalahan (mis. kegagalan 4-way handshake, timeout). Variabel lingkungan seperti `IWD_TLS_DEBUG=1` bisa diaktifkan jika masalah EAP/TLS ditemukan【69†L72-L80】.  
6. **Debug Netlink/EAP:** `iwmon` juga bisa dijalankan (sebelum iwd) untuk melihat paket NL80211/EAPOL lengkap【69†L77-L80】. Ini membantu memeriksa apakah iwd *benar-benar* berhasil berasosiasi ke AP sebelum portal.  
7. **Pantau Proses Browser:** Kadang portal harus diload dalam browser khusus. Pastikan tidak ada VPN atau proxy yang memblokir koneksi HTTP lokal. Dalam kasus kegagalan TLS (mis. captive melempar sertifikat sendiri), cek peringatan browser—ini juga indikator portal aktif.  

Tabel berikut meringkas langkah debug utama:

| Langkah                          | Perintah (contoh)                                      | Keterangan                              |
|----------------------------------|--------------------------------------------------------|-----------------------------------------|
| Cek antarmuka & jaringan Wi-Fi   | `iwctl station wlan0 scan`<br>`iwctl station wlan0 get-networks` | Pastikan SSID muncul dan dipilih.       |
| Hubungkan Wi-Fi                  | `iwctl station wlan0 connect <SSID>`                   | Lihat output status koneksi.            |
| Periksa alamat IP & rute         | `ip addr show wlan0`<br>`ip route`                     | Periksa IP, gateway, DNS.              |
| Tes koneksi Internet             | `ping -c 2 8.8.8.8`<br>`dig @1.1.1.1 example.com`       | Jika gagal tapi Wi-Fi terhubung, tandanya captive portal. |
| Tes HTTP (probes)                | `curl -I http://example.com`<br>`curl -I http://www.msftconnecttest.com/connecttest.txt` | Lihat apakah dialihkan/404.            |
| Tangkap paket HTTP/DNS           | `sudo tcpdump -i wlan0 port 80 or port 53`             | Tangkap untuk analisis portal hijack.    |
| Aktifkan debug iwd               | `sudo iwd -d` atau `iwctl loglevel debug`              | Munculkan pesan debug iwd lengkap【69†L47-L55】. |
| Periksa log systemd              | `journalctl -u iwd -f --since "10 minutes ago"`        | Lihat error handshake atau DHCP.        |
| Analisis NL80211/EAP (Wireshark) | `sudo iwmon --write /tmp/iwd.pcap`                      | Tangkap raw frame Wi-Fi/EAPOL【69†L99-L101】. |

## Rekomendasi Perbaikan / Workarounds  
- **NetworkManager + iwd backend:** Cara tersederhana adalah mengaktifkan NM dengan `wifi.backend=iwd` dan konfigurasi *connectivity check* seperti di atas【73†L142-L150】. Ini akan memicu GNOME/KDE membuka portal secara otomatis.  
- **Ubah URL Deteksi:** Sesuaikan URL uji (misal `connectivitycheck.gstatic.com` atau `neverssl.com`) dan respons yang diharapkan di `NetworkManager.conf`【73†L142-L150】. Misalnya gunakan server yang mudah diakses untuk memastikan NM mendeteksi kegagalan koneksi.  
- **Script/Udev Hook:** Tambahkan skrip yang dijalankan setelah koneksi (via systemd units atau NM dispatcher) untuk memeriksa akses HTTP. Jika gagal, jalankan `xdg-open http://neverssl.com` atau luncurkan browser kecil. Paket seperti *capnet-assist* (Arch Wiki) atau *captive-browser* (AUR) dapat membantu.  
- **DNS/iptables:** Sebagai terakhir, dapat diterapkan aturan iptables/dnsmasq lokal untuk memblokir *NCSI* Microsoft (misal redirect `www.msftconnecttest.com` ke localhost) sehingga Windows tidak “online”. Hal ini paksa Windows selalu memunculkan portal login.  
- **Disable Intercept:** Jika menggunakan proxy atau VPN (termasuk DNS-over-TLS), matikan dahulu agar paket HTTP ke login portal tidak terhalang (Cloudflare menyarankan melepas DoH/DoT saat portal【42†L1171-L1174】).  
- **Auto-Join:** Pastikan opsi “Automatically join this network” aktif (terutama di macOS) agar perangkat tidak gagal terhubung ulang【42†L1230-L1238】. Pada Linux, centang “Connect automatically” di pengaturan NM.  

## Contoh Skrip Diagnostik & Saran Patch iwd  
Berikut contoh skrip diagnostik sederhana (bash) untuk menguji dan merekam perilaku captive portal di Arch+iwd:  
```bash
#!/bin/bash
iface=wlan0; ssid="WiFi_Portal"; url="http://captive.apple.com"
iwctl station $iface connect "$ssid"
sleep 5
echo "Alamat IP:"; ip addr show $iface | grep inet
echo "Rute:"; ip route
echo "DNS:"; resolvectl status | grep "DNS Servers"
echo "Tes ping:"; ping -c1 8.8.8.8 || echo "No ICMP"
echo "Tes HTTP:" 
curl -IsL --connect-timeout 5 $url | head -n1
```
Skrip di atas menghubungkan SSID, menampilkan info IP/DNS, dan menguji HTTP ke `captive.apple.com`. Outputnya membantu menentukan apakah portal merespon (misal jika dialihkan ke login).

**Saran patch iwd:** Karena iwd belum mendukung captive portal, pengembang bisa menambahkan logika pemeriksaan konektivitas setelah DHCP selesai. Misalnya, setelah `StationConnected` event (kode di `main.c`), iwd dapat melakukan HTTP GET ke URL uji (mirip NM) dan jika tidak `200 OK`, menerbitkan sinyal D-Bus baru seperti `CaptivePortalDetected`. Atau, iwd bisa mengekspor status baru di D-Bus untuk diantisipasi NM/DE. Tidak ada patch resmi saat ini, tetapi ide utamanya adalah mengintegrasikan pemeriksaan HTTP sederhana ke dalam loop iwd.

## Tabel Perbandingan Perilaku Captive Portal Antara OS  

| Sistem Operasi  | Metode Probe HTTP/DNS                     | Logika Deteksi                         | Tampilan UX (Autentikasi)                                     |
|-----------------|-------------------------------------------|----------------------------------------|---------------------------------------------------------------|
| **Windows 10/11** | HTTP GET ke `msftconnecttest.com/connecttest.txt` (konten *“Microsoft Connect Test”*); DNS ke `dns.msftncsi.com` | Jika teks tidak sesuai atau DNS gagal, tandai captive portal【41†L77-L85】. | Ikon jaringan menampilkan “No Internet” atau “Sign in to network”; membuka browser (Edge) ke halaman login. |
| **macOS/iOS**   | HTTP GET ke `captive.apple.com/hotspot-detect.html` (konten “Success”); juga deteksi via DHCP Option 114/RA Captive【45†L136-L144】 | Jika respons “Success” tidak diterima (atau ada opsi RA khusus), tangkap captive portal. | Membuka *Captive Network Assistant* (jendela mini Safari) untuk login portal. |
| **Linux + NM (GNOME/KDE)** | HTTP ke URL uji (konfigurasi NM, misal `connectivitycheck.ubuntu.com`); teks “NetworkManager is online” | Jika permintaan gagal atau konten berbeda, anggap captive portal【61†L17-L25】. | NM menampilkan dialog/warn popup; atau membuka browser login secara otomatis (sesuai DE). |
| **Linux + iwd (murni)** | *Tidak ada probe otomatis* (iwd tidak menyediakan) | Pengguna harus memicu manual (misal buka `neverssl.com`) untuk mengakses login. | Tidak ada notifikasi otomatis. Portal harus dibuka secara eksplisit oleh pengguna di browser. |

## Tabel Langkah Debugging Captive Portal di Arch+iwd  

| Langkah                            | Perintah / Peralatan                                        | Keterangan                                |
|------------------------------------|-------------------------------------------------------------|-------------------------------------------|
| **1. Cek Wi-Fi Interface**         | `iwctl device list`<br>`iwctl station wlan0 get-networks`    | Pastikan perangkat Wi-Fi terdeteksi dan SSID terlihat. |
| **2. Hubungkan ke Wi-Fi**          | `iwctl station wlan0 connect <SSID>`                        | Lihat status koneksi; tunggu proses DHCP. |
| **3. Cek IP & Rute**               | `ip addr show wlan0`<br>`ip route`                           | Periksa alamat IP, gateway, dan DNS server. |
| **4. Uji Konektivitas**            | `ping -c2 8.8.8.8`<br>`ping -c2 google.com`                  | Jika ping IP sukses tapi nama gagal, portal mungkin aktif. |
| **5. Uji DNS/HTTP**                | `dig example.com`<br>`curl -I http://neverssl.com`           | Tangkap output HTTP (kode 200/redirect). |
| **6. Packet Capture**              | `sudo tcpdump -i wlan0 port 80 or port 53`                  | Rekam trafik portal (lihat IP redirect).  |
| **7. Logging iwd**                 | `sudo iwd -d` (atau `iwctl loglevel debug`)                  | Aktifkan debug iwd【69†L47-L55】.          |
| **8. Periksa Log Systemd**         | `journalctl -u iwd -f`                                       | Pantau error/tanda gagal handshake DHCP/EAP. |
| **9. Debug Netlink/EAP (Wireshark)** | `sudo iwmon --write /tmp/iwd.pcap`                           | Simpan frame Wi-Fi/EAPOL untuk analisa【69†L99-L101】. |

## Diagram Alur Deteksi Captive Portal  

```mermaid
flowchart TD
    A[Terhubung ke Wi-Fi AP] --> B{Lakukan probe konektivitas}
    B -->|HTTP probe| C{Respons sesuai?}
    C -->|Ya| D[Koneksi normal]
    C -->|Tidak| E[Deteksi Captive Portal]
    E --> F[Buka UI Captive Portal (browser) ]
    F --> G[Pengguna login/terotentikasi]
    G --> H[Koneksi di-recheck]
    H -->|Berhasil| D
    H -->|Gagal lagi| E
```

**Garis Besar:** Setelah sambung ke AP, sistem melakukan *probe* (HTTP/DNS) ke server tepercaya. Jika hasilnya normal, koneksi internet digunakan seperti biasa. Jika hasil uji mengindikasikan intercept (redirect atau gagal), sistem menandai **captive portal** dan membuka antarmuka login otomatis (khususnya di Windows/macOS/GNOME). Setelah pengguna berhasil login di portal, probe diulangi; jika sukses, koneksi dinyatakan terhubung.

## Referensi  
Sumber primer dan dokumentasi resmi terkait digunakan untuk setiap bagian di atas: Microsoft (NCSI)【41†L77-L85】【19†L174-L183】, dokumentasi Apple【45†L136-L144】, wiki/arsip iwd【69†L47-L55】, forum Arch Wiki/StackExchange【61†L17-L25】【73†L142-L150】, serta laporan dan thread pengguna (Arch, Reddit)【50†L47-L50】【71†L142-L150】. Semua kutipan disajikan sesuai format sumber.

