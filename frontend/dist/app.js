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

async function openSheet() {
  el("link").value = await api().GetLink();
  el("sheet").classList.remove("hidden");
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

window.addEventListener("DOMContentLoaded", async () => {
  el("power").onclick = togglePower;
  el("gear").onclick = openSheet;
  el("cancel").onclick = closeSheet;
  el("save").onclick = saveLink;
  el("logs").onclick = openLogs;
  el("logclose").onclick = closeLogs;
  el("logrefresh").onclick = refreshLogs;
  el("logcopy").onclick = copyLogs;

  window.runtime.EventsOn("state", setUI);

  const link = await api().GetLink();
  setUI(await api().State());
  if (!link) openSheet();
});
