package core

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Split — настройки раздельного туннелирования (что идёт мимо Форточки).
type Split struct {
	BypassRu bool     // РФ-домены напрямую
	Apps     []string // имена процессов мимо туннеля (chrome.exe)
	Sites    []string // домены мимо туннеля (example.com)
}

// BuildConfig собирает конфиг sing-box из профилей подписки:
// TUN (весь трафик), авто-выбор лучшего сервера по задержке, обход локалки,
// плюс раздельное туннелирование по приложениям/сайтам/РФ-доменам.
func BuildConfig(profiles []Profile, sp Split) ([]byte, error) {
	var outbounds []map[string]any
	var tags []string
	var wsTag string // CDN-профиль (ws) — он надёжен в РФ, делаем его дефолтным
	used := map[string]bool{}

	for i, p := range profiles {
		if p.Net == "xhttp" { // движок sing-box не поддерживает xhttp
			continue
		}
		tag := p.Name
		if tag == "" || used[tag] {
			tag = fmt.Sprintf("%s-%d", p.Net, i)
		}
		used[tag] = true

		ob := map[string]any{
			"type":        "vless",
			"tag":         tag,
			"server":      p.Server,
			"server_port": p.Port,
			"uuid":        p.UUID,
		}
		if p.Flow != "" {
			ob["flow"] = p.Flow
		}

		tls := map[string]any{
			"enabled":     true,
			"server_name": nonEmpty(p.SNI, p.Server),
			"utls":        map[string]any{"enabled": true, "fingerprint": p.FP},
		}
		if p.Sec == "reality" {
			tls["reality"] = map[string]any{
				"enabled":    true,
				"public_key": p.PBK,
				"short_id":   p.SID,
			}
		}
		ob["tls"] = tls

		switch p.Net {
		case "ws":
			t := map[string]any{"type": "ws", "path": p.Path}
			t["headers"] = map[string]any{"Host": nonEmpty(p.Host, p.Server)}
			ob["transport"] = t
			if wsTag == "" {
				wsTag = tag // первый CDN-профиль → дефолт
			}
		case "grpc":
			ob["transport"] = map[string]any{"type": "grpc", "service_name": def(p.Service, "grpc")}
		}

		outbounds = append(outbounds, ob)
		tags = append(tags, tag)
	}

	if len(tags) == 0 {
		return nil, errors.New("нет профилей, совместимых с движком")
	}

	// По умолчанию — CDN-профиль (надёжен в РФ, подключение сразу, без ожидания
	// мёртвых прямых Reality-портов). «auto» (urltest по всем) остаётся как выбор.
	defaultOut := "auto"
	if wsTag != "" {
		defaultOut = wsTag
	}
	head := []map[string]any{
		{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": append([]string{"auto"}, tags...),
			"default":   defaultOut,
		},
		{
			"type":      "urltest",
			"tag":       "auto",
			"outbounds": tags,
			"url":       "https://www.gstatic.com/generate_204",
			"interval":  "3m0s",
		},
	}
	tail := []map[string]any{
		{"type": "direct", "tag": "direct"},
	}
	all := append(append(head, outbounds...), tail...)

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": map[string]any{
			"servers": []map[string]any{
				{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
			},
			"final":    "remote",
			"strategy": "prefer_ipv4",
		},
		"inbounds": []map[string]any{{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "fortochka",
			"address":        []string{"172.19.0.1/30"},
			"mtu":            1400, // под Reality/TLS-заголовки — без фрагментации
			"auto_route":     true,
			"strict_route":   true, // kill-switch: трафик не может обойти туннель
			"stack":          "system",
		}},
		"outbounds": all,
		"route": map[string]any{
			"rules":                 buildRoute(sp),
			"final":                 "proxy",
			"auto_detect_interface": true,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// buildRoute — правила маршрутизации с учётом раздельного туннелирования.
// Порядок важен: первое совпадение выигрывает, потом final=proxy.
func buildRoute(sp Split) []map[string]any {
	rules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"ip_is_private": true, "outbound": "direct"},
	}
	if len(sp.Apps) > 0 {
		rules = append(rules, map[string]any{"process_name": sp.Apps, "outbound": "direct"})
	}
	if len(sp.Sites) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": sp.Sites, "outbound": "direct"})
	}
	if sp.BypassRu {
		rules = append(rules, map[string]any{"domain_suffix": []string{".ru", ".su", ".рф", "xn--p1ai"}, "outbound": "direct"})
	}
	return rules
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
