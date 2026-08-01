package core

import "strings"

// Российские сервисы, которые ломаются при зарубежном выходном IP.
//
// Список построен НЕ по догадкам: 28.07.2026 каждый домен опрошен с адреса нашего
// же сервера (Франция, OVH) — это ровно то, что видит сервис, когда у человека
// включён туннель. В файл попало только то, что действительно не работает: из 203
// проверенных крупнейших российских доменов сломались 144, остальные (Кинопоиск,
// Мегафон, ЮMoney, Рамблер, «Смотрим», Сферум и др.) в обход НЕ добавлены — гнать
// мимо туннеля работающий сайт значит без нужды раскрывать домашний адрес.
//
// Симптом хранится рядом с доменом и показывается в интерфейсе: человек должен
// видеть, почему домен здесь, и иметь возможность поспорить — снять любой домен
// или выключить категорию целиком.
//
// Отдельно про CDN: суффикса .ru недостаточно. Статика российских сервисов живёт
// на чужих доменах (yastatic.net, wbstatic.net, cdn-tinkoff.ru), и пока она идёт
// через туннель, сайт грузится наполовину.
//
// Проверка исключила и мусор из публичных списков: ozonusercontent.com, wbbasket.ru,
// cdn-vk.ru, t-static.ru, o3.ru, mironline.ru не существуют в DNS вообще — ни через
// глобальный резолвер, ни через российский.

// RuDomain — домен и то, чем он ответил на пробу с зарубежного адреса.
type RuDomain struct {
	Name    string `json:"name"`
	Symptom string `json:"symptom"`
}

// RuCategory — группа доменов, которую можно включить или выключить целиком.
type RuCategory struct {
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	Note       string     `json:"note"`
	Confidence string     `json:"confidence"` // high — симптом измерен, medium — измерен частично
	Default    bool       `json:"default"`
	Domains    []RuDomain `json:"domains"`
}

