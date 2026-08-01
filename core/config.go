package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
)

// Split — настройки раздельного туннелирования.
//
// Два режима, и они противоположны по смыслу:
//
//	OnlyListed = false (по умолчанию) — «исключения»: перечисленное идёт МИМО
//	  туннеля, остальное через него. Так работало всегда.
//	OnlyListed = true — «только выбранное»: через туннель идёт ТОЛЬКО
//	  перечисленное, остальное напрямую. Нужно, когда туннель требуется одной-двум
//	  программам, а остальному интернету он мешает или не нужен.
//
// Поля Apps и Sites в обоих режимах те же самые, меняется лишь их трактовка —
// поэтому подписи в интерфейсе обязаны меняться вместе с режимом, иначе человек
// настроит ровно наоборот.
type Split struct {
	BypassRu   bool     // РФ-домены напрямую (только в режиме исключений)
	Apps       []string // имена процессов: chrome.exe
	Sites      []string // домены: example.com
	OnlyListed bool     // true — через туннель идёт только перечисленное
	RuCats     []string // включённые категории РФ-сервисов (см. ru_direct.go)
	RuOff      []string // домены категорий, снятые пользователем вручную
}

// ClashAPIAddr — дефолтный адрес контроллера для тестов. В приложении порт
// подбирается динамически (свободный), чтобы не падать на занятом порту.
const ClashAPIAddr = "127.0.0.1:19090"

// Resilient — уходит ли канал от заморозки ТСПУ, и потому годится в авто-выбор.
// От заморозки датацентра (TCP на «подозрительный» IP, фриз после ~15-20 КБ) спасают:
//   - UDP-протоколы (hysteria2/tuic) — вне TCP-механизма вовсе;
//   - транспорт за CDN (сервер — ДОМЕН, а не голый IP) — другой, не палёный IP.
//
// Прямой TCP на IP датацентра (grpc/tcp/xhttp на 141.x) морозится ОДИНАКОВО, даже grpc:
// пробник проходит, а реальный трафик виснет. Такое — только вручную, не в авто.
func Resilient(p Profile) bool {
	switch p.Proto {
	case "hysteria2", "tuic":
		return true
	}
	// vless (TCP): безопасен, только если за CDN — сервер задан доменом, не IP.
	if net.ParseIP(p.Server) != nil {
		return false // прямой на IP датацентра — морозится по объёму
	}
	// только транспорты, которые реально собирает buildVless (ws/grpc)
	return p.Net == "grpc" || p.Net == "ws"
}

// Unsupported — почему профиль не попадёт в конфиг движка. "" = попадёт.
// Раньше такие профили отбрасывались молча, и в подписке из шести строк до движка
// доезжало пять без единого слова в журнале — со стороны это выглядело как
// «клиент потерял сервер».
func Unsupported(p Profile) string {
	proto := p.Proto
	if proto == "" {
		proto = "vless"
	}
	if proto == "vless" && p.Net == "xhttp" {
		return "движок sing-box не умеет xhttp (это транспорт Xray)"
	}
	return ""
}

