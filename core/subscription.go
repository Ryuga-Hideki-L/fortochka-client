package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Profile — один сервер из подписки.
type Profile struct {
	Proto   string // vless (по умолчанию) / hysteria2 / tuic
	Name    string
	UUID    string
	Server  string
	Port    int
	Net     string // tcp / ws / grpc / xhttp
	Sec     string // reality / tls
	SNI     string
	FP      string
	PBK     string
	SID     string
	Flow    string
	Path    string
	Host    string
	Service string
	// UDP-протоколы (hysteria2/tuic) — уходят от TCP-заморозки ТСПУ
	Password string
	Insecure bool
	ALPN     string
	Obfs     string // hysteria2: salamander
	ObfsPass string
	CC       string // tuic: congestion control (bbr)
	PortHop  string // hysteria2: диапазон портов "20000-40000" (прыжки против per-port throttle)
}

// хотя бы одна прямая ссылка на сервер (не подписка) — парсим без скачивания.
func hasDirectLink(s string) bool {
	for _, sch := range []string{"vless://", "hysteria2://", "hy2://", "tuic://"} {
		if strings.Contains(s, sch) {
			return true
		}
	}
	return false
}

func FetchProfiles(input string) ([]Profile, error) {
	input = strings.TrimSpace(input)

	// Прямые ссылки (vless/hysteria2/tuic, одна или несколько) — парсим сразу, БЕЗ скачивания.
	// Спасает, когда сервер подписки недоступен (Gcore-edge зарезан у юзера).
	if hasDirectLink(input) {
		if out := parseLines(input); len(out) > 0 {
			return out, nil
		}
		return nil, errors.New("Не удалось разобрать ссылку — проверьте, что скопировали её целиком")
	}

	// Иначе это ссылка-подписка — качаем. CDN (Gcore Free) периодически флапает
	// (502/таймаут), поэтому пробуем несколько раз — обычно одна из попыток проходит.
	subHost := ""
	if u, err := url.Parse(input); err == nil {
		subHost = u.Hostname()
	}
	client := subClient(subHost) // резолв домена через DoH (обход DNS-блока), без системного прокси
	var raw []byte
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(1500 * time.Millisecond)
		}
		resp, err := client.Get(input)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = errors.New("сервер подписки вернул " + strconv.Itoa(resp.StatusCode))
			continue
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		break
	}
	if raw == nil {
		_ = lastErr
		return nil, errors.New("Не удалось скачать подписку — CDN недоступен, попробуйте ещё раз или вставьте прямую ссылку")
	}
	return parseLines(decodeMaybeBase64(string(raw))), nil
}