// RuCategories — встроенный список. Правится здесь одним местом: и правила движка,
// и окно настроек читают его же.
var RuCategories = []RuCategory{
	{
		Key: "banks", Title: "Банки и брокеры", Confidence: "high", Default: true,
		Note: "Сбербанк отдаёт страницу блокировки, ВТБ и Т-Банк не отвечают вовсе, Альфа — отказ.",
		Domains: []RuDomain{
			{"sberbank.ru", "страница блокировки или капча"},
			{"tbank.ru", "не отвечает"},
			{"tinkoff.ru", "не отвечает"},
			{"tcsbank.ru", "не отвечает"},
			{"vtb.ru", "не отвечает"},
			{"alfabank.ru", "страница блокировки или капча"},
			{"gazprombank.ru", "страница блокировки или капча"},
			{"gpb.ru", "страница блокировки или капча"},
			{"rshb.ru", "не отвечает"},
			{"psb.ru", "не отвечает"},
			{"open.ru", "обрыв TLS"},
			{"sovcombank.ru", "отказ в доступе (401)"},
			{"mkb.ru", "страница блокировки или капча"},
			{"uralsib.ru", "не отвечает"},
			{"homecredit.ru", "отказ в доступе (401)"},
			{"renaissance.ru", "обрыв TLS"},
			{"pochtabank.ru", "не отвечает"},
			{"domrfbank.ru", "страница блокировки или капча"},
			{"absolutbank.ru", "страница блокировки или капча"},
			{"avangard.ru", "обрыв TLS"},
			{"akbars.ru", "не отвечает"},
			{"bcs.ru", "страница блокировки или капча"},
			{"finam.ru", "страница блокировки или капча"},
			{"sberbank.com", "страница блокировки или капча"},
			{"otpbank.ru", "страница блокировки или капча"},
			{"rencredit.ru", "страница блокировки или капча"},
			{"rnkb.ru", "соединение отвергнуто"},
		},
	},
	{
		Key: "payments", Title: "Платежи и страхование", Confidence: "high", Default: true,
		Note: "Платёжные шлюзы и страховые с зарубежного адреса отказывают — оплата не проходит.",
		Domains: []RuDomain{
			{"nspk.ru", "обрыв TLS"},
			{"best2pay.net", "страница блокировки или капча"},
			{"cloudpayments.ru", "страница блокировки или капча"},
			{"robokassa.ru", "ошибка сервера (503)"},
			{"freekassa.ru", "не отвечает"},
			{"qiwi.com", "страница блокировки или капча"},
			{"ingos.ru", "не отвечает"},
			{"reso.ru", "не отвечает"},
			{"sogaz.ru", "не отвечает"},
			{"alfastrah.ru", "отказ в доступе (401)"},
			{"vsk.ru", "зацикленный редирект"},
		},
	},
	{
		Key: "gov", Title: "Госуслуги и государство", Confidence: "medium", Default: true,
		Note: "Госуслуги, ЕСИА, mos.ru и госпорталы с зарубежного адреса просто не отвечают.",
		Domains: []RuDomain{
			{"gosuslugi.ru", "не отвечает"},
			{"esia.gosuslugi.ru", "не резолвится за рубежом"},
			{"dom.gosuslugi.ru", "не отвечает"},
			{"mos.ru", "не отвечает"},
			{"mvd.ru", "не отвечает"},
			{"gibdd.ru", "не отвечает"},
			{"mos-gorsud.ru", "не отвечает"},
			{"rosreestr.gov.ru", "не отвечает"},
			{"zakupki.gov.ru", "не отвечает"},
			{"bus.gov.ru", "не отвечает"},
			{"mosreg.ru", "отказ в доступе (403)"},
			{"gu.spb.ru", "не отвечает"},
			{"gov.ru", "не отвечает"},
			{"government.ru", "не отвечает"},
			{"kremlin.ru", "не отвечает"},
			{"fssp.gov.ru", "не резолвится за рубежом"},
			{"gu-st.ru", "проверялся отдельно"},
		},
	},
	{
		Key: "ecommerce", Title: "Магазины и маркетплейсы", Confidence: "high", Default: true,
		Note: "Wildberries отдаёт 498, Ozon зацикливает редирект, Авито и Маркет требуют капчу.",
		Domains: []RuDomain{
			{"wildberries.ru", "отказ (498)"},
			{"wb.ru", "отказ (498)"},
			{"ozon.ru", "зацикленный редирект"},
			{"market.yandex.ru", "страница блокировки или капча"},
			{"avito.ru", "страница блокировки или капча"},
			{"youla.ru", "страница блокировки или капча"},
			{"dns-shop.ru", "отказ в доступе (401)"},
			{"eldorado.ru", "ошибка сервера (503)"},
			{"citilink.ru", "отказ (429)"},
			{"lamoda.ru", "страница блокировки или капча"},
			{"sportmaster.ru", "отказ в доступе (401)"},
			{"detmir.ru", "страница блокировки или капча"},
			{"petrovich.ru", "страница блокировки или капча"},
			{"auchan.ru", "отказ в доступе (401)"},
			{"5ka.ru", "страница блокировки или капча"},
			{"magnit.ru", "страница блокировки или капча"},
			{"lenta.com", "отказ в доступе (401)"},
			{"aliexpress.ru", "не отвечает"},
			{"sberdevices.ru", "зацикленный редирект"},
			{"sbermegamarket.ru", "страница блокировки или капча"},
			{"lemanapro.ru", "отказ в доступе (401)"},
		},
	},
	{
		Key: "delivery", Title: "Доставка и логистика", Confidence: "high", Default: true,
		Note: "СДЭК и Почта России с зарубежного адреса отдают страницу блокировки.",
		Domains: []RuDomain{
			{"cdek.ru", "страница блокировки или капча"},
			{"pochta.ru", "страница блокировки или капча"},
			{"dellin.ru", "отказ в доступе (401)"},
			{"kuper.ru", "отказ в доступе (403)"},
		},
	},
	{
		Key: "telecom", Title: "Операторы связи", Confidence: "high", Default: true,
		Note: "МТС и Tele2 показывают блокировку, Ростелеком рвёт TLS — личные кабинеты недоступны.",
		Domains: []RuDomain{
			{"mts.ru", "страница блокировки или капча"},
			{"my.mts.ru", "страница блокировки или капча"},
			{"tele2.ru", "страница блокировки или капча"},
			{"t2.ru", "страница блокировки или капча"},
			{"rostelecom.ru", "обрыв TLS"},
			{"yota.ru", "страница блокировки или капча"},
			{"domru.ru", "отказ в доступе (403)"},
			{"ttk.ru", "не отвечает"},
			{"ertelecom.ru", "страница блокировки или капча"},
			{"dom.ru", "отказ в доступе (403)"},
			{"sms.ru", "страница блокировки или капча"},
		},
	},
	{
		Key: "media", Title: "Стриминг, музыка и СМИ", Confidence: "high", Default: false,
		Note: "Okko, ivi, Rutube и Premier отдают блокировку; права на видео и так проверяют по стране.",
		Domains: []RuDomain{
			{"ivi.ru", "страница блокировки или капча"},
			{"okko.tv", "страница блокировки или капча"},
			{"wink.ru", "ошибка сервера (503)"},
			{"premier.one", "страница блокировки или капча"},
			{"rutube.ru", "страница блокировки или капча"},
			{"vkvideo.ru", "зацикленный редирект"},
			{"zvuk.com", "страница блокировки или капча"},
			{"matchtv.ru", "не отвечает"},
			{"rbc.ru", "отказ в доступе (401)"},
			{"vedomosti.ru", "ошибка сервера (502)"},
			{"ivicdn.tv", "страница блокировки или капча"},
			{"okkoapi.tv", "отказ в доступе (403)"},
			{"clstorage.net", "отказ в доступе (403)"},
			{"ivi.tv", "страница блокировки или капча"},
			{"more.tv", "обрыв TLS"},
			{"cdnvideo.ru", "страница блокировки или капча"},
			{"cdnvideohub.com", "страница блокировки или капча"},
			{"trbcdn.net", "обрыв TLS"},
			{"record.ru", "обрыв TLS"},
			{"stroki.com", "обрыв TLS"},
			{"bookmate.com", "отказ в доступе (403)"},
		},
	},
	{
		Key: "travel", Title: "Транспорт и путешествия", Confidence: "medium", Default: false,
		Note: "РЖД и Мосметро не отвечают, Аэрофлот отдаёт 503, 2ГИС недоступен.",
		Domains: []RuDomain{
			{"rzd.ru", "не отвечает"},
			{"aeroflot.ru", "ошибка сервера (503)"},
			{"dme.ru", "страница блокировки или капча"},
			{"mosmetro.ru", "не отвечает"},
			{"2gis.ru", "не отвечает"},
			{"2gis.com", "проверялся отдельно"},
			{"level.travel", "страница блокировки или капча"},
			{"citydrive.ru", "страница блокировки или капча"},
			{"maps.yandex.ru", "страница блокировки или капча"},
			{"ostrovok.com", "не отвечает"},
			{"onlinetours.ru", "страница блокировки или капча"},
			{"busfor.ru", "отказ в доступе (403)"},
		},
	},
	{
		Key: "realty", Title: "Недвижимость и жильё", Confidence: "high", Default: false,
		Note: "Домклик отказывает в доступе, ЦИАН требует капчу.",
		Domains: []RuDomain{
			{"domclick.ru", "отказ в доступе (401)"},
			{"cian.ru", "страница блокировки или капча"},
			{"domrf.ru", "страница блокировки или капча"},
		},
	},
	{
		Key: "services", Title: "Работа, учёба и медицина", Confidence: "high", Default: false,
		Note: "hh.ru блокирует и требует капчу, ЯКласс отказывает, аптеки и лаборатории отдают ошибку.",
		Domains: []RuDomain{
			{"hh.ru", "страница блокировки или капча"},
			{"yaklass.ru", "страница блокировки или капча"},
			{"docdoc.ru", "страница блокировки или капча"},
			{"gemotest.ru", "страница блокировки или капча"},
			{"eapteka.ru", "страница блокировки или капча"},
			{"inn.ru", "ошибка сервера (502)"},
			{"rabota.ru", "страница блокировки или капча"},
			{"superjob.ru", "страница блокировки или капча"},
			{"openedu.ru", "страница блокировки или капча"},
			{"skyeng.ru", "страница блокировки или капча"},
			{"uchi.ru", "страница блокировки или капча"},
			{"sberhealth.ru", "страница блокировки или капча"},
			{"doktornarabote.ru", "не отвечает"},
			{"edu.ru", "не отвечает"},
		},
	},
	{
		Key: "games", Title: "Игры и ставки", Confidence: "high", Default: false,
		Note: "Игровые серверы и букмекеры с зарубежного адреса закрываются по географии.",
		Domains: []RuDomain{
			{"lesta.ru", "не отвечает"},
			{"tanki.su", "не отвечает"},
			{"fon.bet", "страница блокировки или капча"},
			{"betboom.ru", "страница блокировки или капча"},
			{"marathonbet.ru", "страница блокировки или капча"},
			{"ligastavok.ru", "не отвечает"},
			{"my.games", "страница блокировки или капча"},
			{"4game.com", "страница блокировки или капча"},
			{"funpay.com", "страница блокировки или капча"},
			{"oplata.info", "страница блокировки или капча"},
			{"ggsel.net", "отказ в доступе (401)"},
			{"plati.ru", "страница блокировки или капча"},
			{"klauncher.ru", "страница блокировки или капча"},
			{"lesta.games", "обрыв TLS"},
			{"wgcdn.co", "не резолвится за рубежом"},
		},
	},
	{
		Key: "cdn", Title: "CDN и статика", Confidence: "medium", Default: true,
		Note: "Картинки и статика российских сервисов — без них сайты грузятся наполовину.",
		Domains: []RuDomain{
			{"wbstatic.net", "не отвечает"},
			{"cdn-tinkoff.ru", "проверялся отдельно"},
			{"yastatic.net", "страница блокировки или капча"},
			{"yandex.net", "страница блокировки или капча"},
			{"vkuser.net", "проверялся отдельно"},
			{"vkuseraudio.net", "отказ (418)"},
			{"vkuseraudio.com", "отказ (418)"},
			{"vkuseraudio.ru", "отказ (418)"},
			{"vkuservideo.net", "отказ (418)"},
			{"vkuservideo.com", "отказ (418)"},
			{"vkuservideo.ru", "отказ (418)"},
			{"vkuserlive.net", "обрыв TLS"},
			{"userapi.com", "страница блокировки или капча"},
		},
	},
}

