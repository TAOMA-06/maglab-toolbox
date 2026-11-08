// CS2 复盘教练 · 前端（vanilla JS，无构建步骤）
// 后端绑定：window.go.gui.App（Wails v2 自动注入）
// 进度回调：Go 用 runtime.EventsEmit("run:progress") 推送，这里用 runtime.EventsOn 订阅。
"use strict";

const App = () => (window.go && window.go.gui && window.go.gui.App) || null;
const $ = (id) => document.getElementById(id);
const STAGE_ORDER = ["parse", "analyze", "interpret", "report"];
const STAGE_LABEL = { parse: "解析 demo", analyze: "规则归因", interpret: "LLM 解读", report: "渲染报告" };

const state = {
  demoPath: "",
  probe: null,
  player: "",
  mode: "facts",
  outDir: "",
  settings: null,
  result: null,
  running: false,
  maxUnlocked: 1,
  percent: 0,
};

/* ================= 基础工具 ================= */

function toast(msg, ok) {
  const t = $("toast");
  t.textContent = String(msg || "");
  t.className = ok ? "ok show" : "show";
  clearTimeout(t._h);
  t._h = setTimeout(() => t.classList.remove("show"), 5200);
}

function errMsg(e) {
  if (!e) return "未知错误";
  if (typeof e === "string") return e;
  return e.message || String(e);
}

function esc(s) {
  return String(s == null ? "" : s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function fmtMs(ms) {
  if (!ms || ms < 0) return "0.0s";
  const s = ms / 1000;
  if (s < 60) return s.toFixed(1) + "s";
  return Math.floor(s / 60) + "m " + String(Math.floor(s % 60)).padStart(2, "0") + "s";
}

function showNote(id, msg, kind) {
  const el = $(id);
  el.className = "note" + (kind === "ok" ? " ok" : kind === "err" ? " err" : kind === "info" ? " info" : "");
  el.textContent = msg;
  el.classList.remove("hidden");
}
function hideNote(id) { $(id).classList.add("hidden"); }

/* ================= 极简 Markdown 渲染 ================= */
// 只支持报告用到的子集：标题 / 粗体 / 斜体 / 行内码 / 围栏码 / 列表 / 表格 / 引用 / 分隔线 / 链接。
// 先转义再加标签，LLM 文本里的 HTML 不会被执行。

function inlineMd(s) {
  let out = esc(s);
  const codes = [];
  out = out.replace(/`([^`]+)`/g, (m, c) => {
    codes.push(c);
    return "\u0000" + (codes.length - 1) + "\u0000";
  });
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
    '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  out = out.replace(/\u0000(\d+)\u0000/g, (m, i) => "<code>" + codes[+i] + "</code>");
  return out;
}

function splitRow(line) {
  return line.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|").map((c) => c.trim());
}

function mdToHtml(src) {
  const lines = String(src == null ? "" : src).replace(/\r\n?/g, "\n").split("\n");
  const html = [];
  let i = 0;
  let para = [];
  const flush = () => {
    if (!para.length) return;
    html.push("<p>" + para.map(inlineMd).join("<br>") + "</p>");
    para = [];
  };

  while (i < lines.length) {
    const line = lines[i];

    if (/^\s*```/.test(line)) { // 围栏代码块
      flush();
      const buf = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) { buf.push(lines[i]); i++; }
      i++;
      html.push("<pre><code>" + esc(buf.join("\n")) + "</code></pre>");
      continue;
    }
    if (!line.trim()) { flush(); i++; continue; }
    if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) { flush(); html.push("<hr>"); i++; continue; }

    let m = /^(#{1,6})\s+(.*)$/.exec(line);
    if (m) {
      flush();
      const lvl = m[1].length;
      html.push(`<h${lvl}>${inlineMd(m[2].trim())}</h${lvl}>`);
      i++;
      continue;
    }

    if (/^\s*>\s?/.test(line)) {
      flush();
      const buf = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ""));
        i++;
      }
      html.push("<blockquote>" + buf.map(inlineMd).join("<br>") + "</blockquote>");
      continue;
    }

    // 表格：表头 + 分隔行
    if (line.includes("|") && i + 1 < lines.length &&
        /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/.test(lines[i + 1])) {
      flush();
      const head = splitRow(line);
      i += 2;
      const rows = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim()) {
        rows.push(splitRow(lines[i]));
        i++;
      }
      html.push(
        "<table><thead><tr>" + head.map((h) => `<th>${inlineMd(h)}</th>`).join("") + "</tr></thead><tbody>" +
        rows.map((r) => "<tr>" + r.map((c) => `<td>${inlineMd(c)}</td>`).join("") + "</tr>").join("") +
        "</tbody></table>"
      );
      continue;
    }

    if (/^\s*[-*+]\s+/.test(line)) {
      flush();
      const items = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*+]\s+/, ""));
        i++;
      }
      html.push("<ul>" + items.map((t) => `<li>${inlineMd(t)}</li>`).join("") + "</ul>");
      continue;
    }

    if (/^\s*\d+\.\s+/.test(line)) {
      flush();
      const items = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ""));
        i++;
      }
      html.push("<ol>" + items.map((t) => `<li>${inlineMd(t)}</li>`).join("") + "</ol>");
      continue;
    }

    para.push(line.trim());
    i++;
  }
  flush();
  return html.join("\n");
}