// dohResolve резолвит host в IPv4 через DoH (Cloudflare/Google), обходя ISP-DNS,
// который в РФ часто блокирует/спуфит домен подписки. "" если DoH недоступен.
func dohResolve(host string) string {
	if host == "" || net.ParseIP(host) != nil {
		return "" // уже IP или пусто — резолв не нужен
	}
	type dnsResp struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	endpoints := []string{
		"https://1.1.1.1/dns-query?type=A&name=",
		"https://dns.google/resolve?type=A&name=",
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	for _, ep := range endpoints {
		req, err := http.NewRequest("GET", ep+url.QueryEscape(host), nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := c.Do(req)
		if err != nil {
			continue
		}
		var d dnsResp
		json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		for _, a := range d.Answer {
			if a.Type == 1 && net.ParseIP(a.Data) != nil { // 1 = A-запись
				return a.Data
			}
		}
	}
	return ""
}

// subClient — http-клиент для скачивания подписки: резолвит домен через DoH
// (SNI при этом сохраняется), не ходит через системный прокси.
func subClient(host string) *http.Client {
	tr := &http.Transport{Proxy: nil}
	if ip := dohResolve(host); ip != "" {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if h, port, err := net.SplitHostPort(addr); err == nil && h == host {
				addr = net.JoinHostPort(ip, port)
			}
			return (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, addr)
		}
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

// parseLines вытаскивает все профили из текста (переносы или пробелы между ссылками).
func parseLines(s string) []Profile {
	var out []Profile
	for _, line := range strings.Split(s, "\n") {
		for _, part := range strings.Fields(line) {
			var p Profile
			var ok bool
			switch {
			case strings.HasPrefix(part, "vless://"):
				p, ok = parseVless(part)
			case strings.HasPrefix(part, "hysteria2://"), strings.HasPrefix(part, "hy2://"):
				p, ok = parseHy2(part)
			case strings.HasPrefix(part, "tuic://"):
				p, ok = parseTuic(part)
			}
			if ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func decodeMaybeBase64(s string) string {
	s = strings.TrimSpace(s)
	if hasDirectLink(s) {
		return s
	}
	// некоторые сервера отдают base64 с переносами строк/пробелами — убираем перед декодом
	compact := strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(compact); err == nil && hasDirectLink(string(b)) {
			return string(b)
		}
	}
	return s
}

func parseVless(link string) (Profile, bool) {
	u, err := url.Parse(link)
	if err != nil || u.User == nil {
		return Profile{}, false
	}
	port, _ := strconv.Atoi(u.Port())
	q := u.Query()
	p := Profile{
		Name:    unescape(u.Fragment),
		UUID:    u.User.Username(),
		Server:  u.Hostname(),
		Port:    port,
		Net:     def(q.Get("type"), "tcp"),
		Sec:     q.Get("security"),
		SNI:     q.Get("sni"),
		FP:      def(q.Get("fp"), "chrome"),
		PBK:     q.Get("pbk"),
		SID:     q.Get("sid"),
		Flow:    q.Get("flow"),
		Path:    def(q.Get("path"), "/"),
		Host:    q.Get("host"),
		Service: q.Get("serviceName"),
	}
	if p.UUID == "" || p.Server == "" || p.Port == 0 {
		return Profile{}, false
	}
	return p, true
}

// hysteria2://password@host:port?sni=..&insecure=1&obfs=salamander&obfs-password=..#name
func parseHy2(link string) (Profile, bool) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return Profile{}, false
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	q := u.Query()
	pass := ""
	if u.User != nil {
		pass = u.User.Username()
		if pass == "" {
			pass, _ = u.User.Password()
		}
	}
	p := Profile{
		Proto:    "hysteria2",
		Name:     unescape(u.Fragment),
		Server:   u.Hostname(),
		Port:     port,
		Password: pass,
		SNI:      def(q.Get("sni"), u.Hostname()),
		Insecure: q.Get("insecure") == "1" || q.Get("insecure") == "true",
		Obfs:     q.Get("obfs"),
		ObfsPass: q.Get("obfs-password"),
		PortHop:  q.Get("mport"), // диапазон портов для прыжков, напр. 20000-40000
	}
	if p.Server == "" || p.Password == "" {
		return Profile{}, false
	}
	return p, true
}

// tuic://uuid:password@host:port?sni=..&alpn=h3&congestion_control=bbr#name
func parseTuic(link string) (Profile, bool) {
	u, err := url.Parse(link)
	if err != nil || u.User == nil || u.Host == "" {
		return Profile{}, false
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 443
	}
	q := u.Query()
	pass, _ := u.User.Password()
	p := Profile{
		Proto:    "tuic",
		Name:     unescape(u.Fragment),
		UUID:     u.User.Username(),
		Server:   u.Hostname(),
		Port:     port,
		Password: pass,
		SNI:      def(q.Get("sni"), u.Hostname()),
		ALPN:     def(q.Get("alpn"), "h3"),
		CC:       def(q.Get("congestion_control"), "bbr"),
		Insecure: q.Get("allow_insecure") == "1" || q.Get("insecure") == "1",
	}
	if p.UUID == "" || p.Server == "" {
		return Profile{}, false
	}
	return p, true
}

func def(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func unescape(s string) string {
	if d, err := url.QueryUnescape(s); err == nil {
		return d
	}
	return s
}
