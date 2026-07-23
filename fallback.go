package main

import (
	"encoding/base64"
	"strings"

	"fortochka/core"
)

// fallbackSub — base64 резервной подписки (прямые vless/hysteria2/tuic ссылки),
// инжектится в бинарь через ldflags -X main.fallbackSub из GitHub-secret.
// В ПУБЛИЧНОМ ИСХОДНИКЕ пусто — чтобы креды не утекли. Пусто = резерва нет.
var fallbackSub string

// embeddedFallback — распарсенный встроенный резерв. Используется, когда живую
// подписку скачать не удалось (Gcore лёг / домен зарезан).
func embeddedFallback() []core.Profile {
	if fallbackSub == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(fallbackSub))
	if err != nil {
		return nil
	}
	p, _ := core.FetchProfiles(string(raw))
	return p
}