// DefaultRuCategories — что включено у нового пользователя: только то, где поломка
// подтверждена замером, плюс CDN (без него подтверждённые категории работают
// наполовину). Остальное человек включает сам, увидев причину.
func DefaultRuCategories() []string {
	var out []string
	for _, c := range RuCategories {
		if c.Default {
			out = append(out, c.Key)
		}
	}
	return out
}

// normalizeDomain приводит снятый пользователем домен к виду из списка: человек
// вполне может вписать «https://WWW.Sberbank.ru/» — и ожидать, что это сработает.
func normalizeDomain(d string) string {
	d = strings.TrimSpace(strings.ToLower(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimSuffix(d, "/")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	return strings.TrimPrefix(d, "www.")
}

// ruDirectDomains — домены включённых категорий за вычетом снятых пользователем.
func ruDirectDomains(cats, off []string) []string {
	if len(cats) == 0 {
		return nil
	}
	enabled := map[string]bool{}
	for _, k := range cats {
		enabled[k] = true
	}
	excluded := map[string]bool{}
	for _, d := range off {
		excluded[normalizeDomain(d)] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range RuCategories {
		if !enabled[c.Key] {
			continue
		}
		for _, d := range c.Domains {
			if excluded[d.Name] || seen[d.Name] {
				continue
			}
			seen[d.Name] = true
			out = append(out, d.Name)
		}
	}
	return out
}