// BuildConfig собирает конфиг sing-box из профилей подписки:
// TUN (весь трафик), авто-выбор лучшего сервера по задержке, обход локалки,
// плюс раздельное туннелирование по приложениям/сайтам/РФ-доменам.
func BuildConfig(profiles []Profile, sp Split, clashAddr string, tlsFragment bool) ([]byte, error) {
	var outbounds []map[string]any
	var tags []string
	var resilientTags []string // каналы вне заморозки ТСПУ: UDP (hy2/tuic) + CDN-домен (ws)
	used := map[string]bool{}
	reserved := map[string]bool{"auto": true, "proxy": true, "direct": true} // служебные теги — имя профиля не должно совпадать

	for i, p := range profiles {
		proto := p.Proto
		if proto == "" {
			proto = "vless"
		}
		if proto == "vless" && p.Net == "xhttp" { // движок sing-box не поддерживает xhttp
			continue
		}
		base := proto
		if proto == "vless" {
			base = p.Net
		}
		tag := p.Name
		if tag == "" || used[tag] || reserved[tag] {
			tag = fmt.Sprintf("%s-%d", base, i)
		}
		used[tag] = true

		var ob map[string]any
		resilient := false

		switch proto {
		case "hysteria2":
			ob = buildHy2(tag, p)
		case "tuic":
			ob = buildTuic(tag, p)
		default: // vless
			ob = buildVless(tag, p, tlsFragment)
		}
		resilient = Resilient(p)

		if resilient {
			resilientTags = append(resilientTags, tag)
		}
		outbounds = append(outbounds, ob)
		tags = append(tags, tag)
	}

	if len(tags) == 0 {
		return nil, errors.New("нет профилей, совместимых с движком")
	}

	// В auto-пул берём только каналы, уходящие от заморозки (UDP + CDN-домен). Прямой
	// TCP на IP датацентра (grpc/tcp/xhttp) исключаем: крошечный пробник generate_204 на
	// нём ПРОХОДИТ, а реальный трафик замерзает после ~15-20 КБ (ТСПУ blackholing) —
	// иначе urltest сажает юзера на мёртвый канал. Они остаются в «proxy» как ручной резерв.
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

	// Куда по умолчанию уходит резолв. В режиме «только выбранное» — напрямую:
	// иначе получается рассинхрон, из-за которого режим работает наполовину.
	// Запрос на резолв уходит РАНЬШЕ, чем маршрутизатор понял, какой процесс его
	// затеял, поэтому при dns.final=remote имена разрешались бы через туннель даже
	// для программ, которым туннель не предназначен: и утечка запросов, и попадание
	// на другой узел CDN, чем тот, куда пойдёт само соединение.
	dnsFinal := "remote"
	if sp.OnlyListed {
		dnsFinal = "local"
		// Домены, которым туннель предназначен, резолвим через него: адрес должен
		// приходить из той же точки, откуда пойдёт соединение.
		if len(sp.Sites) > 0 {
			dnsRules = append(dnsRules,
				map[string]any{"domain_suffix": sp.Sites, "server": "remote"})
		}
	}

	// ipv4_only: не отдаём AAAA → нет IPv6-назначений → нет утечки IPv6 мимо туннеля
	// и нет happy-eyeballs-виса в мёртвый IPv6 (сервера IPv4-only). Сайты почти все с IPv4.
	dnsCfg := map[string]any{"servers": dnsServers, "final": dnsFinal, "strategy": "ipv4_only"}
	if len(dnsRules) > 0 {
		dnsCfg["rules"] = dnsRules
	}

	cfg := map[string]any{
		// level=info + отметки времени: на "warn" движок молчал почти обо всём,
		// и в журнале не было видно ни выбора аутбаунда, ни причины обрыва —
		// оставались только строки самого приложения. Разбирать «где умерло»
		// по такому логу невозможно.
		"log": map[string]any{"level": "info", "timestamp": true},
		"dns": dnsCfg,
		"inbounds": []map[string]any{{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": "fortochka",
			"address":        []string{"172.19.0.1/30"}, // только IPv4: сервера IPv4-only, IPv6 на TUN ломал связность (happy-eyeballs в мёртвый IPv6)
			"mtu":            1400,                      // под Reality/TLS-заголовки — без фрагментации
			"auto_route":     true,
			"strict_route":   true, // kill-switch: трафик не может обойти туннель
			"stack":          "system",
		}},
		"outbounds": all,
		"route": map[string]any{
			"default_domain_resolver": map[string]any{"server": "local"},
			"rules":                   buildRoute(sp),
			// В режиме «только выбранное» всё несовпавшее идёт напрямую, и туннель
			// получают лишь явные правила. В обычном режиме наоборот.
			"final":                 routeFinal(sp),
			"auto_detect_interface": true,
		},
	}
	// локальный контроллер — приложение спрашивает у него активный канал и задержки.
	// Порт передаёт приложение (свободный); пусто — clash_api не включаем.
	if clashAddr != "" {
		cfg["experimental"] = map[string]any{
			"clash_api": map[string]any{"external_controller": clashAddr},
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// routeFinal — куда уходит всё, что не совпало ни с одним правилом.
func routeFinal(sp Split) string {
	if sp.OnlyListed {
		return "direct"
	}
	return "proxy"
}

// appNames приводит имена процессов к тому виду, который ожидает sing-box.
// На Windows расширение обязательно: правило «chrome» не сработает, нужно
// «chrome.exe». Человек в поле ввода про это не помнит, поэтому дополняем сами —
// но только там, где расширения нет вовсе, чтобы не ломать имена в Linux.
func appNames(apps []string) []string {
	out := make([]string, 0, len(apps)*2)
	for _, a := range apps {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		out = append(out, a)
		if runtime.GOOS == "windows" && !strings.Contains(a, ".") {
			out = append(out, a+".exe")
		}
	}
	return out
}

// buildRoute — правила маршрутизации с учётом раздельного туннелирования.
// Порядок важен: первое совпадение выигрывает, дальше действует final.
func buildRoute(sp Split) []map[string]any {
	rules := []map[string]any{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"ip_is_private": true, "outbound": "direct"},
	}

	if sp.OnlyListed {
		// Режим «только выбранное». Порядок правил здесь критичен.
		//
		// Первым — сам движок напрямую. Иначе получается петля: sing-box поднимает
		// соединение к нашему серверу, это соединение попадает в его же правила,
		// не совпадает ни с чем, уходит в... себя. Известные грабли, на них
		// наступали и в Throne, и в v2rayN.
		rules = append(rules, map[string]any{
			"process_name": appNames([]string{"sing-box", "sing-box.exe"}),
			"outbound":     "direct",
		})
		if apps := appNames(sp.Apps); len(apps) > 0 {
			rules = append(rules, map[string]any{"process_name": apps, "outbound": "proxy"})
		}
		if len(sp.Sites) > 0 {
			rules = append(rules, map[string]any{"domain_suffix": sp.Sites, "outbound": "proxy"})
		}
		return rules
	}

	// Обычный режим: перечисленное идёт мимо туннеля.
	if apps := appNames(sp.Apps); len(apps) > 0 {
		rules = append(rules, map[string]any{"process_name": apps, "outbound": "direct"})
	}
	if len(sp.Sites) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": sp.Sites, "outbound": "direct"})
	}
	// Категории РФ-сервисов идут ПЕРЕД широким суффиксом .ru не ради приоритета
	// (первое совпадение и так выигрывает, а результат у обоих правил — direct),
	// а потому что они работают и без него: CDN вроде yastatic.net и okkoapi.tv
	// живут вне зоны .ru, и при выключенном «российские сайты напрямую» именно эти
	// домены остаются единственным, что чинит банки и Госуслуги.
	if ru := ruDirectDomains(sp.RuCats, sp.RuOff); len(ru) > 0 {
		rules = append(rules, map[string]any{"domain_suffix": ru, "outbound": "direct"})
	}
	if sp.BypassRu {
		rules = append(rules, map[string]any{"domain_suffix": []string{".ru", ".su", ".рф", "xn--p1ai"}, "outbound": "direct"})
	}
	return rules
}

func buildVless(tag string, p Profile, tlsFragment bool) map[string]any {
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
		// fragment не применяем: Reality подменяет TLS-хендшейк, fragment режет
		// ClientHello по SNI — см. sing-box TLS outbound (fragment + reality в одном
		// блоке, но fragment для plaintext SNI-matching DPI, не для Reality).
	} else if tlsFragment {
		// не-Reality (ws+tls за CDN): режем ClientHello, чтобы DPI не читал SNI одним пакетом
		tls["fragment"] = true
		tls["fragment_fallback_delay"] = "500ms"
	}
	ob["tls"] = tls
	switch p.Net {
	case "ws":
		ob["transport"] = map[string]any{
			"type":    "ws",
			"path":    p.Path,
			"headers": map[string]any{"Host": nonEmpty(p.Host, p.Server)},
		}
	case "grpc":
		ob["transport"] = map[string]any{"type": "grpc", "service_name": def(p.Service, "grpc")}
	}
	return ob
}

