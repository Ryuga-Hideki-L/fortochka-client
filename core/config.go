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
	var resilientTags []string // grpc/ws/http — мультиплекс, вне «Сигнала 3» ТСПУ
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
		case "grpc":
			ob["transport"] = map[string]any{"type": "grpc", "service_name": def(p.Service, "grpc")}
		}

		// Мультиплексные транспорты (один h2/ws-канал) не палятся «Сигналом 3» ТСПУ
		// (>3 параллельных TLS к одному SNI → фриз ~120с). Сырой tcp/Vision — палится.
		if p.Net == "grpc" || p.Net == "ws" || p.Net == "http" || p.Net == "httpupgrade" {
			resilientTags = append(resilientTags, tag)
		}

		outbounds = append(outbounds, ob)
		tags = append(tags, tag)
	}

	if len(tags) == 0 {
		return nil, errors.New("нет профилей, совместимых с движком")
	}

	// В auto-пул берём устойчивые транспорты (grpc/ws), если они есть. Сырой tcp/Vision
	// исключаем из авто-выбора: крошечный пробник generate_204 на нём ПРОХОДИТ, а реальный
	// трафик замерзает после ~15-20 КБ (ТСПУ blackholing) — urltest иначе залипал на мёртвом.
	// tcp/Vision остаётся в selector'е «proxy» как ручной/последний резерв.
	autoPool := resilientTags
	if len(autoPool) == 0 {
		autoPool = tags // устойчивых нет — берём что есть, лучше так, чем никак
	}

	// По умолчанию — «auto» (urltest): сам выбирает РАБОЧИЙ профиль под сеть юзера.
	// У разных людей рабочий разный: у кого-то CDN (прямой OVH душат), у кого-то
	// grpc-Reality (Gcore-edge недоступен). urltest адаптируется, а не залипает.
	head := []map[string]any{
		{
			"type":      "selector",
			"tag":       "proxy",
			"outbounds": append([]string{"auto"}, tags...), // все транспорты доступны вручную
			"default":   "auto",
		},
		{
			"type":      "urltest",
			"tag":       "auto",
			"outbounds": autoPool,
			"url":       "https://www.gstatic.com/generate_204",
			"interval":  "3m0s",
		},
	}
	tail := []map[string]any{
		{"type": "direct", "tag": "direct"},
	}
	all := append(append(head, outbounds...), tail...)

	// DNS: адреса аутбаундов (vpn.rungvard.net) резолвим НАПРЯМУЮ через local —
	// это задаёт route.default_domain_resolver (иначе замкнутый круг + FATAL
	// в sing-box 1.12+). Остальные запросы — через туннель (remote).
	dnsServers := []map[string]any{
		{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
		{"type": "local", "tag": "local"},
	}
	var dnsRules []map[string]any
	if sp.BypassRu {
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": []string{".ru", ".su", ".рф", "xn--p1ai"}, "server": "local"})
	}
	dnsCfg := map[string]any{"servers": dnsServers, "final": "remote", "strategy": "prefer_ipv4"}
	if len(dnsRules) > 0 {
		dnsCfg["rules"] = dnsRules
	}

	cfg := map[string]any{
		"log": map[string]any{"level": "warn"},
		"dns": dnsCfg,
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
			"default_domain_resolver": map[string]any{"server": "local"},
			"rules":                   buildRoute(sp),
			"final":                   "proxy",
			"auto_detect_interface":   true,
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