/* ================= 导航 ================= */

function unlock(n) {
  state.maxUnlocked = Math.max(state.maxUnlocked, n);
  document.querySelectorAll(".nav-item").forEach((b) => {
    const k = Number(b.dataset.step);
    b.classList.toggle("locked", k > state.maxUnlocked);
  });
}

function goto(n) {
  n = Number(n);
  if (n > state.maxUnlocked) { toast("先完成前面的步骤"); return; }
  document.querySelectorAll(".nav-item").forEach((b) =>
    b.classList.toggle("active", Number(b.dataset.step) === n));
  document.querySelectorAll(".panel").forEach((p) =>
    p.classList.toggle("active", p.id === "step-" + n));
  $("main").scrollTop = 0;
}

function markDone(n) {
  const b = document.querySelector(`.nav-item[data-step="${n}"]`);
  if (b) { b.classList.add("done"); b.classList.remove("locked"); }
}

document.querySelectorAll(".nav-item").forEach((b) =>
  b.addEventListener("click", () => goto(b.dataset.step)));
document.querySelectorAll("[data-goto]").forEach((b) =>
  b.addEventListener("click", () => goto(b.dataset.goto)));

/* ================= 步骤 1：文件 ================= */

function setS1Badge(text, kind) {
  const b = $("s1-state");
  b.textContent = text;
  b.className = "badge" + (kind ? " " + kind : "");
}

async function probe(path) {
  const api = App();
  if (!api) { toast("未检测到 Wails 运行时：请用桌面版（wails dev / wails build）打开"); return; }
  if (!path) return;
  hideNote("s1-error");
  setS1Badge("读取中…", "amber");
  try {
    const info = await api.Probe(path);
    state.probe = info;
    state.demoPath = info.path;
    renderFileInfo(info);
    renderPlayers(info);
    setS1Badge(info.isPOV ? "POV 录像" : "GOTV 录像", info.isPOV ? "violet" : "cyan");
    markDone(1);
    unlock(2);
    unlock(3);
    goto(2);
  } catch (e) {
    const msg = errMsg(e);
    showNote("s1-error", msg, "err");
    setS1Badge("读取失败", "rose");
    toast(msg);
  }
}

