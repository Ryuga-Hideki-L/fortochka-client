package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fortochka/core"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx         context.Context
	engine      *core.Engine
	mu          sync.Mutex
	link        string
	state       string
	cfg         []byte        // последний рабочий конфиг — для авто-реконнекта
	healthStop  chan struct{} // закрытие останавливает health-loop
	bypassRu     bool     // РФ-домены напрямую (режим исключений)
	onlyListed   bool     // через туннель только перечисленное
	bypassApps   []string // приложения в списке split
	bypassSites  []string // сайты в списке split
	tlsFragment  bool     // TLS ClientHello fragment для не-Reality
	ruCats       []string // включённые категории РФ-сервисов (см. core/ru_direct.go)
	ruOff        []string // домены категорий, снятые пользователем вручную
	lastChan    string        // последний активный канал (для лога переключений)
	updNote     string        // статус обновления с последней проверки (для лога)
	clashAddr   string        // адрес локального контроллера sing-box этой сессии
	gen         uint64        // счётчик сессий подключения — защита от гонок Connect/Disconnect
	sess        string        // короткий id сессии — им склеиваются журнал клиента и серверный
	pinned      string        // канал, выбранный вручную приложением после провала по объёму
}

// sessionID — короткий идентификатор попытки подключения. Печатается в шапке журнала
// и уходит на сервер: по нему одна жалоба сводится с одной записью в панели.
func sessionID() string {
	b := make([]byte, 4)
	if _, err := crand.Read(b); err != nil {
		return time.Now().Format("150405")
	}
	return hex.EncodeToString(b)
}

func nonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type settings struct {
	Link        string   `json:"link"`
	BypassRu    *bool    `json:"bypassRu,omitempty"` // указатель: nil = первый запуск → дефолт true
	OnlyListed  *bool    `json:"onlyListed,omitempty"`
	BypassApps  []string `json:"bypassApps,omitempty"`
	BypassSites []string `json:"bypassSites,omitempty"`
	TlsFragment *bool    `json:"tlsFragment,omitempty"`
	// Указатель, а не срез: пустой список категорий — осмысленный выбор
	// («ничего не пускать мимо»), и отличать его от первого запуска обязательно,
	// иначе после сохранения дефолт вернётся сам и человек решит, что настройки не держатся.
	RuCats []string `json:"ruCats"`
	RuSet  *bool    `json:"ruCatsSet,omitempty"`
	RuOff  []string `json:"ruOff,omitempty"`
}

// InstalledApp — программа из реестра/Program Files (Windows) или .desktop (Linux).
type InstalledApp struct {
	Name string `json:"name"`
	Exe  string `json:"exe"`
}

// RunningApp — процесс, работающий прямо сейчас. Отдельный тип от InstalledApp,
// потому что здесь есть то, чего у установленной программы нет: путь к бинарю и
// сколько процессов с таким именем живо. Имя процесса — ровно то, что сверяет
// движок в правиле process_name, поэтому выбор отсюда не промахивается.
type RunningApp struct {
	Name  string `json:"name"`
	Exe   string `json:"exe"`
	Path  string `json:"path"`
	Count int    `json:"count"`
	Net   bool   `json:"net"` // держит открытые сетевые сокеты прямо сейчас
}

// SplitCfg отдаётся во фронт для окна настроек.
type SplitCfg struct {
	BypassRu    bool     `json:"bypassRu"`
	OnlyListed  bool     `json:"onlyListed"`
	BypassApps  []string `json:"bypassApps"`
	BypassSites []string `json:"bypassSites"`
	TlsFragment bool     `json:"tlsFragment"`
	RuCats      []string `json:"ruCats"`
	RuOff       []string `json:"ruOff"`
}

func NewApp() *App {
	return &App{engine: core.NewEngine(), state: "disconnected", bypassRu: true, tlsFragment: true,
		ruCats: core.DefaultRuCategories()}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.loadSettings()
}

func (a *App) shutdown(ctx context.Context) {
	a.engine.Stop()
	a.DPIStop() // снять системный прокси и убить byedpi/winws, чтобы не осиротели
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	dir = filepath.Join(dir, "Fortochka")
	os.MkdirAll(dir, 0o700)
	return dir
}