func buildHy2(tag string, p Profile) map[string]any {
	ob := map[string]any{
		"type":     "hysteria2",
		"tag":      tag,
		"server":   p.Server,
		"password": p.Password,
		"tls": map[string]any{
			"enabled":     true,
			"server_name": nonEmpty(p.SNI, p.Server),
			"insecure":    p.Insecure,
		},
	}
	// port-hopping: клиент прыгает по диапазону портов (сервер редиректит их на 443),
	// обходит per-port UDP-throttle ТСПУ. server_ports и server_port взаимоисключающие.
	if hop := hopRange(p.PortHop); hop != "" {
		ob["server_ports"] = []string{hop}
		ob["hop_interval"] = "30s"
	} else {
		ob["server_port"] = p.Port
	}
	if p.Obfs == "salamander" && p.ObfsPass != "" {
		ob["obfs"] = map[string]any{"type": "salamander", "password": p.ObfsPass}
	}
	return ob
}

// hopRange нормализует "20000-40000" (или "20000:40000") в формат sing-box "20000:40000".
// Возвращает "" на любой некорректный ввод (не два числа) — иначе sing-box упадёт на старте.
func hopRange(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	sep := "-"
	if strings.Contains(s, ":") {
		sep = ":"
	}
	parts := strings.Split(s, sep)
	if len(parts) != 2 {
		return ""
	}
	a, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	b, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil || a <= 0 || b <= 0 || a > b || b > 65535 {
		return ""
	}
	return fmt.Sprintf("%d:%d", a, b)
}

func buildTuic(tag string, p Profile) map[string]any {
	tls := map[string]any{
		"enabled":     true,
		"server_name": nonEmpty(p.SNI, p.Server),
		"insecure":    p.Insecure,
	}
	if p.ALPN != "" {
		tls["alpn"] = []string{p.ALPN}
	}
	return map[string]any{
		"type":               "tuic",
		"tag":                tag,
		"server":             p.Server,
		"server_port":        p.Port,
		"uuid":               p.UUID,
		"password":           p.Password,
		"congestion_control": def(p.CC, "bbr"),
		"udp_relay_mode":     "native",
		"tls":                tls,
	}
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