function renderFileInfo(info) {
  $("file-info").classList.remove("hidden");
  $("fi-name").textContent = info.fileName;
  $("fi-size").textContent = info.sizeText + "（" + Number(info.sizeBytes).toLocaleString() + " 字节）";
  $("fi-time").textContent = info.modTime;
  $("fi-path").textContent = info.path;
  $("fi-players").textContent = (info.players ? info.players.length : 0) + " 人";
  const pov = $("fi-pov");
  pov.textContent = info.isPOV ? ("POV · " + (info.povName || "录制者")) : "GOTV 全景";
  pov.className = "badge " + (info.isPOV ? "violet" : "");
}

$("btn-browse").addEventListener("click", async () => {
  const api = App();
  if (!api) { toast("未检测到 Wails 运行时"); return; }
  try {
    const p = await api.SelectDemo();
    if (p) await probe(p);
  } catch (e) {
    toast(errMsg(e));
  }
});

$("btn-reprobe").addEventListener("click", () => {
  if (state.demoPath) probe(state.demoPath);
});

$("btn-to-step2").addEventListener("click", () => goto(2));

// 拖拽：Go 侧 OnFileDrop 把本地路径通过 "demo:dropped" 事件送回来。
const dz = $("dropzone");
["dragenter", "dragover"].forEach((ev) =>
  dz.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.add("over"); }));
["dragleave", "drop"].forEach((ev) =>
  dz.addEventListener(ev, (e) => { e.preventDefault(); dz.classList.remove("over"); }));

/* ================= 步骤 2：玩家 ================= */

function playerInitial(name) {
  const n = String(name || "?").trim();
  return n ? Array.from(n)[0].toUpperCase() : "?";
}

function renderPlayers(info) {
  const box = $("player-list");
  box.innerHTML = "";
  const players = info.players || [];
  $("player-empty").classList.toggle("hidden", players.length > 0);

  const note = $("pov-note");
  note.textContent = info.isPOV
    ? "POV 录像：已自动选中录制者 " + (info.povName || "") + "，也可以换成其他玩家复盘。"
    : "GOTV 全景录像：报告需要一个人称视角，请手动选择焦点玩家。";
  note.classList.remove("hidden");

  let auto = null;
  players.forEach((p) => {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "player-row" + (p.focus ? " sel" : "");
    row.dataset.id = p.steamID64;
    row.dataset.name = p.name;
    row.innerHTML =
      `<span class="avatar">${esc(playerInitial(p.name))}</span>` +
      `<span class="meta"><span class="name">${esc(p.name)}</span>` +
      `<span class="id mono">${esc(p.steamID64)}</span></span>` +
      (p.focus ? '<span class="badge violet" style="margin-left:auto">录制者</span>' : "");
    row.addEventListener("click", () => selectPlayer(row));
    box.appendChild(row);
    if (p.focus) auto = row;
  });

  if (auto) selectPlayer(auto, true);
  else if (players.length === 1) selectPlayer(box.firstElementChild, true);
  else { state.player = ""; setS2Badge("未选择", "amber"); }
}

function selectPlayer(row, silent) {
  if (!row) return;
  document.querySelectorAll(".player-row").forEach((r) => r.classList.remove("sel"));
  row.classList.add("sel");
  state.player = row.dataset.id;
  setS2Badge(row.dataset.name, "emerald");
  markDone(2);
  unlock(4);
  updateRunSummary();
  if (!silent) toast("焦点玩家：" + row.dataset.name, true);
}

function setS2Badge(text, kind) {
  const b = $("s2-state");
  b.textContent = text;
  b.className = "badge" + (kind ? " " + kind : "");
}

$("btn-to-step3").addEventListener("click", () => {
  if (!state.player) { toast("请先选择焦点玩家"); return; }
  goto(3);
});

/* ================= 步骤 3：模式 ================= */

function modeLabel(m) {
  return m === "ollama" ? "本地 Ollama" : m === "api" ? "外部 API" : "纯事实";
}