func (a *App) loadSettings() {
	b, err := os.ReadFile(filepath.Join(configDir(), "settings.json"))
	if err != nil {
		return
	}
	var s settings
	if json.Unmarshal(b, &s) == nil {
		a.link = s.Link
		if s.BypassRu != nil {
			a.bypassRu = *s.BypassRu
		}
		if s.OnlyListed != nil {
			a.onlyListed = *s.OnlyListed
		}
		a.bypassApps = s.BypassApps
		a.bypassSites = s.BypassSites
		if s.RuSet != nil {
			a.ruCats = s.RuCats
		}
		a.ruOff = s.RuOff
		if s.TlsFragment != nil {
			a.tlsFragment = *s.TlsFragment
		}
	}
}

func (a *App) saveSettings() {
	ru := a.bypassRu
	ol := a.onlyListed
	tf := a.tlsFragment
	ruSet := true
	b, _ := json.MarshalIndent(settings{Link: a.link, BypassRu: &ru, OnlyListed: &ol,
		BypassApps: a.bypassApps, BypassSites: a.bypassSites, TlsFragment: &tf,
		RuCats: a.ruCats, RuSet: &ruSet, RuOff: a.ruOff}, "", "  ")
	os.WriteFile(filepath.Join(configDir(), "settings.json"), b, 0o600)
}

// GetSplit / SetSplit — настройки раздельного туннелирования для окна настроек.
func (a *App) GetSplit() SplitCfg {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SplitCfg{BypassRu: a.bypassRu, OnlyListed: a.onlyListed,
		BypassApps: a.bypassApps, BypassSites: a.bypassSites, TlsFragment: a.tlsFragment,
		RuCats: a.ruCats, RuOff: a.ruOff}
}

func (a *App) SetSplit(bypassRu, onlyListed bool, apps, sites []string, tlsFragment bool,
	ruCats, ruOff []string) {
	a.mu.Lock()
	a.bypassRu = bypassRu
	a.onlyListed = onlyListed
	a.bypassApps = cleanList(apps)
	a.bypassSites = cleanList(sites)
	a.tlsFragment = tlsFragment
	a.ruCats = cleanList(ruCats)
	a.ruOff = cleanList(ruOff)
	a.mu.Unlock()
	a.saveSettings()
}

// ListRuCategories — встроенный список российских сервисов для окна настроек:
// заголовки, пояснения «что именно ломается» и сами домены. Фронт показывает их
// как есть, чтобы человек видел, что именно он пускает мимо туннеля, и мог убрать
// и категорию целиком, и отдельный домен.
func (a *App) ListRuCategories() []core.RuCategory {
	return core.RuCategories
}

// ListInstalledApps — установленные программы (Windows: реестр + Program Files,
// Linux: .desktop-файлы, включая flatpak и snap).
func (a *App) ListInstalledApps() []InstalledApp {
	return listInstalledApps()
}

// ListRunningApps — что работает прямо сейчас. Основной способ выбора: имя процесса
// здесь настоящее, а не выведенное из названия в меню.
//
// На Linux без прав root видны только свои процессы — клиент и так запускается
// от root ради TUN, но если это не так, список будет коротким, и интерфейс должен
// об этом сказать, а не притворяться, что программ в системе нет.
func (a *App) ListRunningApps() []RunningApp {
	return listRunningApps()
}

func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) GetLink() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.link
}

func (a *App) SetLink(link string) {
	link = strings.TrimSpace(link)
	// принимаем и hiddify://import/<url>, и голый url подписки
	if i := strings.Index(link, "://import/"); i >= 0 {
		link = link[i+len("://import/"):]
		if h := strings.IndexByte(link, '#'); h >= 0 {
			link = link[:h]
		}
	}
	a.mu.Lock()
	a.link = link
	a.mu.Unlock()
	a.saveSettings()
}

func (a *App) State() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *App) setState(s string) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "state", s)
}

// superseded — эту сессию подключения сменил другой Connect/Disconnect (по счётчику gen).
func (a *App) superseded(myGen uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen != myGen
}

func logPath() string     { return filepath.Join(configDir(), "fortochka.log") }
func prevLogPath() string { return filepath.Join(configDir(), "fortochka.prev.log") }

// keptSessions — сколько прошлых журналов держим. Одного «предыдущего» не хватало:
// жалоба почти всегда приходит через несколько запусков после поломки, и лог той
// самой сессии к тому моменту уже затёрт.
const keptSessions = 6

