# Что мы упускаем — разбор против других VPN (2026)

_Собрано Haiku-агентами (IniX-VPN + зрелые VPN-проекты). Полный синтез — в workflow. Здесь ключевые пробелы против нашего сетапа._

## IniX-VPN (spaiK111) — молодой personal-проект (0 звёзд, июль 2026)
- База: **Remnawave** (распределённая: Panel+Node+Subscription, не монолит) + Xray-core.
- 4 протокола сразу: VLESS+Reality:443, Hysteria2:8880, Trojan+TLS:2096, Shadowsocks:8388.
- Клиент — НЕ свой: Mihomo/Clash Meta по YAML-шаблону + стандартные (v2rayN/Hiddify).
- Доставка: subscription page на поддомене sub. + Cloudflare Zero Trust.
- Интересное: **RU Zapret blocklist** (ежедневный cron, blackhole РКН-доменов — чтобы снизить привлекательность для DPI-зондов); **Torrent Blocker** (nftables, снижает DPI-сигнатуры); GitOps CI/CD (push→deploy).
- НЕТ: port-hopping, ротации доменов, WARP, auto-failover между протоколами, домен-фронтинга.
- Вывод: у нас архитектура сопоставима/лучше (свой клиент + авто-failover + UDP). Полезное к заимствованию: **Zapret-blackhole доменов** (снизить probing), распределённость.

## Ключевые ПРОБЕЛЫ у нас (против зрелых: Hiddify/Xray/Amnezia/Hysteria2)

### Критично (бьёт по нашей же проблеме)
1. **Единый CDN (Gcore) = точка отказа.** Best-practice 2026: **multi-CDN failover** (Cloudflare + CloudFront/Bunny/Fastly, health-probes). Сегодняшний Gcore-обвал — ровно это. → нужен 2-й CDN или прямой fallback доставки.
2. **Sub-URL зашит один.** Если Gcore-домен заблокят — клиент кирпич. Best-practice: **встроенные fallback-адреса / несколько доменов / signed-config с ротацией** (паттерн Умного Голосования: signed JSON + low-TTL + DoH + SNI-faking).
3. **Нет port-hopping (Hysteria2/TUIC).** Клиент берёт случайный UDP-порт из диапазона и прыгает; сервер nftables-редиректит диапазон на listen-порт. Обходит **per-port UDP-throttling** — вероятная причина смерти у Сергея (UDP:443 душат). Hysteria2 умеет НАТИВНО. **Дёшево, высокий эффект.**
4. **Один сервер = один IP = одна точка блокировки.** Зрелые — multi-node.

### Средне
5. **TLS-fragment** (разбить ClientHello, DPI не читает SNI). sing-box (dial fragment) и Xray умеют. Помогло бы ws/Reality пережить SNI-детект.
6. **WARP-in** (Cloudflare WireGuard): выход через residential Cloudflare-IP, прячет datacenter-IP OVH (который и душат по «Сигналу 1»). VLESS+Reality+WARP — «highest survival rate» в РФ/Иране/Китае 2026.
7. **AmneziaWG 2.0** маскируется под QUIC/DNS (обновление 2025-2026) — сильнее нашей salamander. У нас AWG только в отдельном приложении.
8. **Brutal congestion** (Hysteria2) — держит скорость на потерях. Тюнинг.

### Проверить на КОСЯКИ у нас
- **uTLS-фингерпринт**: в 2026 палят по **JA4** (нормализует порядок, рандомизация не спасает). Проверить, что fp реально имитирует браузер, а не палёный.
- **Reality decoy-SNI**: используем `api-maps.yandex.ru` — должен реально отвечать 200/подходить; проверить.
- **MTU** 1400 — best-practice для Reality/WG; ок, но перепроверить под QUIC.
- **DoH**: у нас есть (1.1.1.1 через прокси) — ок. Но сам sub-fetch через DoH-резолв нескольких эндпоинтов — нет.
- **Health-monitor**: у нас есть (healthLoop 30с + реконнект) — ок.

## Что уже хорошо у нас (не хуже зрелых)
- Свой клиент с **авто-failover** (urltest) — в базовых панелях (3x-ui/Marzban/Hiddify) этого НЕТ, нужен external tool.
- **2 UDP-протокола** (hy2+tuic) + CDN + мультиплекс.
- Диагностика через clash_api (активный канал, задержки) — редкость.
- Ретрай подписки, kill-switch (strict_route), сплит-туннель.

## Приоритет действий (предварительно, до синтеза Sonnet)
1. **Port-hopping для Hysteria2** — nftables-редирект диапазона + client hop. Спасает UDP-throttled (Сергей). Дёшево.
2. **Второй путь доставки подписки** — минимум прямой origin-fallback (в обход Gcore) или второй CDN.
3. **Fallback-адреса в клиент** — чтобы блок sub-URL не убивал всех.
4. **TLS-fragment** в sing-box клиенте.
5. **WARP-in** для скрытия datacenter-IP.
6. Заимствовать **Zapret-blackhole** (снизить DPI-probing на ноду).
