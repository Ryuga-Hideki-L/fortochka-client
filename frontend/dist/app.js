const api = () => window.go.main.App;
const el = (id) => document.getElementById(id);
let state = "disconnected";

function setUI(s) {
  state = s;
  document.body.classList.toggle("on", s === "connected");
  document.body.classList.toggle("busy", s === "connecting");
  const status = el("status"), sub = el("sub"), power = el("power");
  if (s === "connected") {
    status.textContent = "Защищено";
    sub.textContent = "Соединение зашифровано";
    power.textContent = "Отключиться";
    showExit();
  } else if (s === "connecting") {
    status.textContent = "Подключение…";
    sub.textContent = "Ищем лучший сервер";
    power.textContent = "Отмена";
    el("foot").textContent = "";
  } else {
    status.textContent = "Отключено";
    sub.textContent = "Нажмите, чтобы подключиться";
    power.textContent = "Подключиться";
    el("foot").textContent = "";
  }
}

async function showExit() {
  try {
    const ip = await api().ExitIP();
    if (ip && state === "connected") el("foot").textContent = "Выход: " + ip;
  } catch (e) {}
}

function toast(msg) {
  const t = el("toast");
  t.textContent = msg;
  t.classList.add("show");
  clearTimeout(t._t);
  t._t = setTimeout(() => t.classList.remove("show"), 3200);
}

async function togglePower() {
  if (state === "connected" || state === "connecting") {
    await api().Disconnect();
    return;
  }
  const err = await api().Connect();
  if (err) {
    setUI("disconnected");
    toast(err);
  }
}

function lines(s) {
  return (s || "").split(/[\n,]+/).map((x) => x.trim()).filter(Boolean);
}

function updateSplitUI() {
  const only = el("onlyListed").checked;
  el("bypassRuBlock").classList.toggle("split-disabled", only);
  el("splitHint").textContent = only
    ? "Сейчас: через VPN идёт только перечисленное, остальное — напрямую."
    : "Сейчас: перечисленное идёт мимо VPN, остальное через него.";
  el("appsLabel").innerHTML = only
    ? "Программы через Форточку <small>(остальные — напрямую)</small>"
    : "Программы мимо Форточки <small>(остальные — через туннель)</small>";
  el("sitesLabel").innerHTML = only
    ? "Сайты через Форточку <small>(по одному в строке: example.com)</small>"
    : "Сайты мимо Форточки <small>(по одному в строке: example.com)</small>";
}

/* ---------- выбор программ для раздельного туннелирования ----------
   Правило движка сверяет ИМЯ ПРОЦЕССА, а не название в меню: «Google Chrome» —
   это chrome.exe, telegram-desktop в меню зовётся Telegram. Поэтому основной
   источник — живые процессы, а не список установленного; вкладка «Сейчас в сети»
   стоит первой, потому что заворачивают обычно то, что прямо сейчас лезет наружу. */
let PICKED = [];        // выбранные имена процессов
let APPSRC = "net";     // активная вкладка списка
let RUNNING = null;     // кэш ответа ListRunningApps
let INSTALLED = null;

function pickedHas(exe) {
  return PICKED.some((p) => p.toLowerCase() === String(exe).toLowerCase());
}
function togglePicked(exe) {
  exe = String(exe || "").trim();
  if (!exe) return;
  if (pickedHas(exe)) PICKED = PICKED.filter((p) => p.toLowerCase() !== exe.toLowerCase());
  else PICKED.push(exe);
  renderChips();
  renderAppList();
}
function renderChips() {
  const box = el("appChips");
  box.innerHTML = "";
  PICKED.forEach((p) => {
    const c = document.createElement("span");
    c.className = "chip";
    c.textContent = p;
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = "✕";
    b.title = "Убрать из списка";
    b.onclick = () => togglePicked(p);
    c.appendChild(b);
    box.appendChild(c);
  });
  el("appChipsEmpty").style.display = PICKED.length ? "none" : "";
}

async function loadAppSources() {
  try { RUNNING = (await api().ListRunningApps()) || []; } catch (e) { RUNNING = []; }
  try { INSTALLED = (await api().ListInstalledApps()) || []; } catch (e) { INSTALLED = []; }
}