func sessionLogPath(i int) string {
	return filepath.Join(configDir(), fmt.Sprintf("fortochka.%d.log", i))
}

// rotateLogs сдвигает журналы: текущий → .1, .1 → .2 и так до keptSessions.
// Самый старый удаляется. prevLogPath оставлен как есть — на него смотрит UI.
func rotateLogs() {
	os.Remove(sessionLogPath(keptSessions))
	for i := keptSessions - 1; i >= 1; i-- {
		os.Rename(sessionLogPath(i), sessionLogPath(i+1))
	}
	if b, err := os.ReadFile(logPath()); err == nil && len(b) > 0 {
		os.WriteFile(sessionLogPath(1), b, 0o600)
		os.WriteFile(prevLogPath(), b, 0o600)
	}
}

// GetPrevLogs — журнал прошлой сессии (для истории/диагностики).
func (a *App) GetPrevLogs() string {
	b, err := os.ReadFile(prevLogPath())
	if err != nil {
		return ""
	}
	return string(b)
}

// logMu сериализует запись: журнал пишут health-loop, горутина отправки и Connect
// одновременно, и без замка строки перемешивались посередине.
var logMu sync.Mutex

func (a *App) log(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	// Дата и смещение зоны, а не голое «15:04:05»: журнал клиента приходится
	// сводить с серверными логами, а там UTC — без даты и зоны это гадание.
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05.000-07:00"),
		fmt.Sprintf(format, args...))
}

// GetLogs отдаёт хвост журнала (свой + движка) для окна логов.
func (a *App) GetLogs() string {
	b, err := os.ReadFile(logPath())
	if err != nil {
		return "Журнал пуст."
	}
	const max = 16000
	if len(b) > max {
		b = b[len(b)-max:]
	}
	return string(b)
}

