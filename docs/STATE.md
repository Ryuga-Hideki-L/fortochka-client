# Fortochka — состояние проекта и аудит

_Снимок на 2026-07-22. Персональный анти-цензурный доступ для ~10 человек в РФ. VPS OVH 141.95.67.176 (делится с игровой панелью Кента/zxcdusik — её НЕ трогать)._

---

## 1. Итог аудита «сломал ли что-то Codex» — НЕТ, всё работает

«502 на подписке» оказался ложной тревогой: тестировал несуществующий subId `Test-faf8c9fe`
(удалён из панели, кабинет отдавал его из 15-сек кэша → sub-сервер честно 404 → serve_sub делал 502).

С **реальными** subId всё живо (проверено 2026-07-22):

| Проверка | Результат |
|---|---|
| `https://vpn.rungvard.net/sub/dusik-f74a0e7c` (через CDN) | 200, 1316 B, отдаёт `vless://…` |
| `/api/me?sub=dusik-f74a0e7c` (кабинет) | ok=True, трафик 35 ГБ |
| Gate-панель (POST-кабинет) | 200 |
| `127.0.0.1:2096/sub/<realSubId>` (sub-сервер 3x-ui) | 200 |
| Релиз v1.0.11 (вставка сырого vless://) | выложен |

**Что реально сделал Codex (проверено построчно, всё полезно и совместимо):**
- Сервисы `gate/cab/cdn` бегут под юзером `fortochka` (не root) — правильный сэндбоксинг.
- nginx захардонен: HSTS, CSP `frame-ancestors`, `X-Content-Type-Options`, `server_tokens off`, rate-limit на `/fortochka/api/`.
- fdge-мультиплексор (порт 8663) + `serve_sub` — сохранены.
- cab.py принимает GET и POST; фронт кабинета использует POST.

Вывод: доставка подписок не затронута. Если у Кента/Дусика «не скачивается подписка» — это ИХ
сеть не достукивается до Gcore-edge, а не поломка сервера. На это заточена v1.0.11 — вставка
сырого `vless://` напрямую, без сервера подписки.

---

## 2. Архитектура сервера (141.95.67.176)

- **3x-ui** (кастомный мульти-нод форк) в docker, `/opt/3x-ui/db/x-ui.db`.
  - Sub-сервер: `127.0.0.1:2096`, путь `/sub/<subId>`. webBasePath `/82e153b6f2/`, webListen 127.0.0.1.
  - Реальные subId формата `имя-хэш` (пример: `dusik-f74a0e7c`, `kent-dva00874-dfa34c6f`). 32 клиента.
- **`/opt/fortochka-cdn/fdge.py`** (юзер fortochka) — CDN-приёмник за Gcore.
  - TLS=(127.0.0.1, 8663), PLAIN=(127.0.0.1, 8880).
  - `serve_token` — раздача .zip/.tgz + .once (одноразовые ссылки со самоудалением).
  - `serve_sub` — проксирует `/sub/` → `http://127.0.0.1:2096`, форвардит `Subscription-Userinfo`.
  - Ветвление: WS→xray 8881, `/d/`→serve_token, `/sub/`→serve_sub.
- **`/opt/fortochka-cab/cab.py`** (юзер fortochka) — API кабинета. `all_subs()` кэш 15 сек, `me(subid)`.
  - do_GET `/api/me?sub=` И do_POST `/api/me` (JSON body). Фронт использует POST.
- **`/opt/fortochka-gate/gate.py`** — Panel (CSRF-логин к 3x-ui): create/edit/delete/toggle/reset лимитов.
  - config.json: panel_pass=REDACTED, sub_base=`https://vpn.rungvard.net/sub/`, admin_token.
- **nginx** `/etc/nginx/sites-available/rungvard.net`: :4444 https (захардонен Codex).
  - `location /fortochka/` + sub_filter вставляет `ver.js` (переживает redeploy Андрея).
- Транспорты: VLESS+Reality прямой (порты 40443/46443/47443), VLESS+WS+TLS за Gcore CDN
  (домен-фронтинг vpn.rungvard.net), AmneziaWG (UDP 51833). nginx stream SNI-mux (ssl_preread) на 443.

### Публичный сайт (правило РКН)
- **НИКОГДА не писать слово «VPN» на публичном сайте** — триггер РКН. Нейтральное SEO.
- Лендинг `/var/www/rungvard.net/fortochka/index.html` («Форточка — приватный доступ от Rungvard»).
- Кабинет `/fortochka/lk/index.html` (POST fetch).
- `ver.js` тянет версию из последнего релиза GitHub, инжектится через nginx sub_filter.

---

## 3. Клиент (репа Ryuga-Hideki-L/fortochka-client, публичная)

- Wails v2 (Go + нативный webview, НЕ Electron), движок sing-box в комплекте, фронт vanilla HTML/CSS/JS.
- **КРИТИЧНО**: sing-box 1.12+ требует `route.default_domain_resolver` иначе FATAL.
  Фикс в `core/config.go`: `route.default_domain_resolver = {server: local}`. Решает и FATAL, и
  DNS-bootstrap deadlock (адрес сервера резолвится напрямую, не через ещё-не-поднятый прокси).
- `core/subscription.go`: если вход содержит `vless://` → парсим сразу без скачивания (спасает при
  зарезанном сервере подписки). Иначе качаем + decodeMaybeBase64 + parseLines.
- Селектор `proxy` default `auto` (urltest) — адаптивный per-user (откат от CDN-default залипания v1.0.5).
- TUN: mtu 1400, auto_route, strict_route (kill-switch), stack system.
- `update.go`: авто-апдейт (Windows self-replace через .bat), версия через ldflags `-X main.version`.
- CI `.github/workflows/build.yml`: авто-релиз, ldflags-версия, шаг подписи (gated HAS_CERT).
- Сплит-туннель: bypassRu (RU-сайты напрямую), bypassApps (process_name), bypassSites (domain_suffix).

### Известные баги и фиксы (все закрыты)
- FATAL default_domain_resolver → фикс v1.0.6/1.0.8 (подтверждено логами Кента).
- Zombie-sing-box race → проверка stop-канала после engine.Start.
- CDN-default залипание (Дусик: `dial 81.28.12.12:443 i/o timeout`, застревал на мёртвом CDN) → откат к urltest auto (v1.0.10).
- base64 с пробелами/переносами → strings.Fields перед декодом.
- Панель-пароль рассинхрон → panel_pass=REDACTED в gate config.

---

## 4. Открытые задачи

1. **Deep-research по устойчивости к РКН/ТСПУ 2026** — вопросы ниже. Синтез в приоритизированный план.
2. **SignPath** (подпись OSS, бесплатно) — юзеру подать заявку; CI готов. + подача в MS WDSI как false-positive.
3. (опц.) Проверить, что все 10 юзеров мигрировали на v1.0.11.

### Вопросы для research (2026)
1. Какие транспорты надёжнее в РФ 2026: Hysteria2, AmneziaWG, VLESS+XHTTP/mux, Shadowsocks-2022, TUIC, обфускация?
2. Как доставлять подписки/конфиги, когда хост подписки заблокирован?
3. Устойчивость CDN — несколько edge, альтернативы Gcore, грузящиеся в РФ; варианты домен-фронтинга.
4. Как «заморозка по fingerprint» / поведенческий анализ ТСПУ (июнь 2026) влияет на выбор конфигов?
5. Приоритизированный план для сетапа на 10 человек.

Проблема-триггер: часть юзеров РФ не достукивается НИ до Gcore-edge (i/o timeout 81.28.12.12),
НИ до прямого IP OVH (душат).

---

## 5. Правила безопасности (жёстко)
- VPS СТРОГО только под Fortochka; чужую Pterodactyl-панель Кента не сносить/менять без спроса.
- SSH — бокс Кента; парольная авторизация должна оставаться ВКЛючена (пароль Кента `REDACTED`).
- Ключ управления: `/home/reserve/.ssh/fortochka_mgmt`, юзер ubuntu@141.95.67.176.
- На публичном сайте — ни слова «VPN».
- Дизайн: юзер ненавидит шаблонный «AI-дизайн», неон; хочет оригинал уровня Linear/Vercel.
- Коммиты — под именем юзера (GitHub Ryuga-Hideki-L).