function setMode(mode, opts) {
  const m = mode === "ollama" || mode === "api" ? mode : "facts";
  state.mode = m;
  document.querySelectorAll(".mode-card").forEach((c) =>
    c.classList.toggle("sel", c.dataset.mode === m));
  $("ollama-box").classList.toggle("hidden", m !== "ollama");
  $("api-box").classList.toggle("hidden", m !== "api");
  const b = $("s3-state");
  b.textContent = modeLabel(m);
  b.className = "badge " + (m === "api" ? "amber" : m === "ollama" ? "violet" : "cyan");
  $("regen-mode").value = m;
  $("ask-mode").textContent = "追问模式：" + modeLabel(m);
  $("ask-mode").className = "badge " + (m === "api" ? "amber" : m === "ollama" ? "violet" : "cyan");
  updateRunSummary();
  if (!opts || !opts.silent) {
    markDone(3);
    persistSettings({ mode: m });
  }
}

document.querySelectorAll(".mode-card").forEach((c) =>
  c.addEventListener("click", () => setMode(c.dataset.mode)));

$("ollama-url").addEventListener("change", () =>
  persistSettings({ ollamaURL: $("ollama-url").value.trim() }));
$("ollama-model").addEventListener("change", () =>
  persistSettings({ ollamaModel: $("ollama-model").value.trim() }));

$("btn-check-ollama").addEventListener("click", async () => {
  const api = App();
  if (!api) return toast("未检测到 Wails 运行时");
  const badge = $("ollama-state");
  badge.textContent = "检测中…"; badge.className = "badge amber";
  try {
    await persistSettings({
      ollamaURL: $("ollama-url").value.trim(),
      ollamaModel: $("ollama-model").value.trim(),
    });
    const msg = await api.CheckLLM("ollama");
    badge.textContent = "已就绪"; badge.className = "badge emerald";
    toast(msg, true);
  } catch (e) {
    badge.textContent = "不可用"; badge.className = "badge rose";
    toast(errMsg(e));
  }
});

$("btn-check-api").addEventListener("click", async () => {
  const api = App();
  if (!api) return toast("未检测到 Wails 运行时");
  const badge = $("api-state");
  try {
    const msg = await api.CheckLLM("api");
    badge.textContent = "已配置"; badge.className = "badge emerald";
    toast(msg, true);
  } catch (e) {
    badge.textContent = "不可用"; badge.className = "badge rose";
    toast(errMsg(e));
  }
});

$("btn-open-settings").addEventListener("click", openSettings);
$("btn-to-step4").addEventListener("click", () => {
  if (!state.player) { toast("请先选择焦点玩家"); return; }
  goto(4);
});

/* ================= 步骤 4：运行 ================= */

function updateRunSummary() {
  const parts = [];
  if (state.probe) parts.push(state.probe.fileName);
  if (state.player) {
    const p = ((state.probe && state.probe.players) || []).find((x) => x.steamID64 === state.player);
    parts.push("焦点：" + (p ? p.name : state.player));
  }
  parts.push("模式：" + modeLabel(state.mode));
  $("run-summary").textContent = parts.length ? parts.join(" · ") : "—";
  if (!state.outDir && state.settings && state.settings.outDir) state.outDir = state.settings.outDir;
  if (!$("outdir").value && state.outDir) $("outdir").value = state.outDir;
}

function resetProgress() {
  state.percent = 0;
  $("bar-fill").style.width = "0%";
  $("prog-pct").textContent = "0%";
  $("prog-stage").textContent = "准备中…";
  $("prog-msg").textContent = "Go 端正在启动管线…";
  const interp = document.querySelector('#stages li[data-stage="interpret"]');
  interp.classList.toggle("skip", state.mode === "facts");
  interp.innerHTML = '<span class="ico"></span>' +
    (state.mode === "facts" ? "LLM 解读（纯事实模式，跳过）" : "LLM 解读（" + modeLabel(state.mode) + "）");
  document.querySelectorAll("#stages li").forEach((li) => {
    li.classList.remove("done", "active", "err");
  });
}