function currentSource() {
  if (APPSRC === "installed") {
    return (INSTALLED || []).map((a) => ({ name: a.name, exe: a.exe }));
  }
  const list = RUNNING || [];
  return (APPSRC === "net" ? list.filter((a) => a.net) : list).map((a) => ({
    name: a.name, exe: a.exe, path: a.path, count: a.count, net: a.net,
  }));
}

function renderAppList() {
  const box = el("appList");
  const q = (el("appSearch").value || "").trim().toLowerCase();
  let items = currentSource();
  if (q) {
    items = items.filter(
      (a) => a.exe.toLowerCase().includes(q) || String(a.name || "").toLowerCase().includes(q)
    );
  }
  box.innerHTML = "";
  if (!items.length) {
    const d = document.createElement("div");
    d.className = "picker-empty";
    d.textContent = q
      ? 'Ничего не найдено. Кнопка «Добавить» внесёт «' + el("appSearch").value.trim() + '» как есть.'
      : APPSRC === "net"
      ? "Сейчас никто не держит соединений. Загляните во вкладку «Запущенные»."
      : APPSRC === "running"
      ? "Список процессов пуст — на Linux он виден только с правами root."
      : "Установленные программы не найдены.";
    box.appendChild(d);
    return;
  }
  items.slice(0, 300).forEach((a) => {
    const row = document.createElement("div");
    row.className = "picker-row" + (pickedHas(a.exe) ? " picked" : "");
    row.title = a.path || a.exe;
    row.onclick = () => togglePicked(a.exe);

    const mark = document.createElement("span");
    mark.className = "mark";
    mark.textContent = pickedHas(a.exe) ? "✓" : "";
    row.appendChild(mark);

    const nm = document.createElement("span");
    nm.className = "nm";
    nm.textContent = a.name && a.name !== a.exe ? a.name + " " : "";
    const i = document.createElement("i");
    i.textContent = a.exe;
    nm.appendChild(i);
    row.appendChild(nm);

    if (a.count > 1) {
      const c = document.createElement("span");
      c.className = "cnt";
      c.textContent = "×" + a.count;
      row.appendChild(c);
    }
    if (a.net) {
      const n = document.createElement("span");
      n.className = "net";
      n.textContent = "в сети";
      row.appendChild(n);
    }
    box.appendChild(row);
  });
}

/* ---------- российские сервисы напрямую ----------
   Список зашит в программе, но он не догма: категорию можно выключить целиком,
   а любой домен внутри — снять. Рядом с каждой категорией написано, что именно
   ломается и проверено ли это замером, иначе выбор превращается в гадание. */
let RUCATS = [];        // справочник из Go
let RU_ON = [];         // включённые категории
let RU_OFF = [];        // снятые пользователем домены

const CONF_LABEL = { high: "проверено", medium: "частично", low: "не проверено" };

