package core

import (
	"encoding/base64"
	"errors"
	"io"
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

	// Иначе это ссылка-подписка — качаем.
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(input)
	if err != nil {
		return nil, errors.New("Не удалось скачать подписку — проверьте ссылку и интернет")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseLines(decodeMaybeBase64(string(raw))), nil
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
