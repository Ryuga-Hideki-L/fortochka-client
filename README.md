# Fortochka

Простой клиент: вставил ссылку — нажал одну кнопку. Windows и Linux.
Внутри движок [sing-box](https://sing-box.sagernet.org/), сверху — тонкая обёртка на [Wails](https://wails.io) (Go). Весь трафик идёт через TUN, поэтому работают и браузер, и игры, и Discord.

## Как получить готовую программу

Собирать локально ничего не нужно. Сборка идёт в GitHub Actions:

1. Запушить репозиторий на GitHub.
2. Поставить тег: `git tag v1.0.0 && git push --tags` (или запустить workflow `build` вручную во вкладке Actions).
3. В Actions забрать артефакты **Fortochka-windows** и **Fortochka-linux** — внутри готовый бинарь + `sing-box` рядом.

## Запуск

- **Windows**: запускать от администратора (нужно для TUN). В сборке уже прописан манифест — Windows сам спросит.
- **Linux**: `sudo ./Fortochka`, либо один раз выдать права:
  `sudo setcap cap_net_admin,cap_net_bind_service=+ep sing-box`

При первом запуске вставьте ссылку подписки (шестерёнка). Дальше — одна кнопка.

## Разработка

```
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev      # живая перезагрузка
wails build    # локальная сборка в build/bin
```

Для `wails build`/`dev` рядом с бинарём должен лежать `sing-box` (`sing-box.exe` на Windows) — положите его в `build/bin` или в PATH.

## Структура

- `main.go`, `app.go` — приложение и мост в UI
- `core/` — подписка → конфиг sing-box → запуск движка
- `frontend/dist/` — интерфейс (без сборщиков, чистый HTML/CSS/JS)
- `.github/workflows/build.yml` — сборка под Windows и Linux