function renderProgress(p) {
  if (!p) return;
  const pct = Math.max(state.percent, Math.min(100, Number(p.percent) || 0));
  state.percent = pct;
  $("bar-fill").style.width = pct + "%";
  $("prog-pct").textContent = pct + "%";
  if (p.message) $("prog-msg").textContent = p.message;

  const stage = p.stage;
  const idx = STAGE_ORDER.indexOf(stage);
  $("prog-stage").textContent =
    stage === "done" ? "完成"
    : stage === "error" ? "失败"
    : stage === "start" ? "启动"
    : (STAGE_LABEL[stage] || stage);

  STAGE_ORDER.forEach((s, k) => {
    const li = document.querySelector(`#stages li[data-stage="${s}"]`);
    if (!li) return;
    const skipped = li.classList.contains("skip");
    li.classList.toggle("done", stage === "done" || (idx >= 0 && k < idx) || (skipped && idx > k));
    li.classList.toggle("active", !skipped && k === idx && stage !== "done" && stage !== "error");
    li.classList.toggle("err", stage === "error" && k === idx);
  });

  if (stage === "done") {
    document.querySelectorAll("#stages li").forEach((li) => li.classList.add("done"));
  }
}

async function runAnalysis() {
  const api = App();
  if (!api) return toast("未检测到 Wails 运行时");
  if (state.running) return;
  if (!state.demoPath) { toast("请先选择 .dem 文件"); goto(1); return; }
  if (!state.player) { toast("请先选择焦点玩家"); goto(2); return; }

  state.running = true;
  hideNote("run-warning");
  resetProgress();
  const btn = $("btn-run");
  btn.disabled = true;
  btn.textContent = "分析中…";
  const s4 = $("s4-state");
  s4.textContent = "运行中"; s4.className = "badge amber";
  const t0 = Date.now();

  try {
    const res = await api.Run(state.demoPath, state.player, state.mode, $("outdir").value.trim());
    state.result = res;
    state.outDir = res.outDir;
    $("outdir").value = res.outDir;
    renderReport(res);
    if (res.warning) showNote("run-warning", res.warning, "warn");
    s4.textContent = "已完成"; s4.className = "badge emerald";
    // 兜底：即使最后一个进度事件丢失，也让进度条与阶段列表落到完成态。
    renderProgress({ stage: "done", percent: 100, message: "完成" });
    $("prog-msg").textContent = "完成：用时 " + fmtMs(res.durationMs || Date.now() - t0);
    markDone(4); markDone(5);
    unlock(5); unlock(6);
    goto(5);
    persistSettings({ outDir: res.outDir, mode: state.mode });
  } catch (e) {
    const msg = errMsg(e);
    showNote("run-warning", msg, "err");
    s4.textContent = "失败"; s4.className = "badge rose";
    toast(msg);
  } finally {
    state.running = false;
    btn.disabled = false;
    btn.textContent = "▶ 开始分析";
  }
}

$("btn-run").addEventListener("click", runAnalysis);
$("btn-outdir").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  try {
    const d = await api.SelectOutDir();
    if (d) { state.outDir = d; $("outdir").value = d; }
  } catch (e) { toast(errMsg(e)); }
});
$("btn-open-outdir").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  try { await api.OpenOutDir(); } catch (e) { toast(errMsg(e)); }
});

/* ================= 步骤 5：报告 ================= */

function badge(text, kind) {
  return `<span class="badge ${kind || ""}">${esc(text)}</span>`;
}

function renderReport(res) {
  $("report-meta").innerHTML = [
    badge(res.map || "未知地图", "cyan"),
    badge("T " + res.scoreT + " : " + res.scoreCT + " CT", "emerald"),
    badge("焦点 " + (res.focusName || "—"), "violet"),
    badge(res.totalRounds + " 回合", ""),
    badge(modeLabel(res.mode) + (res.interpreted ? " · 已解读" : " · 纯事实"), res.interpreted ? "cyan" : ""),
    badge("用时 " + fmtMs(res.durationMs), ""),
  ].join("");

  if (res.warning) showNote("report-warning", res.warning, "warn");
  else hideNote("report-warning");

  $("report-frame").srcdoc = res.html || "";
  $("md-render").innerHTML = mdToHtml(res.markdown || "");
  $("facts-body").innerHTML = renderFacts(res.summary || {}, res);
  if (["facts", "ollama", "api"].includes(res.mode)) $("regen-mode").value = res.mode;
}

