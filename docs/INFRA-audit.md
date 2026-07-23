# Аудит инфраструктуры — сервер + OVH + Namecheap + (Gcore pending)

_2026-07-23. Живой аудит VPS + доки провайдеров (Haiku). Gcore/Zapret — в работе._

## Живой аудит сервера 141.95.67.176 (read-only)
**Хорошо (косяков нет):**
- Congestion **BBR + fq**; UDP-буферы `rmem/wmem_max=64MB`; TCP Fast Open вкл.
- conntrack 161/262144 (запас); сертификаты до октября 2026.
- Хостовых rate-лимитов на сервисы нет; Tor-выход заблокирован.

**Косяки/риски:**
1. **Reality decoy SNI = `api-maps.yandex.ru`** на ВСЕХ прямых инбаундах (Vision/gRPC/XHTTP).
   Российский домен на французском IP OVH → несоответствие SNI↔гео, палится поведенческим ТСПУ.
   Decoy сам по себе TLS 1.3 отвечает (валиден), но лучше иностранный (microsoft/cloudflare). **Пересмотреть.**
2. **UDP receive-buffer errors ~27000** (`UdpRcvbufErrors`) — часть UDP дропается на приёме. Бьёт по hy2/tuic/WG.
   Тюнинг: поднять `rmem_default`, буферы сервисов (`net.core.rmem_default`, per-socket SO_RCVBUF).

## OVH — КРИТИЧНО
1. **UDP fragmentation DROP по умолчанию на Edge Network Firewall!** Прямо режет фрагментированные
   UDP (Hysteria2/TUIC/AmneziaWG). → нельзя допускать фрагментацию UDP: держать QUIC-пакеты < path MTU,
   явно задать MTU/размер пакета в hy2/tuic, проверить что фрагментов нет. **Проверить/настроить.**
2. **VAC (anti-DDoS)** может ложно срабатывать на «много мелких UDP от одного IP» (паттерн прокси) → скраббинг/троттлинг.
3. **Additional IP** — можно докупить (разные страны ЕС) для мульти-IP. НО блок может быть на уровне AS OVH целиком →
   тогда второй OVH-IP не спасёт; лучше **второй эндпоинт у ДРУГОГО провайдера**.
4. Основной интерфейс MTU 1500 (ок), TUN 1400 (ок). Проверить PTR/reverse DNS (репутация).
   Источники: docs.ovhcloud.com (firewall-network, vps-faq, buy-additional-ip, configuration-change-mtu-size).

## Namecheap (домен/DNS)
- **Нет** встроенного DNS-failover и round-robin. PremiumDNS Anycast — только uptime самого DNS, не failover origin.
- **TTL мин 60с**; wildcard-поддомены (`vpn1.`,`vpn2.`…); DDNS (только IPv4).
- **API `domains.dns.setHosts`** для смены записей скриптом — РАБОТАЕТ ТОЛЬКО на **BasicDNS** (не PremiumDNS).
  → можно автоматизировать ротацию A-записи/поддомена при блокировке (свой скрипт-монитор).
- Настоящий мульти-CDN failover Namecheap сам не даёт — нужен CDN-уровень или скрипт-переключатель по health-check.
  Источники: namecheap.com/support/api, knowledgebase (roundrobin, host-records, dynamic-dns, dns-limits).

## Gcore CDN — почему флапает
- **Free-tier: лимиты не документированы, вероятно rate-limit + агрессивная L7 DDoS-фильтрация** нашего
  «подозрительного» трафика (long-lived WS, высокий RPS) → 429/503/обрывы. Это наиболее вероятная причина флапа.
- **Нет origin shielding на free** → прямой удар по origin, при нагрузке нестабильно.
- НО Gcore умеет **origin groups + failover (`use_next`)** — можно задать backup-origin (если будет 2-й сервер),
  Gcore сам переключится при fail. Требует настройки ресурса (Terraform/API).
- Origin protocol должен быть HTTPS (у нас так). WebSocket поддерживается.
- API `/cdn/public-ip-list` (без auth) — список edge-IP. Для purge/переустройства нужен API-токен (у нас нет в сессии).
- Вывод: **Gcore Free — слабое звено**. Варианты: платный tier (снять фильтрацию/добавить shielding),
  ИЛИ второй CDN (Cloudflare/Bunny), ИЛИ снизить зависимость доставки от Gcore (origin-fallback/встроенные адреса).
  Источники: docs.gcore.com/cdn (cdn-resource-options, terraform, troubleshooting), gcore.com/cdn.

## Выводы для плана (инфра-часть)
- **UDP-фрагментация OVH** — проверить, не режет ли она hy2/tuic (возможная причина смертей у Сергея). Дёшево.
- **UDP rcvbuf тюнинг** — поднять буферы.
- **Reality decoy** — сменить на иностранный.
- **Второй эндпоинт у не-OVH провайдера** (против AS-блока) + **свой DNS-ротатор через Namecheap API** (BasicDNS).
- Multi-CDN на уровне DNS Namecheap не выйдет автоматом — либо второй CDN + скрипт, либо origin-fallback в клиент.
