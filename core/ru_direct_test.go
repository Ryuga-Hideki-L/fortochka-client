package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// Правило РФ-категорий обязано попадать в конфиг движка: раньше «мимо туннеля»
// умел только суффикс .ru, и CDN вроде yastatic.net уходил в туннель, из-за чего
// сайт грузился наполовину. Проверяем не список доменов (он будет меняться),
// а то, что выбор пользователя доезжает до правил маршрутизации.
func TestRuCategoriesReachRoute(t *testing.T) {
	rules := buildRoute(Split{RuCats: []string{"banks"}})
	dom := domainsOfDirectRules(t, rules)
	if !dom["sberbank.ru"] {
		t.Fatalf("банковская категория включена, а sberbank.ru не в direct: %v", dom)
	}
	if dom["kinopoisk.ru"] {
		t.Errorf("домен выключенной категории попал в правила: kinopoisk.ru")
	}
}

func TestRuOffRemovesDomain(t *testing.T) {
	rules := buildRoute(Split{RuCats: []string{"banks"}, RuOff: []string{"sberbank.ru"}})
	dom := domainsOfDirectRules(t, rules)
	if dom["sberbank.ru"] {
		t.Error("снятый пользователем домен всё равно попал в обход")
	}
	if !dom["vtb.ru"] {
		t.Error("снятие одного домена не должно убирать остальные из категории")
	}
}

// Пользователь вправе вписать домен как ему привычно — из адресной строки.
func TestRuOffNormalizesInput(t *testing.T) {
	for _, in := range []string{"https://WWW.Sberbank.ru/", "www.sberbank.ru", " SBERBANK.RU "} {
		rules := buildRoute(Split{RuCats: []string{"banks"}, RuOff: []string{in}})
		if domainsOfDirectRules(t, rules)["sberbank.ru"] {
			t.Errorf("%q не распознан как sberbank.ru", in)
		}
	}
}

// В режиме «через туннель только выбранное» весь остальной трафик и так идёт
// напрямую — добавлять туда РФ-категории значит писать правила, которые ничего
// не меняют, и путать того, кто читает конфиг.
func TestRuCategoriesSkippedInOnlyListedMode(t *testing.T) {
	rules := buildRoute(Split{OnlyListed: true, RuCats: []string{"banks"}})
	if domainsOfDirectRules(t, rules)["sberbank.ru"] {
		t.Error("в режиме «только выбранное» РФ-категории не нужны")
	}
}

func TestDefaultRuCategoriesAreConfirmedOnes(t *testing.T) {
	def := map[string]bool{}
	for _, k := range DefaultRuCategories() {
		def[k] = true
	}
	for _, c := range RuCategories {
		// по умолчанию включаем только то, где поломка подтверждена замером,
		// иначе человек молча теряет туннель на доменах, которые и так работали
		if c.Confidence == "low" && def[c.Key] {
			t.Errorf("категория %s с низкой уверенностью включена по умолчанию", c.Key)
		}
	}
	if !def["cdn"] {
		t.Error("без CDN подтверждённые категории работают наполовину — должна быть включена")
	}
}

// Домены не должны повторяться между категориями: дубль в domain_suffix безвреден
// для движка, но означает, что снятие домена в одной категории не сработает.
func TestNoDuplicateDomainsAcrossCategories(t *testing.T) {
	where := map[string]string{}
	for _, c := range RuCategories {
		for _, d := range c.Domains {
			if prev, ok := where[d.Name]; ok {
				t.Errorf("домен %s есть и в %s, и в %s", d.Name, prev, c.Key)
			}
			where[d.Name] = c.Key
		}
	}
}

func TestDomainsLookLikeSuffixes(t *testing.T) {
	for _, c := range RuCategories {
		for _, d := range c.Domains {
			if d.Name != strings.ToLower(d.Name) {
				t.Errorf("%s: домен не в нижнем регистре", d.Name)
			}
			if strings.ContainsAny(d.Name, "/: ") || strings.HasPrefix(d.Name, ".") {
				t.Errorf("%s: не годится для domain_suffix", d.Name)
			}
			if !strings.Contains(d.Name, ".") {
				t.Errorf("%s: домен без точки", d.Name)
			}
			// симптом — то, чем домен ответил на пробу; без него непонятно, почему он в списке
			if strings.TrimSpace(d.Symptom) == "" {
				t.Errorf("%s: не указан симптом", d.Name)
			}
		}
	}
}

// domainsOfDirectRules собирает домены всех правил, ведущих в direct.
func domainsOfDirectRules(t *testing.T, rules []map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	// маршалим и разбираем обратно — так же, как это увидит движок
	raw, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("правила не сериализуются: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("правила не читаются обратно: %v", err)
	}
	for _, r := range parsed {
		if r["outbound"] != "direct" {
			continue
		}
		list, ok := r["domain_suffix"].([]any)
		if !ok {
			continue
		}
		for _, d := range list {
			if s, ok := d.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