function renderFacts(s, res) {
  const won = s.focusWon || 0;
  const lost = s.focusLost || 0;
  const total = won + lost;
  const rate = total ? Math.round((won / total) * 100) : 0;
  const top = (s.causes && s.causes[0]) || null;

  let html = '<div class="stat-grid">' +
    `<div class="stat good"><div class="v">${won}</div><div class="k">焦点方胜回合</div></div>` +
    `<div class="stat bad"><div class="v">${lost}</div><div class="k">焦点方丢回合</div></div>` +
    `<div class="stat"><div class="v">${rate}%</div><div class="k">胜率</div></div>` +
    `<div class="stat warn"><div class="v">${top ? top.count : 0}</div>` +
    `<div class="k">头号丢分原因${top ? "：" + esc(top.label) : ""}</div></div>` +
    "</div>";

  html += "<h3>丢分原因频次</h3>";
  if (!s.causes || !s.causes.length) {
    html += '<div class="note info">本场没有可归因的丢分回合。</div>';
  } else {
    html += '<table class="data"><thead><tr><th>原因</th><th>次数</th><th>占比</th><th>回合号</th></tr></thead><tbody>';
    s.causes.forEach((c) => {
      html += `<tr><td>${esc(c.label)}</td><td class="num">${c.count}</td>` +
        `<td>${esc(c.percent)}</td><td class="tags">${esc(c.rounds)}</td></tr>`;
    });
    html += "</tbody></table>";
  }

  const n = Math.min(3, (s.suspects || []).length);
  html += `<h3>最亏的丢分回合（Top ${n}）</h3>`;
  if (!s.suspects || !s.suspects.length) {
    html += '<div class="note ok">没有标记出可疑回合。要么打得很好，要么数据不足。</div>';
  } else {
    html += '<table class="data"><thead><tr><th>回合</th><th>标签</th><th>代价</th><th>原因</th></tr></thead><tbody>';
    s.suspects.forEach((x) => {
      html += `<tr><td class="round">R${x.round}</td><td class="tags">${esc(x.tags)}</td>` +
        `<td class="cost">${Number(x.cost || 0).toFixed(2)}</td><td>${esc(x.reason)}</td></tr>`;
    });
    html += "</tbody></table>" +
      '<p class="hint" style="margin-top:8px">代价 = WinCost（这分不该丢的程度，0-1），排序即报告 Top3 顺序。</p>';
  }

  html += "<h3>值得保留的好回合</h3>";
  if (!s.goodRounds || !s.goodRounds.length) {
    html += '<div class="note info">本场未识别出值得保留的好回合。</div>';
  } else {
    html += '<table class="data"><thead><tr><th>回合</th><th>为什么好</th><th>高光</th></tr></thead><tbody>';
    s.goodRounds.forEach((g) => {
      html += `<tr><td class="round">R${g.round}</td><td>${esc(g.reason)}</td><td>${esc(g.highlight)}</td></tr>`;
    });
    html += "</tbody></table>";
  }

  if (s.keySwings && s.keySwings.length) {
    html += "<h3>比分关键节点</h3><ul>";
    s.keySwings.forEach((k) => { html += `<li>${esc(k)}</li>`; });
    html += "</ul>";
  }

  html += `<p class="hint" style="margin-top:18px">产物目录：<code>${esc(res.outDir || "")}</code></p>`;
  return html;
}