function renderRuCats() {
  const box = el("ruCats");
  if (!box) return;
  box.innerHTML = "";
  RUCATS.forEach((c) => {
    const on = RU_ON.includes(c.key);
    const offInCat = (c.domains || []).filter((d) => RU_OFF.includes(d.name)).length;

    const wrap = document.createElement("div");
    wrap.className = "rucat";

    const head = document.createElement("div");
    head.className = "rucat-head";
    head.onclick = (e) => {
      if (e.target.tagName === "INPUT") return;   // клик по галочке — не раскрытие
      wrap.classList.toggle("open");
    };

    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = on;
    cb.onclick = (e) => {
      e.stopPropagation();
      RU_ON = cb.checked ? RU_ON.concat([c.key]) : RU_ON.filter((k) => k !== c.key);
      renderRuCats();
    };
    head.appendChild(cb);

    const main = document.createElement("div");
    main.className = "rucat-main";
    const title = document.createElement("div");
    title.className = "rucat-title";
    title.textContent = c.title + " ";
    const conf = document.createElement("span");
    conf.className = "conf " + (c.confidence || "low");
    conf.textContent = CONF_LABEL[c.confidence] || "не проверено";
    title.appendChild(conf);
    main.appendChild(title);
    const note = document.createElement("div");
    note.className = "rucat-note";
    note.textContent = c.note || "";
    main.appendChild(note);
    head.appendChild(main);

    const cnt = document.createElement("span");
    cnt.className = "rucat-count";
    cnt.textContent = offInCat
      ? (c.domains.length - offInCat) + "/" + c.domains.length
      : String((c.domains || []).length);
    head.appendChild(cnt);
    wrap.appendChild(head);

    const doms = document.createElement("div");
    doms.className = "rucat-domains";
    (c.domains || []).forEach((d) => {
      const off = RU_OFF.includes(d.name);
      const chip = document.createElement("span");
      chip.className = "dchip" + (off ? " off" : "");
      chip.textContent = d.name;
      // симптом — то, чем домен ответил на пробу с зарубежного адреса;
      // без него список выглядит как чужое мнение, а не как измерение
      chip.title = d.symptom ? d.name + " — " + d.symptom : d.name;
      const b = document.createElement("button");
      b.type = "button";
      b.textContent = off ? "+" : "✕";
      b.title = off ? "Вернуть домен в обход" : "Убрать домен из обхода";
      b.onclick = (e) => {
        e.stopPropagation();
        RU_OFF = off ? RU_OFF.filter((x) => x !== d.name) : RU_OFF.concat([d.name]);
        renderRuCats();
      };
      chip.appendChild(b);
      doms.appendChild(chip);
    });
    wrap.appendChild(doms);
    box.appendChild(wrap);
  });
}

async function loadRuCats() {
  try { RUCATS = (await api().ListRuCategories()) || []; } catch (e) { RUCATS = []; }
  renderRuCats();
}

function addManualApp() {
  const v = (el("appSearch").value || "").trim();
  if (!v) return;
  if (pickedHas(v)) { toast("Уже в списке"); return; }
  togglePicked(v);
  el("appSearch").value = "";
  renderAppList();
}

async function openSheet() {
  el("link").value = await api().GetLink();
  try {
    const sp = await api().GetSplit();
    el("bypassRu").checked = !!(sp && sp.bypassRu);
    el("onlyListed").checked = !!(sp && sp.onlyListed);
    PICKED = ((sp && sp.bypassApps) || []).slice();
    RU_ON = ((sp && sp.ruCats) || []).slice();
    RU_OFF = ((sp && sp.ruOff) || []).slice();
    el("bypassSites").value = ((sp && sp.bypassSites) || []).join("\n");
    el("tlsFragment").checked = sp && sp.tlsFragment !== false;
  } catch (e) {}
  updateSplitUI();
  renderChips();
  renderRuCats();
  el("sheet").classList.remove("hidden");
  // список программ грузим после показа окна: перебор процессов занимает
  // десятки миллисекунд, и ждать его перед открытием настроек незачем
  await loadAppSources();
  renderAppList();
  await loadRuCats();
}
function closeSheet() {
  el("sheet").classList.add("hidden");
}
async function saveLink() {
  const v = el("link").value.trim();
  if (!v) {
    toast("Вставьте ссылку");
    return;
  }
  await api().SetLink(v);
  try {
    await api().SetSplit(
      el("bypassRu").checked,
      el("onlyListed").checked,
      PICKED.slice(),
      lines(el("bypassSites").value),
      el("tlsFragment").checked,
      RU_ON.slice(),
      RU_OFF.slice()
    );
  } catch (e) {}
  closeSheet();
  toast("Сохранено");
}

async function refreshLogs() {
  const box = el("logbox");
  box.textContent = await api().GetLogs();
  box.scrollTop = box.scrollHeight;
}
async function openLogs() {
  await refreshLogs();
  el("logsheet").classList.remove("hidden");
}
function closeLogs() {
  el("logsheet").classList.add("hidden");
}
async function copyLogs() {
  try {
    await navigator.clipboard.writeText(el("logbox").textContent);
    toast("Скопировано");
  } catch (e) {}
}