func (a *App) Connect() string {
	a.mu.Lock()
	if a.state == "connecting" { // уже идёт подключение — игнорируем повторное нажатие
		a.mu.Unlock()
		return ""
	}
	a.gen++
	myGen := a.gen
	a.state = "connecting"
	link := a.link
	tlsFrag := a.tlsFragment
	sp := core.Split{BypassRu: a.bypassRu, OnlyListed: a.onlyListed, Apps: a.bypassApps, Sites: a.bypassSites,
		RuCats: a.ruCats, RuOff: a.ruOff}
	un := a.updNote
	a.lastChan = ""
	a.mu.Unlock()
	runtime.EventsEmit(a.ctx, "state", "connecting")
	defer a.sendLogToServer(link) // по завершении попытки — отправить журнал на сервер под subId

	if link == "" {
		a.setState("disconnected")
		return "Вставьте ссылку подписки"
	}
	// Zapret (WinDivert) конфликтует с TUN-туннелем — глушим его перед VPN. ByeDPI не мешает.
	dpi.mu.Lock()
	zapretOn := dpi.running && dpi.engine == "zapret"
	dpi.mu.Unlock()
	if zapretOn {
		a.DPIStop()
	}
	rotateLogs() // история последних сессий, а не только предыдущей
	// новый журнал на сессию
	sess := sessionID()
	a.mu.Lock()
	a.sess = sess
	a.mu.Unlock()
	os.WriteFile(logPath(), []byte(fmt.Sprintf("%s  === подключение · сессия %s ===\n",
		time.Now().Format("2006-01-02 15:04:05.000-07:00"), sess)), 0o600)
	core.Log = a.log // core пишет свои шаги в этот же журнал
	a.log("Форточка %s · %s · %s", version, osArch(), osDetail())
	a.log("устройство %s · часы %s (UTC%s)", deviceID(),
		time.Now().Format("2006-01-02 15:04:05"), time.Now().Format("-07:00"))
	if isAdmin() {
		a.log("права администратора: да")
	} else {
		a.log("права администратора: НЕТ — туннель не поднимется. Закройте и запустите от имени администратора (ПКМ по ярлыку → «Запуск от имени администратора»)")
	}
	if un != "" {
		a.log("%s", un)
	}
	a.log("источник: %s", sourceDesc(link))
	a.logNetEnv()

	a.log("этап 1/6 — скачиваю и разбираю ссылку")
	tFetch := time.Now()
	profiles, usedURL, err := core.FetchProfilesAny(link)
	if usedURL != "" && usedURL != link {
		// Зеркало сработало, а основной адрес нет — запоминаем рабочий, иначе
		// человек будет упираться в мёртвый адрес при каждом запуске.
		a.mu.Lock()
		a.link = usedURL
		a.mu.Unlock()
		a.saveSettings()
		a.log("основной адрес подписки недоступен, дальше используем зеркало")
	}
	usedEmbedded := false
	if (err != nil || len(profiles) == 0) && fallbackSub != "" {
		// подписка недоступна (Gcore лёг / домен зарезан) — встроенный резерв
		if fb := embeddedFallback(); len(fb) > 0 {
			a.log("подписка недоступна — включаю встроенный резерв (%d профилей)", len(fb))
			profiles, err = fb, nil
			usedEmbedded = true
		}
	}
	if err != nil {
		a.log("подписка: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	a.log("получено профилей: %d за %s", len(profiles), time.Since(tFetch).Round(time.Millisecond))
	if len(profiles) == 0 {
		a.setState("disconnected")
		return "В подписке нет поддерживаемых профилей"
	}
	a.log("этап 2/6 — профили из подписки")
	a.logProfiles(profiles)
	a.log("настройки: %s", splitDesc(sp))
	// свободный порт под локальный контроллер (диагностика). Занят/нет порта —
	// работаем без него: коннект важнее логов каналов.
	clashAddr := freeLoopbackAddr()
	a.mu.Lock()
	a.clashAddr = clashAddr
	a.mu.Unlock()
	a.log("этап 3/6 — собираю конфиг движка (контроллер %s)", nonEmptyStr(clashAddr, "выключен"))
	cfg, err := core.BuildConfig(profiles, sp, clashAddr, tlsFrag)
	if err != nil {
		a.log("конфиг: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	a.logConfigSummary(cfg)
	if a.superseded(myGen) { // пока качали/строили — юзер нажал «Отключить»
		return ""
	}
	a.log("этап 4/6 — запускаю движок")
	if err := a.engine.Start(cfg, logPath()); err != nil {
		a.log("движок: %s", err)
		a.setState("disconnected")
		return err.Error()
	}
	a.log("движок запущен: %s", a.engine.Describe())

	// Не показываем «Подключено», пока реально не вышли через туннель.
	// Если туннель мёртв — strict_route блокирует трафик, проверка не пройдёт.
	a.log("этап 5/6 — проверяю выход в сеть")
	if !a.probe(12 * time.Second) {
		a.engine.Stop()
		if a.superseded(myGen) {
			return ""
		}
		a.log("туннель не поднялся — нет ответа через VPN")
		a.setState("disconnected")
		return "Не удалось выйти в сеть через VPN. Проверьте ссылку или смените сервер."
	}
	// нас мог сменить Disconnect, пока шёл пробник — не поднимаем «connected» поверх
	if a.superseded(myGen) {
		a.engine.Stop()
		return ""
	}
	a.log("туннель проверен, соединение активно")
	a.logChannels()
	a.log("этап 6/6 — проверяю канал под нагрузкой")
	a.ensureVolume(myGen)
	// Подписку не удалось скачать до подключения (домен зарезан), а сейчас туннель
	// работает — значит можно взять её через него. Это разрывает замкнутый круг:
	// чтобы получить свежие ссылки, нужен доступ, а доступ даёт как раз туннель,
	// поднятый на встроенном резерве.
	if usedEmbedded {
		if p, u, e := core.FetchProfilesAny(link); e == nil && len(p) > 0 {
			a.log("подписка обновлена через туннель (%d профилей)", len(p))
			if u != "" && u != link {
				a.mu.Lock()
				a.link = u
				a.mu.Unlock()
				a.saveSettings()
			}
		} else {
			a.log("подписку не удалось обновить даже через туннель: %v", e)
		}
	}
	if ip := a.ExitIP(); ip != "" {
		a.log("внешний адрес через туннель: %s", ip)
	}

	a.mu.Lock()
	a.cfg = cfg
	stop := make(chan struct{})
	a.healthStop = stop
	a.mu.Unlock()
	go a.healthLoop(stop, myGen)

	a.setState("connected")
	return ""
}

func (a *App) Disconnect() {
	a.mu.Lock()
	a.gen++ // помечаем: любая идущая Connect-сессия устарела
	if a.healthStop != nil {
		close(a.healthStop)
		a.healthStop = nil
	}
	a.mu.Unlock()
	a.engine.Stop()
	a.log("отключено")
	a.pushLog() // полный лог сессии на сервер
	a.setState("disconnected")
}

// probe проверяет, что трафик реально уходит через туннель. Два хоста —
// чтобы блокировка/сбой одного не давал ложное «не подключено».
func (a *App) probe(within time.Duration) bool {
	client := directClient(4 * time.Second) // без системного прокси — меряем туннель, а не прокси юзера
	urls := []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"}
	deadline := time.Now().Add(within)
	round := 0
	var lastErr string
	for {
		round++
		for _, u := range urls {
			started := time.Now()
			resp, err := client.Get(u)
			if err == nil {
				code := resp.StatusCode
				resp.Body.Close()
				if code == 204 || code == 200 {
					if round > 1 { // с первой попытки — молчим, чтобы не засорять журнал
						a.log("  пробник: %s ответил %d с %d-й попытки (%s)",
							hostOf(u), code, round, time.Since(started).Round(time.Millisecond))
					}
					return true
				}
				lastErr = fmt.Sprintf("%s → HTTP %d", hostOf(u), code)
				continue
			}
			// Текст ошибки раньше выбрасывался, и в журнале оставалось голое
			// «туннель не поднялся» — неотличимо, отказ это DNS, TLS или таймаут.
			lastErr = fmt.Sprintf("%s → %v", hostOf(u), err)
		}
		if time.Now().After(deadline) {
			if lastErr != "" {
				a.log("  пробник: не прошёл за %s, последняя ошибка — %s", within, lastErr)
			}
			return false
		}
		time.Sleep(700 * time.Millisecond)
	}
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// healthLoop раз в 30с проверяет живость туннеля и переподключает при обрыве.
// Раз в 5 минут дополнительно гоняет проверку объёмом: лёгкий пробник на 204
// проходит и по замороженному каналу, и без этой проверки клиент часами сидит
// на канале, через который ничего крупнее пары килобайт не проходит.
func (a *App) healthLoop(stop chan struct{}, myGen uint64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	fails := 0
	ticks := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if a.superseded(myGen) || a.State() != "connected" {
				return
			}
			if a.probe(5 * time.Second) {
				fails = 0
				a.noteChannelSwitch() // залогировать, если urltest сменил канал
				ticks++
				if ticks%10 == 0 { // каждые ~5 минут
					if ok, note := a.volumeOK(); !ok {
						a.log("канал перестал держать объём (%s) — ищу другой", note)
						a.ensureVolume(myGen)
					}
				}
				continue
			}
			fails++
			a.log("проверка связи не прошла (%d/2)", fails)
			if fails == 1 {
				a.pushLog() // поймать момент падения (с ошибками sing-box в логе)
			}
			if fails >= 2 {
				a.reconnect(stop, myGen)
				fails = 0
			}
		}
	}
}

// reconnect перезапускает движок тем же конфигом. При неудаче трафик остаётся
// заблокированным kill-switch'ем (strict_route) — утечки нет.
func (a *App) reconnect(stop chan struct{}, myGen uint64) {
	select {
	case <-stop: // уже отключились вручную
		return
	default:
	}
	if a.superseded(myGen) {
		return
	}
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg == nil {
		return
	}
	a.log("обрыв — переподключаюсь…")
	a.engine.Stop()
	time.Sleep(1 * time.Second)
	if err := a.engine.Start(cfg, logPath()); err != nil {
		a.log("реконнект: движок не стартовал: %s", err)
		a.setState("disconnected")
		return
	}
	// пользователь мог нажать «Отключить», пока мы переподключались —
	// иначе останется живой sing-box после отключения (зомби-туннель).
	select {
	case <-stop:
		a.engine.Stop()
		return
	default:
	}
	if a.superseded(myGen) {
		a.engine.Stop()
		return
	}
	if a.probe(12 * time.Second) {
		a.log("переподключено")
		a.logChannels()
	} else {
		a.log("реконнект не удался — трафик заблокирован (нет утечки)")
		a.pushLog()
		a.setState("disconnected")
	}
}

// ExitIP запрашивается уже через туннель — показываем адрес выхода.
func (a *App) ExitIP() string {
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}