document.querySelectorAll(".tab").forEach((t) =>
  t.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((x) => x.classList.remove("active"));
    document.querySelectorAll(".pane").forEach((x) => x.classList.remove("active"));
    t.classList.add("active");
    $("pane-" + t.dataset.tab).classList.add("active");
  }));

$("btn-open-report").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  try { await api.OpenReportHTML(); } catch (e) { toast(errMsg(e)); }
});

$("btn-copy-md").addEventListener("click", async () => {
  const md = state.result && state.result.markdown;
  if (!md) return toast("还没有报告可复制");
  try {
    if (window.runtime && window.runtime.ClipboardSetText) {
      await window.runtime.ClipboardSetText(md);
    } else {
      await navigator.clipboard.writeText(md);
    }
    toast("Markdown 已复制到剪贴板", true);
  } catch (e) { toast("复制失败：" + errMsg(e)); }
});

$("btn-regen").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  const mode = $("regen-mode").value;
  const btn = $("btn-regen");
  btn.disabled = true; btn.textContent = "生成中…";
  try {
    const res = await api.RegenerateReport(mode, state.outDir);
    state.result = res;
    state.mode = res.mode || mode;
    renderReport(res);
    setMode(state.mode, { silent: true });
    if (res.warning) showNote("report-warning", res.warning, "warn");
    else hideNote("report-warning");
    toast("报告已重新生成（" + modeLabel(state.mode) + "）", true);
  } catch (e) {
    toast(errMsg(e));
  } finally {
    btn.disabled = false; btn.textContent = "重新生成报告";
  }
});

$("btn-to-step6").addEventListener("click", () => goto(6));

/* ================= 步骤 6：追问 ================= */

function addMsg(text, cls) {
  const d = document.createElement("div");
  d.className = "msg " + cls;
  d.textContent = text;
  $("chat").appendChild(d);
  d.scrollIntoView({ block: "end" });
  return d;
}

function addRichMsg(md) {
  const d = document.createElement("div");
  d.className = "msg a rich";
  d.innerHTML = mdToHtml(md);
  $("chat").appendChild(d);
  d.scrollIntoView({ block: "end" });
  return d;
}

async function ask(q) {
  const api = App();
  if (!api) return toast("未检测到 Wails 运行时");
  q = String(q || "").trim();
  if (!q) { toast("问题不能为空"); return; }
  if (!state.result) { toast("请先运行一次分析"); return; }

  addMsg(q, "q");
  const thinking = addMsg("检索事实并组织回答…", "a thinking");
  const btn = $("btn-ask");
  btn.disabled = true;
  try {
    const ans = await api.Ask(state.demoPath, q);
    thinking.remove();
    addRichMsg(ans);
  } catch (e) {
    thinking.remove();
    addMsg("出错：" + errMsg(e), "a");
  } finally {
    btn.disabled = false;
  }
}

$("btn-ask").addEventListener("click", () => {
  const input = $("chat-input");
  const q = input.value;
  input.value = "";
  ask(q);
});
$("chat-input").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    const q = e.target.value;
    e.target.value = "";
    ask(q);
  }
});
document.querySelectorAll("#suggestions .chip").forEach((c) =>
  c.addEventListener("click", () => ask(c.textContent)));

/* ================= 设置 ================= */

function applySettingsToUI(s) {
  state.settings = s;
  $("ollama-url").value = s.ollamaURL || "";
  $("ollama-model").value = s.ollamaModel || "";
  if (s.outDir) state.outDir = s.outDir;
  $("outdir").value = state.outDir || "";
  const provider = s.provider || "";
  const model = s.model || (provider === "openai" ? "gpt-4o-mini" : provider === "anthropic" ? "claude-opus-5-5" : "跟随环境变量");
  $("api-summary").textContent = "当前：" + (provider || "跟随环境变量") + " · 模型 " + model +
    (s.baseURL ? " · 网关 " + s.baseURL : "");
  $("api-state").textContent = (s.apiKey || s.provider) ? "已配置" : "未配置";
  $("api-state").className = "badge " + ((s.apiKey || s.provider) ? "emerald" : "");
  setMode(s.mode || "facts", { silent: true });
  updateRunSummary();
}

