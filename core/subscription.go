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
}

func FetchProfiles(input string) ([]Profile, error) {
	input = strings.TrimSpace(input)

	// Прямые vless-ссылки (одна или несколько) — парсим сразу, БЕЗ скачивания.
	// Спасает, когда сервер подписки недоступен (Gcore-edge зарезан у юзера).
	if strings.Contains(input, "vless://") {
		if out := parseLines(input); len(out) > 0 {
			return out, nil
		}
		return nil, errors.New("Не удалось разобрать vless-ссылку — проверьте, что скопировали целиком")
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

// parseLines вытаскивает все vless-профили из текста (переносы или пробелы между ссылками).
func parseLines(s string) []Profile {
	var out []Profile
	for _, line := range strings.Split(s, "\n") {
		for _, part := range strings.Fields(line) {
			if strings.HasPrefix(part, "vless://") {
				if p, ok := parseVless(part); ok {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

func decodeMaybeBase64(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "vless://") {
		return s
	}
	// некоторые сервера отдают base64 с переносами строк/пробелами — убираем перед декодом
	compact := strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(compact); err == nil && strings.Contains(string(b), "vless://") {
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