async function checkUpdate() {
  try {
    const u = await api().CheckUpdate();
    if (u && u.hasUpdate) {
      el("updtext").textContent = "Доступна версия " + u.latest;
      el("updbar").classList.remove("hidden");
    }
  } catch (e) {}
}
async function doUpdate() {
  el("updtext").textContent = "Обновляю…";
  el("updbtn").disabled = true;
  try {
    const err = await api().DoUpdate();
    if (err) {
      toast(err);
      el("updbtn").disabled = false;
      el("updtext").textContent = "Доступно обновление";
    }
    // при успехе приложение само перезапустится
  } catch (e) {
    el("updbtn").disabled = false;
  }
}

async function fillPresets() {
  try {
    const presets = await api().DPIPresets(el("zEngine").value);
    const sel = el("zPreset");
    sel.innerHTML = "";
    (presets || []).forEach((p) => {
      const o = document.createElement("option");
      o.value = p.id;
      o.textContent = p.name;
      sel.appendChild(o);
    });
  } catch (e) {}
}
async function refreshZStatus() {
  try {
    const s = await api().DPIStatus();
    const on = !!(s && s.running);
    el("zStatus").textContent = on ? "Включено · " + (s.engine || "") + " / " + (s.preset || "") : "Выключено";
    el("zStatus").classList.toggle("on", on);
    el("zToggle").textContent = on ? "Выключить" : "Включить";
  } catch (e) {}
}
async function openZapret() {
  await fillPresets();
  await refreshZStatus();
  el("zapretsheet").classList.remove("hidden");
}
function closeZapret() {
  el("zapretsheet").classList.add("hidden");
}
async function toggleZapret() {
  try {
    const s = await api().DPIStatus();
    if (s && s.running) {
      await api().DPIStop();
    } else {
      const err = await api().DPIStart(el("zEngine").value, el("zPreset").value);
      if (err) {
        toast(err);
        return;
      }
      toast("Запрет включён");
    }
    await refreshZStatus();
  } catch (e) {}
}

async function runTest() {
  toast("Проверяю…");
  try {
    const r = await api().SelfTest();
    if (!r) return;
    let msg = r.message || (r.ok ? "Работает" : "Проблема");
    if (r.active) msg += " · " + r.active;
    toast(msg);
  } catch (e) {
    toast("Тест не удался");
  }
}

window.addEventListener("DOMContentLoaded", async () => {
  el("power").onclick = togglePower;
  el("gear").onclick = openSheet;
  el("onlyListed").onchange = updateSplitUI;
  el("appAddManual").onclick = addManualApp;
  el("appSearch").oninput = renderAppList;
  el("appSearch").onkeydown = (e) => { if (e.key === "Enter") { e.preventDefault(); addManualApp(); } };
  document.querySelectorAll(".picker-tabs button").forEach((b) => {
    b.onclick = () => {
      document.querySelectorAll(".picker-tabs button").forEach((x) => x.setAttribute("aria-selected", "false"));
      b.setAttribute("aria-selected", "true");
      APPSRC = b.dataset.src;
      renderAppList();
    };
  });
  el("cancel").onclick = closeSheet;
  el("save").onclick = saveLink;
  el("logs").onclick = openLogs;
  el("logclose").onclick = closeLogs;
  el("logrefresh").onclick = refreshLogs;
  el("logcopy").onclick = copyLogs;

  el("updbtn").onclick = doUpdate;

  el("killstuck").onclick = async () => {
    try {
      const msg = await api().Cleanup();
      toast(msg || "Готово");
    } catch (e) {
      toast("Не удалось");
    }
  };

  el("zapret").onclick = openZapret;
  el("zClose").onclick = closeZapret;
  el("zEngine").onchange = fillPresets;
  el("zToggle").onclick = toggleZapret;
  el("test").onclick = runTest;

  window.runtime.EventsOn("state", setUI);

  try {
    el("appver").textContent = "v" + (await api().Version());
  } catch (e) {}

  const link = await api().GetLink();
  setUI(await api().State());
  if (!link) openSheet();
  checkUpdate();
});