async function openSettings() {
  const api = App();
  if (!api) return toast("未检测到 Wails 运行时");
  try {
    const s = await api.GetSettings();
    $("set-provider").value = s.provider || "";
    $("set-key").value = s.apiKey || "";
    $("set-base").value = s.baseURL || "";
    $("set-model").value = s.model || "";
    $("set-ollama-url").value = s.ollamaURL || "";
    $("set-ollama-model").value = s.ollamaModel || "";
    $("set-outdir").value = s.outDir || "";
    $("settings-modal").classList.remove("hidden");
  } catch (e) { toast(errMsg(e)); }
}

$("btn-settings").addEventListener("click", openSettings);
$("set-cancel").addEventListener("click", () => $("settings-modal").classList.add("hidden"));
$("settings-modal").addEventListener("click", (e) => {
  if (e.target === $("settings-modal")) $("settings-modal").classList.add("hidden");
});

function settingsPayloadFromForm() {
  return {
    mode: state.mode,
    provider: $("set-provider").value,
    apiKey: $("set-key").value.trim(),
    baseURL: $("set-base").value.trim(),
    model: $("set-model").value.trim(),
    ollamaURL: $("set-ollama-url").value.trim(),
    ollamaModel: $("set-ollama-model").value.trim(),
    outDir: $("set-outdir").value.trim(),
    useLLM: state.mode !== "facts",
  };
}

$("set-save").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  const payload = settingsPayloadFromForm();
  try {
    await api.SaveSettings(payload);
    applySettingsToUI(Object.assign({}, state.settings || {}, payload));
    $("settings-modal").classList.add("hidden");
    toast("设置已保存到本机", true);
  } catch (e) { toast(errMsg(e)); }
});

$("set-test").addEventListener("click", async () => {
  const api = App();
  if (!api) return;
  try {
    // 先落盘，CheckLLM 依赖后端已保存的配置。
    await api.SaveSettings(settingsPayloadFromForm());
    toast(await api.CheckLLM(state.mode), true);
  } catch (e) { toast(errMsg(e)); }
});

let saveTimer = null;
function persistSettings(patch) {
  const api = App();
  if (!api) return;
  state.settings = Object.assign({}, state.settings || {}, patch);
  if (patch.mode) state.mode = patch.mode;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    try {
      const s = state.settings;
      await api.SaveSettings({
        mode: s.mode || state.mode,
        provider: s.provider || "",
        apiKey: s.apiKey || "",
        baseURL: s.baseURL || "",
        model: s.model || "",
        ollamaURL: s.ollamaURL || "",
        ollamaModel: s.ollamaModel || "",
        outDir: s.outDir || state.outDir || "",
        useLLM: s.useLLM || false,
      });
    } catch (e) {
      console.warn("保存设置失败", e);
    }
  }, 400);
}

/* ================= 启动 ================= */

function subscribeProgress() {
  if (!window.runtime || !window.runtime.EventsOn) return;
  try {
    window.runtime.EventsOn("run:progress", (p) => renderProgress(p));
    window.runtime.EventsOn("demo:dropped", (path) => {
      if (path) probe(String(path));
    });
  } catch (e) {
    console.warn("事件订阅失败", e);
  }
}

async function boot() {
  subscribeProgress();
  const api = App();
  if (!api) {
    showNote("s1-error", "未检测到 Wails 运行时。请用桌面版启动（wails dev 或构建后的 App），浏览器直接打开无法调用本地能力。", "err");
    return;
  }
  try {
    $("workdir").textContent = await api.WorkDir();
  } catch (e) { /* 忽略 */ }
  try {
    applySettingsToUI(await api.GetSettings());
  } catch (e) {
    console.warn("读取设置失败", e);
  }
  updateRunSummary();
}

boot();
