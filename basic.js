/* ============================================================
   MAGLAB · 磁轴调校实验室
   纯前端、零依赖。所有数据只保存在本机 localStorage。
   ============================================================ */

(function () {
  'use strict';

  /* ------------------------------------------------------------
     Utilities
  ------------------------------------------------------------ */

  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
  const now = () => performance.now();
  const clamp = (v, min, max) => Math.min(max, Math.max(min, v));
  const norm = (v, min, max) => clamp((v - min) / (max - min || 1), 0, 1);
  const roundTo = (v, step) => Math.round(v / step) * step;
  const mean = (arr) => (arr.length ? arr.reduce((a, b) => a + b, 0) / arr.length : 0);

  function median(arr) {
    if (!arr || !arr.length) return 0;
    const sorted = [...arr].sort((a, b) => a - b);
    const mid = Math.floor(sorted.length / 2);
    return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
  }

  function std(arr) {
    if (!arr || arr.length < 2) return 0;
    const m = mean(arr);
    return Math.sqrt(mean(arr.map((v) => (v - m) ** 2)));
  }

  function formatClock(ms) {
    const total = Math.ceil(Math.max(0, ms) / 1000);
    return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`;
  }

  function formatSigned(ms) {
    if (ms === null || ms === undefined || Number.isNaN(ms)) return '—';
    const value = Math.round(ms);
    if (Math.abs(value) < 1) return '0ms';
    return `${value > 0 ? '+' : ''}${value}ms`;
  }

  function timingLabel(ms) {
    if (ms === null || ms === undefined || Number.isNaN(ms)) return '—';
    const label = formatSigned(ms);
    if (ms < -35) return `${label} 重叠`;
    if (ms > 90) return `${label} 空档`;
    return `${label} 干净`;
  }

  function keyName(code) {
    return {
      KeyW: 'W', KeyA: 'A', KeyS: 'S', KeyD: 'D',
      Space: 'SPACE', ShiftLeft: 'SHIFT', ControlLeft: 'CTRL'
    }[code] || code;
  }

  /* ------------------------------------------------------------
     State
  ------------------------------------------------------------ */

  const STATE_KEY = 'maglab_state_v1';
  const SCREENS = ['home', 'profile', 'rest', 'tap', 'strafe', 'hold', 'result'];

  const DEFAULT_PROFILE = {
    totalTravel: 3.4,
    currentActuation: 2.0,
    rtOn: false,
    currentRt: 0.15,
    pressStyle: 'balanced',
    fingersRest: true,
    misTouch: 2,
    playstyle: 'rifler',
    goals: ['balance']
  };

  function freshState() {
    return {
      version: 2,
      profile: { ...DEFAULT_PROFILE, goals: [...DEFAULT_PROFILE.goals] },
      rest: null,
      tap: null,
      strafe: null,
      hold: null,
      retest: null,
      completed: {},
      sound: true
    };
  }

  function loadState() {
    try {
      const raw = localStorage.getItem(STATE_KEY);
      if (!raw) return freshState();
      const parsed = JSON.parse(raw);
      const state = freshState();
      if (!parsed || typeof parsed !== 'object') return state;

      if (parsed.profile && typeof parsed.profile === 'object') {
        state.profile = { ...state.profile, ...parsed.profile };
      }

      const profile = state.profile;
      profile.totalTravel = clamp(Number(profile.totalTravel) || 3.4, 2.5, 4.5);
      profile.currentActuation = clamp(
        Number(profile.currentActuation) || 2.0,
        0.1,
        Math.max(0.5, profile.totalTravel - 0.2)
      );
      profile.currentRt = clamp(Number(profile.currentRt) || 0.15, 0.05, 0.30);
      profile.misTouch = clamp(Number(profile.misTouch) || 2, 1, 5);
      profile.rtOn = Boolean(profile.rtOn);
      profile.fingersRest = Boolean(profile.fingersRest);
      if (!['light', 'balanced', 'heavy'].includes(profile.pressStyle)) profile.pressStyle = 'balanced';
      if (!['entry', 'rifler', 'awper', 'igl', 'casual'].includes(profile.playstyle)) profile.playstyle = 'rifler';
      if (!Array.isArray(profile.goals) || profile.goals.length === 0) {
        profile.goals = ['balance'];
      }
      ['rest', 'tap', 'strafe', 'hold', 'retest'].forEach((key) => {
        if (parsed[key] !== undefined) state[key] = parsed[key];
      });
      if (parsed.completed && typeof parsed.completed === 'object') {
        state.completed = { ...parsed.completed };
      }
      if (typeof parsed.sound === 'boolean') state.sound = parsed.sound;
      return state;
    } catch (error) {
      return freshState();
    }
  }

  let state = loadState();
  let demoMode = false;
  let demoBackup = null;

  function saveState() {
    if (demoMode) return;
    try {
      localStorage.setItem(STATE_KEY, JSON.stringify(state));
    } catch (error) {
      /* localStorage 不可用时静默失败，测试仍可进行 */
    }
  }

  function buildDemoState() {
    const taps = [];
    let tapTime = 210;
    for (let i = 0; i < 60; i += 1) {
      const hold = 54 + Math.round(32 * Math.abs(Math.sin(i * 1.7)));
      taps.push({ down: Math.round(tapTime), up: Math.round(tapTime + hold) });
      tapTime += hold + 92 + Math.round(32 * Math.abs(Math.cos(i * 2.1))) + (i % 9 === 0 ? 26 : 0);
      if (tapTime > 7850) break;
    }

    const signedValues = [12, -24, 8, 36, -52, 18, 5, -8, 44, -32, 10, -18];
    const transitions = [];
    let cueTime = 1400;
    signedValues.forEach((signed, index) => {
      const reaction = 150 + ((index * 19) % 65);
      const newDown = cueTime + reaction;
      const oldUp = newDown - signed;
      transitions.push({
        index,
        from: index % 2 === 0 ? 'KeyA' : 'KeyD',
        to: index % 2 === 0 ? 'KeyD' : 'KeyA',
        cue: cueTime,
        oldUp,
        newDown,
        signed,
        reaction,
        early: false,
        capped: false,
        prePressed: false,
        clean: signed >= -35 && signed <= 60
      });
      cueTime += 1850;
    });

    return {
      version: 2,
      profile: {
        totalTravel: 3.4,
        currentActuation: 2.0,
        rtOn: false,
        currentRt: 0.15,
        pressStyle: 'balanced',
        fingersRest: true,
        misTouch: 2,
        playstyle: 'rifler',
        goals: ['speed']
      },
      rest: {
        duration: 20,
        skipped: false,
        events: [{ code: 'KeyA', key: 'A', t: 8400 }]
      },
      tap: { duration: 8, taps },
      strafe: {
        transitions,
        rounds: 12,
        duration: 25,
        finishedAt: Date.now()
      },
      hold: {
        duration: 15,
        breaks: [{ start: 7.2, end: 7.34 }],
        finishedAt: Date.now()
      },
      retest: null,
      completed: { profile: true, rest: true, tap: true, strafe: true, hold: true },
      sound: true
    };
  }

  function loadDemo() {
    if (demoMode) return;
    demoBackup = state;
    demoMode = true;
    state = buildDemoState();
    retestMode = false;
    go('result');
    showToast('示例报告已加载，数据为模拟值');
  }

  function exitDemo() {
    if (!demoMode) return;
    state = demoBackup || freshState();
    demoBackup = null;
    demoMode = false;
    $('#demoBanner')?.classList.add('hidden');
    try {
      if (window.location.hash === '#demo') {
        history.replaceState(null, '', window.location.pathname + window.location.search);
      }
    } catch (error) {
      /* ignore */
    }
    go('home');
    showToast('已退出示例');
  }

  let currentScreen = 'home';
  let activeSession = null;
  let retestMode = false;
  let lastStrafeSummaryRetest = false;
  let customTravelActive = false;
  let lastAnalysis = null;
  let toastTimer = null;
  let audioCtx = null;

  const pressed = new Set();

  /* ------------------------------------------------------------
     Audio / Toast
  ------------------------------------------------------------ */

  function getAudio() {
    if (!audioCtx) {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      if (!Ctx) return null;
      audioCtx = new Ctx();
    }
    if (audioCtx.state === 'suspended') {
      audioCtx.resume().catch(() => {});
    }
    return audioCtx;
  }

  function beep(freq = 880, duration = 0.06, volume = 0.035, type = 'sine') {
    if (!state.sound) return;
    try {
      const ctx = getAudio();
      if (!ctx) return;
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.type = type;
      osc.frequency.value = freq;
      gain.gain.setValueAtTime(volume, ctx.currentTime);
      gain.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + duration);
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start();
      osc.stop(ctx.currentTime + duration);
    } catch (error) {
      /* 忽略音频错误 */
    }
  }

  function showToast(message) {
    const el = $('#toast');
    if (!el) return;
    el.textContent = message;
    el.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('show'), 2800);
  }

  function flashElement(el) {
    if (!el) return;
    el.classList.remove('active');
    void el.offsetWidth;
    el.classList.add('active');
  }

  /* ------------------------------------------------------------
     Session helper (timers + listeners cleanup)
  ------------------------------------------------------------ */

  function createSession() {
    const timeouts = new Set();
    const intervals = new Set();
    const rafs = new Set();
    const cleanups = [];

    const session = {
      aborted: false,
      setTimeout(fn, ms) {
        if (session.aborted) return 0;
        const id = setTimeout(() => {
          timeouts.delete(id);
          if (!session.aborted) fn();
        }, ms);
        timeouts.add(id);
        return id;
      },
      clearTimeout(id) {
        if (!id) return;
        clearTimeout(id);
        timeouts.delete(id);
      },
      setInterval(fn, ms) {
        if (session.aborted) return 0;
        const id = setInterval(() => {
          if (!session.aborted) fn();
        }, ms);
        intervals.add(id);
        return id;
      },
      raf(fn) {
        if (session.aborted) return 0;
        const id = requestAnimationFrame((t) => {
          rafs.delete(id);
          if (!session.aborted) fn(t);
        });
        rafs.add(id);
        return id;
      },
      on(target, type, fn, opts) {
        target.addEventListener(type, fn, opts);
        cleanups.push(() => target.removeEventListener(type, fn, opts));
      },
      abort() {
        if (session.aborted) return;
        session.aborted = true;
        timeouts.forEach((id) => clearTimeout(id));
        intervals.forEach((id) => clearInterval(id));
        rafs.forEach((id) => cancelAnimationFrame(id));
        timeouts.clear();
        intervals.clear();
        rafs.clear();
        cleanups.forEach((fn) => fn());
        cleanups.length = 0;
      }
    };

    return session;
  }

  function abortActive() {
    if (activeSession) {
      const session = activeSession;
      activeSession = null;
      session.abort();
    }
  }

  /* ------------------------------------------------------------
     Navigation
  ------------------------------------------------------------ */

  function go(name, options = {}) {
    if (!SCREENS.includes(name)) return;
    if (demoMode && name !== 'result') {
      if (name === 'home') {
        exitDemo();
        return;
      }
      showToast('示例模式：先点击「退出示例」再开始测试');
      return;
    }
    abortActive();
    if (name !== 'strafe' && name !== 'result') retestMode = false;
    if (options.retest === true) retestMode = true;

    currentScreen = name;
    $$('.screen').forEach((screen) => {
      screen.classList.toggle('is-active', screen.id === `screen-${name}`);
    });
    updateStepper();
    updateProgress();
    window.scrollTo(0, 0);

    if (name === 'profile') syncProfileUI();
    if (name === 'rest') prepareRest();
    if (name === 'tap') prepareTap();
    if (name === 'strafe') prepareStrafe();
    if (name === 'hold') prepareHold();
    if (name === 'result') renderResult();
    if (name === 'home') updateHomeResume();

    saveState();
  }

  function updateHomeResume() {
    const button = $('#resumeBtn');
    if (!button) return;
    button.classList.toggle('hidden', !(state.tap && state.strafe));
  }

  function updateStepper() {
    $$('.step').forEach((step) => {
      const target = step.dataset.step;
      step.classList.toggle('is-active', target === currentScreen);
      step.classList.toggle('is-done', Boolean(state.completed[target]));
    });
  }

  function updateProgress() {
    const index = SCREENS.indexOf(currentScreen);
    const percent = currentScreen === 'home' ? 0 : (index / (SCREENS.length - 1)) * 100;
    const bar = $('#progressBar');
    if (bar) bar.style.width = `${percent}%`;
  }

  /* ------------------------------------------------------------
     Hero keyboard decoration
  ------------------------------------------------------------ */

  function renderHeroKeyboard() {
    const board = $('#heroKeyboard');
    if (!board) return;

    const rows = [
      ['Esc', '1', '2', '3', '4', '5', '6', '7', '8', '9', '0', '-', '='],
      ['Tab', 'Q', 'W', 'E', 'R', 'T', 'Y', 'U', 'I', 'O', 'P', '[', ']'],
      ['Caps', 'A', 'S', 'D', 'F', 'G', 'H', 'J', 'K', 'L', ';', "'"],
      ['Shift', 'Z', 'X', 'C', 'V', 'B', 'N', 'M', ',', '.', '/', 'Shift'],
      ['Ctrl', 'Win', 'Alt', 'Space', 'Alt', 'Fn', 'Ctrl']
    ];

    const hot = new Set(['W', 'A', 'S', 'D']);

    board.innerHTML = rows.map((row) => {
      const keys = row.map((key) => {
        const classes = ['kb-key'];
        if (key === 'Space') classes.push('space');
        if (['Tab', 'Caps', 'Shift', 'Ctrl', 'Win', 'Alt', 'Fn'].includes(key)) classes.push('wide');
        if (hot.has(key)) classes.push('hot');
        if (key === 'A' || key === 'D') classes.push('pulse');
        if (key === 'D') classes.push('delay');
        return `<div class="${classes.join(' ')}">${key === 'Space' ? '' : key}</div>`;
      }).join('');
      return `<div class="kb-row">${keys}</div>`;
    }).join('');
  }

  /* ------------------------------------------------------------
     Profile
  ------------------------------------------------------------ */

  function setSelected(group, value) {
    if (!group) return;
    $$('button[data-value]', group).forEach((button) => {
      button.classList.toggle('is-selected', button.dataset.value === String(value));
    });
  }

  function syncProfileUI() {
    const profile = state.profile;
    const standard = [3.4, 3.6, 4.0];
    const isStandard = standard.some((value) => Math.abs(value - Number(profile.totalTravel)) < 0.001);

    const travelGroup = $('[data-choice="totalTravel"]');
    if (travelGroup) setSelected(travelGroup, isStandard ? String(profile.totalTravel) : 'custom');

    const customRow = $('#customTravelRow');
    const showCustom = customTravelActive || !isStandard;
    customRow?.classList.toggle('hidden', !showCustom);

    const customInput = $('#customTravel');
    if (customInput && document.activeElement !== customInput) {
      customInput.value = Number(profile.totalTravel).toFixed(2);
    }

    const actuation = $('#currentActuation');
    const actuationMax = roundTo(Math.max(0.5, Number(profile.totalTravel) - 0.2), 0.05);
    if (actuation) {
      actuation.max = String(actuationMax);
      const value = clamp(Number(profile.currentActuation), 0.1, actuationMax);
      profile.currentActuation = value;
      actuation.value = String(value);
      $('#currentActuationOut').innerHTML = `${value.toFixed(2)} <small>mm</small>`;
    }
    const travelMax = $('#travelMax');
    if (travelMax) travelMax.textContent = actuationMax.toFixed(2);

    $('#rtToggle')?.setAttribute('aria-checked', String(Boolean(profile.rtOn)));
    const rtLabel = $('#rtToggleLabel');
    if (rtLabel) rtLabel.textContent = profile.rtOn ? '已开启' : '未开启';
    $('#currentRtRow')?.classList.toggle('hidden', !profile.rtOn);

    const rtInput = $('#currentRt');
    if (rtInput) {
      profile.currentRt = clamp(Number(profile.currentRt), 0.05, 0.30);
      rtInput.value = String(profile.currentRt);
      $('#currentRtOut').textContent = `${profile.currentRt.toFixed(2)} mm`;
    }

    $('#fingersToggle')?.setAttribute('aria-checked', String(Boolean(profile.fingersRest)));

    const styleGroup = $('[data-choice="pressStyle"]');
    if (styleGroup) setSelected(styleGroup, profile.pressStyle);

    const misTouchGroup = $('[data-choice="misTouch"]');
    if (misTouchGroup) setSelected(misTouchGroup, profile.misTouch);

    const playstyleGroup = $('[data-choice="playstyle"]');
    if (playstyleGroup) setSelected(playstyleGroup, profile.playstyle);

    const goalsGroup = $('[data-choice="goals"]');
    if (goalsGroup) {
      $$('button[data-value]', goalsGroup).forEach((button) => {
        button.classList.toggle('is-selected', profile.goals.includes(button.dataset.value));
      });
    }

    updateProfileSummary();
  }

  function updateProfileSummary() {
    const profile = state.profile;
    const travel = $('#sumTravel');
    const actuation = $('#sumActuation');
    const rt = $('#sumRt');
    const style = $('#sumStyle');
    if (travel) travel.textContent = `${Number(profile.totalTravel).toFixed(2)} mm`;
    if (actuation) actuation.textContent = `${Number(profile.currentActuation).toFixed(2)} mm`;
    if (rt) rt.textContent = profile.rtOn ? `开启 · ${Number(profile.currentRt).toFixed(2)}mm` : '未开启';
    if (style) {
      style.textContent = {
        light: '轻点悬停',
        balanced: '均衡',
        heavy: '习惯到底'
      }[profile.pressStyle] || '均衡';
    }
  }

  function bindProfile() {
    $$('[data-choice]').forEach((group) => {
      group.addEventListener('click', (event) => {
        const button = event.target.closest('button[data-value]');
        if (!button) return;
        const key = group.dataset.choice;
        const value = button.dataset.value;

        if (key === 'totalTravel') {
          if (value === 'custom') {
            customTravelActive = true;
            syncProfileUI();
            const input = $('#customTravel');
            input?.focus();
            input?.select();
          } else {
            customTravelActive = false;
            state.profile.totalTravel = Number(value);
            state.profile.currentActuation = Math.min(
              Number(state.profile.currentActuation),
              Number(value) - 0.2
            );
            syncProfileUI();
          }
        } else if (key === 'misTouch') {
          state.profile.misTouch = Number(value);
          syncProfileUI();
        } else if (key === 'goals') {
          const goals = state.profile.goals;
          if (value === 'balance') {
            state.profile.goals = ['balance'];
          } else {
            const withoutBalance = goals.filter((goal) => goal !== 'balance');
            if (withoutBalance.includes(value)) {
              const remaining = withoutBalance.filter((goal) => goal !== value);
              state.profile.goals = remaining.length ? remaining : [value];
            } else {
              state.profile.goals = [...withoutBalance, value];
            }
          }
          syncProfileUI();
        } else {
          state.profile[key] = value;
          syncProfileUI();
        }

        saveState();
      });
    });

    const customInput = $('#customTravel');
    customInput?.addEventListener('input', () => {
      const value = clamp(Number(customInput.value) || 3.4, 2.5, 4.5);
      state.profile.totalTravel = value;
      customTravelActive = true;
      state.profile.currentActuation = Math.min(
        Number(state.profile.currentActuation),
        value - 0.2
      );
      const actuation = $('#currentActuation');
      const actuationMax = roundTo(Math.max(0.5, value - 0.2), 0.05);
      if (actuation) {
        actuation.max = String(actuationMax);
        actuation.value = String(state.profile.currentActuation);
      }
      const out = $('#currentActuationOut');
      if (out) out.innerHTML = `${Number(state.profile.currentActuation).toFixed(2)} <small>mm</small>`;
      const travelMax = $('#travelMax');
      if (travelMax) travelMax.textContent = actuationMax.toFixed(2);
      updateProfileSummary();
      saveState();
    });

    customInput?.addEventListener('blur', () => {
      customInput.value = Number(state.profile.totalTravel).toFixed(2);
    });

    $('#currentActuation')?.addEventListener('input', (event) => {
      const value = Number(event.target.value);
      state.profile.currentActuation = value;
      const out = $('#currentActuationOut');
      if (out) out.innerHTML = `${value.toFixed(2)} <small>mm</small>`;
      updateProfileSummary();
      saveState();
    });

    $('#currentRt')?.addEventListener('input', (event) => {
      const value = Number(event.target.value);
      state.profile.currentRt = value;
      const out = $('#currentRtOut');
      if (out) out.textContent = `${value.toFixed(2)} mm`;
      updateProfileSummary();
      saveState();
    });

    $('#rtToggle')?.addEventListener('click', () => {
      state.profile.rtOn = !state.profile.rtOn;
      syncProfileUI();
      saveState();
      beep(state.profile.rtOn ? 720 : 380, 0.05, 0.02);
    });

    $('#fingersToggle')?.addEventListener('click', () => {
      state.profile.fingersRest = !state.profile.fingersRest;
      syncProfileUI();
      saveState();
      beep(state.profile.fingersRest ? 720 : 380, 0.05, 0.02);
    });

    $('#profileNext')?.addEventListener('click', () => {
      state.completed.profile = true;
      saveState();
      go('rest');
    });
  }

  /* ------------------------------------------------------------
     REST TEST
  ------------------------------------------------------------ */

  const REST_KEYS = new Set(['KeyW', 'KeyA', 'KeyS', 'KeyD', 'Space', 'ShiftLeft', 'ControlLeft']);
  const REST_DURATION = 20000;

  function resetRestVisual() {
    $('#restTimer').textContent = '00:20';
    $('#restCount').textContent = '0';
    $('#restKeysHit').textContent = '—';
    $('#restState').textContent = '准备';
    $('#restOrb').style.setProperty('--p', '100');
    $('#restOrb').classList.remove('is-running');
    $('#restHint').textContent = '点击下方按钮开始，然后把双手放回键盘，像平时一样。';
    $$('.mini-key', $('#restKeys')).forEach((key) => key.classList.remove('active'));
  }

  function prepareRest() {
    resetRestVisual();
    const start = $('#restStart');
    const next = $('#restNext');
    const skip = $('#restSkip');
    start.disabled = false;
    start.classList.remove('hidden');
    next.classList.add('hidden');
    skip.classList.remove('hidden');

    const data = state.rest;
    if (!data) {
      start.textContent = '开始 20 秒测试';
      return;
    }

    start.textContent = '重新测试';
    next.classList.remove('hidden');
    skip.classList.add('hidden');

    if (data.skipped) {
      $('#restState').textContent = '已跳过';
      $('#restTimer').textContent = '00:00';
      $('#restOrb').style.setProperty('--p', '0');
      $('#restHint').textContent = '已跳过静置测试。方案会更多依赖问卷和其余测试。';
      return;
    }

    const events = data.events || [];
    const keys = [...new Set(events.map((event) => event.key))];
    $('#restCount').textContent = String(events.length);
    $('#restKeysHit').textContent = keys.length ? keys.join(' / ') : '—';
    $('#restState').textContent = '完成';
    $('#restTimer').textContent = '00:00';
    $('#restOrb').style.setProperty('--p', '0');
    $('#restHint').textContent = events.length
      ? `上次记录到 ${events.length} 次误触。建议加大顶部死区。`
      : '上次静置 20 秒没有误触，手部静置压力很干净。';
  }

  function startRestTest() {
    abortActive();
    const session = createSession();
    activeSession = session;

    const events = [];
    let phase = 'countdown';
    let count = 3;
    let startedAt = 0;

    resetRestVisual();
    $('#restStart').disabled = true;
    $('#restStart').textContent = '测试中…';
    $('#restSkip').classList.add('hidden');
    $('#restNext').classList.add('hidden');
    $('#restState').textContent = '3';
    $('#restHint').textContent = '手指自然放上来，不要刻意控制。';

    const onKeyDown = (event) => {
      if (phase !== 'running') return;
      if (event.code === 'Space') event.preventDefault();
      if (event.repeat) return;
      if (!REST_KEYS.has(event.code)) return;

      const elapsed = Math.round(now() - startedAt);
      events.push({ code: event.code, key: keyName(event.code), t: elapsed });
      const chip = $(`.mini-key[data-code="${event.code}"]`, $('#restKeys'));
      chip?.classList.add('active');
      $('#restCount').textContent = String(events.length);
      const keys = [...new Set(events.map((item) => item.key))];
      $('#restKeysHit').textContent = keys.join(' / ');
      flashElement($('#restFlash'));
      beep(300 + Math.min(events.length, 8) * 24, 0.05, 0.025);
    };

    const onKeyUp = (event) => {
      $(`.mini-key[data-code="${event.code}"]`, $('#restKeys'))?.classList.remove('active');
    };

    session.on(window, 'keydown', onKeyDown);
    session.on(window, 'keyup', onKeyUp);

    const countdown = () => {
      if (session.aborted) return;
      if (count >= 1) {
        $('#restState').textContent = String(count);
        beep(440, 0.06, 0.03);
        count -= 1;
        session.setTimeout(countdown, 800);
      } else {
        run();
      }
    };

    const run = () => {
      phase = 'running';
      startedAt = now();
      $('#restState').textContent = '放松';
      $('#restOrb').classList.add('is-running');
      $('#restHint').textContent = '保持自然。不要看键盘，也不要为了通过测试抬手指。';
      session.setInterval(tick, 50);
    };

    const tick = () => {
      if (phase !== 'running') return;
      const elapsed = now() - startedAt;
      const remaining = Math.max(0, REST_DURATION - elapsed);
      $('#restTimer').textContent = formatClock(remaining);
      $('#restOrb').style.setProperty('--p', String((remaining / REST_DURATION) * 100));
      if (elapsed >= REST_DURATION) finish();
    };

    const finish = () => {
      if (phase === 'done') return;
      phase = 'done';
      session.abort();
      if (activeSession === session) activeSession = null;

      state.rest = { duration: 20, events, skipped: false };
      state.completed.rest = true;
      saveState();

      $('#restOrb').classList.remove('is-running');
      $('#restOrb').style.setProperty('--p', '0');
      $$('.mini-key', $('#restKeys')).forEach((key) => key.classList.remove('active'));
      $('#restTimer').textContent = '00:00';
      $('#restState').textContent = '完成';
      $('#restStart').disabled = false;
      $('#restStart').textContent = '重新测试';
      $('#restNext').classList.remove('hidden');
      $('#restSkip').classList.add('hidden');
      $('#restHint').textContent = events.length
        ? `记录到 ${events.length} 次误触。建议加大顶部死区。`
        : '静置 20 秒没有误触，手部静置压力很干净。';
      beep(660, 0.12, 0.04);
    };

    session.setTimeout(countdown, 300);
  }

  function skipRest() {
    abortActive();
    state.rest = { skipped: true };
    state.completed.rest = true;
    saveState();
    prepareRest();
    showToast('已跳过静置测试');
  }

  /* ------------------------------------------------------------
     TAP TEST
  ------------------------------------------------------------ */

  const TAP_DURATION = 8000;

  function tapMetrics(data) {
    if (!data) return null;
    const taps = (data.taps || []).filter((tap) => tap && tap.up !== null && tap.up !== undefined && tap.up >= tap.down);
    const holds = taps.map((tap) => tap.up - tap.down);
    const downs = taps.map((tap) => tap.down);
    const intervals = [];
    for (let i = 1; i < downs.length; i += 1) {
      intervals.push(downs[i] - downs[i - 1]);
    }
    const duration = data.duration || 8;
    const tps = taps.length / duration;
    const intervalMean = mean(intervals);
    const cv = intervals.length > 1 ? std(intervals) / (intervalMean || 1) : 0;
    return {
      count: taps.length,
      taps,
      holds,
      intervals,
      tps,
      cv,
      bounce: intervals.filter((value) => value < 30).length,
      holdMedian: median(holds),
      minInterval: intervals.length ? Math.min(...intervals) : 0
    };
  }

  function renderTapBars(intervals) {
    const container = $('#tapBars');
    if (!container) return;
    const recent = intervals.slice(-38);
    const max = Math.max(120, ...recent);
    container.innerHTML = recent.map((value) => {
      const height = clamp((value / max) * 100, 8, 100);
      const cls = value < 30 ? 'bar bounce' : value < 80 ? 'bar hot' : 'bar';
      return `<div class="${cls}" style="height:${height.toFixed(0)}%"></div>`;
    }).join('');
  }

  function resetTapVisual() {
    $('#tapTimer').textContent = '8.0s';
    $('#tapSideTimer').textContent = '8.0s';
    $('#tapLiveTps').textContent = '0.0';
    $('#tapLiveCount').textContent = '0 次';
    $('#tapLiveHint').textContent = '等待开始';
    $('#tapMinInterval').textContent = '—';
    $('#tapHoldLive').textContent = '—';
    $('#tapHint').textContent = '用 D 键连续点按 8 秒。抬起不必完全离键，按你的习惯来。';
    $('#tapKey').classList.remove('is-active');
    $('#tapBars').innerHTML = '';
    $('#tapSummary').classList.add('hidden');
  }

  function prepareTap() {
    resetTapVisual();
    const start = $('#tapStart');
    const next = $('#tapNext');
    start.disabled = false;
    start.textContent = '开始 8 秒测试';
    next.classList.add('hidden');

    const data = state.tap;
    if (!data) return;

    const metrics = tapMetrics(data);
    if (!metrics) return;

    start.textContent = '重新测试';
    next.classList.remove('hidden');
    $('#tapTimer').textContent = '0.0s';
    $('#tapSideTimer').textContent = '0.0s';
    $('#tapLiveTps').textContent = metrics.tps.toFixed(1);
    $('#tapLiveCount').textContent = `${metrics.count} 次`;
    $('#tapLiveHint').textContent = '本次完成';
    $('#tapMinInterval').textContent = metrics.minInterval ? `${Math.round(metrics.minInterval)}ms` : '—';
    $('#tapHoldLive').textContent = `${Math.round(metrics.holdMedian)}ms`;
    $('#tapHint').textContent = '上次测试已完成。点击「重新测试」可以再采一次。';
    renderTapBars(metrics.intervals);
    fillTapSummary(metrics);
  }

  function fillTapSummary(metrics) {
    $('#tapSummary').classList.remove('hidden');
    $('#tapSumCount').textContent = String(metrics.count);
    $('#tapSumTps').textContent = metrics.tps.toFixed(1);
    $('#tapSumCv').textContent = String(Math.round(metrics.cv * 100));
    $('#tapSumHold').textContent = String(Math.round(metrics.holdMedian));
    $('#tapSumBounce').textContent = String(metrics.bounce);
  }

  function startTapTest() {
    abortActive();
    const session = createSession();
    activeSession = session;

    const taps = [];
    let open = null;
    let phase = 'countdown';
    let count = 3;
    let startedAt = 0;
    let lastTapCount = 0;

    resetTapVisual();
    $('#tapStart').disabled = true;
    $('#tapStart').textContent = '测试中…';
    $('#tapNext').classList.add('hidden');
    $('#tapLiveHint').textContent = '准备';
    $('#tapHint').textContent = '3';

    const pushOpen = (at) => {
      if (!open) return;
      open.up = at;
      taps.push(open);
      open = null;
      lastTapCount = taps.length;
      updateLive();
    };

    const updateLive = () => {
      const elapsed = Math.max(0.2, (now() - startedAt) / 1000);
      const countNow = taps.length + (open ? 1 : 0);
      const tps = countNow / Math.max(0.5, Math.min(elapsed, TAP_DURATION / 1000));
      $('#tapLiveTps').textContent = tps.toFixed(1);
      $('#tapLiveCount').textContent = `${countNow} 次`;
      if (taps.length >= 2) {
        $('#tapLiveHint').textContent = `最近 ${Math.round(taps[taps.length - 1].up - taps[taps.length - 1].down)}ms`;
      }
      const downs = taps.map((tap) => tap.down);
      const intervals = [];
      for (let i = 1; i < downs.length; i += 1) intervals.push(downs[i] - downs[i - 1]);
      renderTapBars(intervals);
      if (intervals.length) {
        $('#tapMinInterval').textContent = `${Math.round(Math.min(...intervals))}ms`;
      }
      const holds = taps.map((tap) => tap.up - tap.down);
      if (holds.length) {
        $('#tapHoldLive').textContent = `${Math.round(median(holds))}ms`;
      }
    };

    const onKeyDown = (event) => {
      if (phase !== 'running' || event.code !== 'KeyD') return;
      event.preventDefault();
      if (event.repeat) return;
      const at = now() - startedAt;
      if (open) {
        /* 理论上不会发生；如果 keyup 丢失，先收掉上一拍 */
        pushOpen(at);
      }
      open = { down: at, up: null };
      $('#tapKey').classList.add('is-active');
      updateLive();
    };

    const onKeyUp = (event) => {
      if (phase !== 'running' || event.code !== 'KeyD') return;
      event.preventDefault();
      $('#tapKey').classList.remove('is-active');
      if (!open) return;
      open.up = now() - startedAt;
      taps.push(open);
      open = null;
      updateLive();
    };

    session.on(window, 'keydown', onKeyDown);
    session.on(window, 'keyup', onKeyUp);

    const countdown = () => {
      if (session.aborted) return;
      if (phase !== 'countdown') return;
      if (count >= 1) {
        $('#tapHint').textContent = String(count);
        $('#tapLiveHint').textContent = '准备';
        beep(440, 0.06, 0.03);
        count -= 1;
        session.setTimeout(countdown, 800);
      } else {
        run();
      }
    };

    const run = () => {
      phase = 'running';
      startedAt = now();
      $('#tapHint').textContent = '开始！用 D 键连续点按，保持最快且可控。';
      $('#tapLiveHint').textContent = '按下 D 开始';
      session.setInterval(tick, 60);
    };

    const tick = () => {
      if (phase !== 'running') return;
      const elapsed = now() - startedAt;
      const remaining = Math.max(0, TAP_DURATION - elapsed);
      $('#tapTimer').textContent = `${(remaining / 1000).toFixed(1)}s`;
      $('#tapSideTimer').textContent = `${(remaining / 1000).toFixed(1)}s`;
      updateLive();
      if (elapsed >= TAP_DURATION) finish();
    };

    const finish = () => {
      if (phase === 'done') return;
      phase = 'done';
      if (open) {
        open.up = TAP_DURATION;
        taps.push(open);
        open = null;
      }
      session.abort();
      if (activeSession === session) activeSession = null;

      state.tap = { duration: 8, taps };
      state.completed.tap = true;
      saveState();

      const metrics = tapMetrics(state.tap);
      $('#tapKey').classList.remove('is-active');
      $('#tapTimer').textContent = '0.0s';
      $('#tapSideTimer').textContent = '0.0s';
      $('#tapStart').disabled = false;
      $('#tapStart').textContent = '重新测试';
      $('#tapNext').classList.remove('hidden');
      $('#tapHint').textContent = '测试完成。点击「下一项」继续急停测试。';
      if (metrics) {
        $('#tapLiveTps').textContent = metrics.tps.toFixed(1);
        $('#tapLiveCount').textContent = `${metrics.count} 次`;
        $('#tapLiveHint').textContent = '本次完成';
        $('#tapMinInterval').textContent = metrics.minInterval ? `${Math.round(metrics.minInterval)}ms` : '—';
        $('#tapHoldLive').textContent = `${Math.round(metrics.holdMedian)}ms`;
        renderTapBars(metrics.intervals);
        fillTapSummary(metrics);
      }
      beep(660, 0.12, 0.04);
    };

    session.setTimeout(countdown, 300);
  }

  /* ------------------------------------------------------------
     STRAFE TEST
  ------------------------------------------------------------ */

  function strafeMetrics(data) {
    if (!data) return null;
    const transitions = data.transitions || [];
    const valid = transitions.filter((t) => t && t.newDown !== null && t.newDown !== undefined);
    const signed = valid.map((t) => t.signed).filter((v) => v !== null && v !== undefined && !Number.isNaN(v));
    const reactions = valid
      .map((t) => t.newDown - t.cue)
      .filter((v) => v >= 0 && !Number.isNaN(v));
    const clean = valid.filter((t) => {
      if (t.clean !== undefined) return t.clean;
      return !t.early && t.signed >= -35 && t.signed <= 60;
    }).length;
    const early = transitions.filter((t) => t.early).length;
    const overlap = signed.filter((v) => v < -35).length;
    const gap = signed.filter((v) => v > 90).length;
    const signedMedian = median(signed);
    const mad = median(signed.map((v) => Math.abs(v - signedMedian)));
    return {
      transitions,
      valid,
      signed,
      clean,
      cleanRate: transitions.length ? clean / transitions.length : 0,
      overlapRate: valid.length ? overlap / valid.length : 0,
      gapRate: valid.length ? gap / valid.length : 0,
      earlyRate: transitions.length ? early / transitions.length : 0,
      failed: transitions.length - valid.length,
      medianSigned: signedMedian,
      mad,
      reactionMedian: median(reactions),
      reactions
    };
  }

  function buildStrafeDots() {
    const container = $('#strafeDots');
    if (!container) return;
    container.innerHTML = Array.from({ length: 12 }, (_, index) => (
      `<div class="dot" data-index="${index}"></div>`
    )).join('');
  }

  function markDot(index, clean) {
    const dot = $(`#strafeDots .dot[data-index="${index}"]`);
    if (!dot) return;
    dot.classList.remove('now');
    dot.classList.add(clean ? 'done' : 'bad');
  }

  function markCurrentDot(index) {
    const dot = $(`#strafeDots .dot[data-index="${index}"]`);
    if (!dot) return;
    dot.classList.add('now');
  }

  function updateStrafeKeys() {
    $('#keyA')?.classList.toggle('is-active', pressed.has('KeyA'));
    $('#keyD')?.classList.toggle('is-active', pressed.has('KeyD'));

    const left = $('.lane-zone.left');
    const right = $('.lane-zone.right');
    left?.classList.toggle('active', pressed.has('KeyA'));
    right?.classList.toggle('active', pressed.has('KeyD'));

    const marker = $('#laneMarker');
    if (!marker) return;
    const both = pressed.has('KeyA') && pressed.has('KeyD');
    marker.classList.toggle('pos-left', pressed.has('KeyA') && !pressed.has('KeyD'));
    marker.classList.toggle('pos-right', pressed.has('KeyD') && !pressed.has('KeyA'));
    if (both) {
      marker.style.left = '50%';
      marker.style.background = 'var(--red)';
      marker.style.boxShadow = '0 0 0 8px rgba(255,82,104,.09), 0 0 30px rgba(255,82,104,.5)';
    } else {
      marker.style.left = '';
      marker.style.background = '';
      marker.style.boxShadow = '';
    }
  }

  function setLaneCue(side) {
    const lane = $('#strafeLane');
    if (!lane) return;
    lane.classList.remove('cue-left', 'cue-right');
    if (side === 'left') lane.classList.add('cue-left');
    if (side === 'right') lane.classList.add('cue-right');
  }

  function setTargetZone(side) {
    $('.lane-zone.left')?.classList.toggle('target', side === 'left');
    $('.lane-zone.right')?.classList.toggle('target', side === 'right');
  }

  function setCueOrb(text, armed) {
    const orb = $('#strafeCue');
    if (!orb) return;
    const span = $('span', orb);
    if (span) span.textContent = text;
    orb.classList.toggle('armed', Boolean(armed));
  }

  function updateStrafeHud() {
    const transitions = runTransitions;
    const valid = transitions.filter((t) => t.newDown !== null && t.newDown !== undefined);
    const clean = transitions.filter((t) => t.clean).length;
    const signed = valid.map((t) => t.signed).filter((v) => v !== null && v !== undefined);
    const overlap = signed.filter((v) => v > 35).length;
    const gap = signed.filter((v) => v < -90).length;

    $('#strafeCleanHud').textContent = `${clean} / ${transitions.length}`;
    $('#strafeCleanSide').textContent = valid.length ? `${clean} / ${transitions.length}` : '0 / 0';
    $('#strafeOverlapSide').textContent = valid.length ? `${Math.round((overlap / valid.length) * 100)}%` : '—';
    $('#strafeGapSide').textContent = valid.length ? `${Math.round((gap / valid.length) * 100)}%` : '—';

    const last = transitions[transitions.length - 1];
    if (last && last.newDown !== null && last.newDown !== undefined) {
      $('#strafeReactionHud').textContent = `${Math.round(last.newDown - last.cue)} ms`;
      const signedValue = last.signed;
      const el = $('#strafeSignedHud');
      el.textContent = timingLabel(signedValue);
      el.style.color = signedValue < -35 ? 'var(--red)' : signedValue > 90 ? 'var(--amber)' : 'var(--lime)';
    } else if (last) {
      $('#strafeReactionHud').textContent = '未检测到';
      $('#strafeSignedHud').textContent = '—';
      $('#strafeSignedHud').style.color = 'var(--muted)';
    }
  }

  let runTransitions = [];

  function resetStrafeVisual() {
    runTransitions = [];
    $('#strafeTimer').textContent = '00:00';
    $('#strafeCleanHud').textContent = '0 / 0';
    $('#strafeReactionHud').textContent = '-- ms';
    $('#strafeSignedHud').textContent = '--';
    $('#strafeSignedHud').style.color = '';
    $('#strafeCleanSide').textContent = '0 / 0';
    $('#strafeOverlapSide').textContent = '—';
    $('#strafeGapSide').textContent = '—';
    $('#strafeHint').textContent = '点击开始，然后按住 A。';
    $('#keyA').classList.remove('is-active');
    $('#keyD').classList.remove('is-active');
    $('#laneMarker').style.left = '';
    $('#laneMarker').style.background = '';
    $('#laneMarker').style.boxShadow = '';
    setLaneCue(null);
    setTargetZone(null);
    setCueOrb('READY', false);
    $('.lane-zone.left')?.classList.remove('active');
    $('.lane-zone.right')?.classList.remove('active');
    buildStrafeDots();
  }

  function setStrafeOverlay(config) {
    const overlay = $('#strafeOverlay');
    if (!overlay) return;
    overlay.classList.remove('hidden');
    $('#strafeOverlayKicker').textContent = config.kicker || 'COUNTER-STRAFE';
    const title = $('#strafeOverlayTitle');
    title.textContent = config.title || '';
    title.classList.toggle('countdown', Boolean(config.countdown));
    $('#strafeOverlayText').textContent = config.text || '';
    $('#strafeStartBtn').classList.toggle('hidden', !config.showStart);
    $('#strafeAgainBtn').classList.toggle('hidden', !config.showAgain);
    $('#strafeDoneBtn').classList.toggle('hidden', !config.showDone);
    if (config.showDone) $('#strafeDoneBtn').textContent = config.doneText || '下一项：悬停 →';
    if (config.showStart) $('#strafeStartBtn').textContent = config.startText || '开始测试';
  }

  function hideStrafeOverlay() {
    $('#strafeOverlay')?.classList.add('hidden');
  }

  function showStrafeIdle(isRetest) {
    setStrafeOverlay({
      kicker: isRetest ? 'RETEST MODE' : 'COUNTER-STRAFE',
      title: isRetest ? '用新参数再测一次' : '准备好急停了吗？',
      text: isRetest
        ? '先把推荐参数写入键盘。开始后按住 A，提示出现时松开 A、按下 D。'
        : '开始后按住 A。提示出现时，立即松开 A 并按下 D。保持 D，等下一次提示。',
      showStart: true,
      showAgain: false,
      showDone: false,
      startText: isRetest ? '开始回测' : '开始测试'
    });
  }

  function showStrafeSummary(result, isRetest) {
    const metrics = strafeMetrics(result);
    if (!metrics) return;
    lastStrafeSummaryRetest = isRetest;
    setStrafeOverlay({
      kicker: isRetest ? 'RETEST COMPLETE' : 'TEST COMPLETE',
      title: `干净切换 ${metrics.clean} / ${metrics.transitions.length}`,
      text: `中位时序 ${timingLabel(metrics.medianSigned)} · 切换反应 ${Math.round(metrics.reactionMedian)}ms · 重叠 ${Math.round(metrics.overlapRate * 100)}%`,
      showStart: false,
      showAgain: true,
      showDone: true,
      doneText: isRetest ? '查看对比 →' : '下一项：悬停 →'
    });

    $('#strafeCleanHud').textContent = `${metrics.clean} / ${metrics.transitions.length}`;
    $('#strafeCleanSide').textContent = `${metrics.clean} / ${metrics.transitions.length}`;
    $('#strafeOverlapSide').textContent = `${Math.round(metrics.overlapRate * 100)}%`;
    $('#strafeGapSide').textContent = `${Math.round(metrics.gapRate * 100)}%`;
    $('#strafeTimer').textContent = formatClock((result.duration || 0) * 1000);
    $('#strafeReactionHud').textContent = `${Math.round(metrics.reactionMedian)} ms`;
    $('#strafeSignedHud').textContent = timingLabel(metrics.medianSigned);
    $('#strafeSignedHud').style.color = metrics.medianSigned < -35
      ? 'var(--red)'
      : metrics.medianSigned > 90 ? 'var(--amber)' : 'var(--lime)';

    $$('#strafeDots .dot').forEach((dot, index) => {
      dot.classList.remove('done', 'bad', 'now');
      const transition = metrics.transitions[index];
      if (!transition) return;
      dot.classList.add(transition.clean ? 'done' : 'bad');
    });
    setLaneCue(null);
    setTargetZone(null);
    setCueOrb('DONE', false);
  }

  function prepareStrafe() {
    resetStrafeVisual();
    if (retestMode) {
      if (state.retest) showStrafeSummary(state.retest, true);
      else showStrafeIdle(true);
      return;
    }
    if (state.strafe) {
      showStrafeSummary(state.strafe, false);
      return;
    }
    showStrafeIdle(false);
  }

  function startStrafeFlow() {
    abortActive();
    const session = createSession();
    activeSession = session;

    resetStrafeVisual();
    const isRetest = retestMode;
    lastStrafeSummaryRetest = isRetest;

    let phase = 'waitHold';
    let current = 'KeyA';
    let next = 'KeyD';
    let round = null;
    let preReleasedAt = null;
    let prePressedAt = null;
    let startedAt = 0;

    runTransitions = [];
    showStrafeWait();

    function showStrafeWait() {
      setStrafeOverlay({
        kicker: isRetest ? 'RETEST MODE' : 'READY',
        title: '请按住 A 键',
        text: '按住 A 后倒计时开始。提示出现前保持按住，不要提前松手。',
        showStart: false,
        showAgain: false,
        showDone: false
      });
      $('#strafeHint').textContent = '按住 A 开始。';
      updateStrafeKeys();
    }

    function startCountdown() {
      phase = 'countdown';
      let count = 3;
      setStrafeOverlay({
        kicker: isRetest ? 'RETEST MODE' : 'READY',
        title: '3',
        text: '保持按住 A...',
        countdown: true,
        showStart: false,
        showAgain: false,
        showDone: false
      });
      $('#strafeOverlayTitle').classList.add('countdown');

      const step = () => {
        if (session.aborted || phase !== 'countdown') return;
        if (!pressed.has('KeyA')) {
          phase = 'waitHold';
          showStrafeWait();
          return;
        }
        if (count >= 1) {
          $('#strafeOverlayTitle').textContent = String(count);
          beep(440, 0.06, 0.03);
          count -= 1;
          session.setTimeout(step, 760);
        } else {
          hideStrafeOverlay();
          run();
        }
      };
      step();
    }

    function run() {
      phase = 'waitCue';
      startedAt = now();
      $('#strafeTimer').textContent = '00:00';
      session.setInterval(() => {
        if (phase !== 'waitCue' && phase !== 'armed' && phase !== 'between') return;
        $('#strafeTimer').textContent = formatClock(now() - startedAt);
      }, 250);
      beginRound();
    }

    function beginRound() {
      if (runTransitions.length >= 12) {
        finishStrafe();
        return;
      }
      current = runTransitions.length % 2 === 0 ? 'KeyA' : 'KeyD';
      next = current === 'KeyA' ? 'KeyD' : 'KeyA';
      round = {
        index: runTransitions.length,
        from: current,
        to: next,
        cueAt: 0,
        oldUp: null,
        newDown: null,
        early: false,
        preReleased: null,
        prePressed: false,
        resolved: false,
        overlapTimer: null
      };
      phase = 'waitCue';
      preReleasedAt = null;
      prePressedAt = null;

      setCueOrb('HOLD', false);
      setLaneCue(null);
      setTargetZone(null);
      $('#strafeHint').textContent = `保持 ${keyName(current)}… 等提示。`;
      updateStrafeKeys();
      markCurrentDot(round.index);

      const delay = 1100 + Math.random() * 950 + (runTransitions.length === 0 ? 450 : 0);
      session.setTimeout(showCue, delay);
    }

    function showCue() {
      if (phase !== 'waitCue' || !round || round.resolved) return;
      phase = 'armed';
      round.cueAt = now();

      if (preReleasedAt && !pressed.has(current)) {
        round.preReleased = preReleasedAt;
        round.early = true;
      }
      if (prePressedAt) {
        round.newDown = round.cueAt;
        round.prePressed = true;
      }

      beep(880, 0.07, 0.045);
      flashElement($('#strafeFlash'));
      setCueOrb('SWITCH', true);
      setLaneCue(next === 'KeyA' ? 'left' : 'right');
      setTargetZone(next === 'KeyA' ? 'left' : 'right');
      $('#strafeHint').textContent = `松开 ${keyName(current)} → 按下 ${keyName(next)}`;
      markCurrentDot(round.index);

      session.setTimeout(() => {
        if (!round || round.resolved || phase !== 'armed') return;
        if (round.newDown === null) {
          failRound();
        } else {
          attemptResolve();
        }
      }, 1500);

      if (round.newDown !== null) attemptResolve();
    }

    function attemptResolve() {
      if (!round || round.resolved || round.newDown === null) return;

      if (round.oldUp === null) {
        if (round.preReleased !== null) {
          round.oldUp = round.preReleased;
          round.early = true;
        } else if (!pressed.has(round.from)) {
          round.oldUp = round.newDown;
        } else {
          if (!round.overlapTimer) {
            round.overlapTimer = session.setTimeout(() => {
              if (!round || round.resolved) return;
              if (round.oldUp === null) {
                round.oldUp = round.newDown + 500;
                round.cappedOverlap = true;
              }
              finalizeRound();
            }, 520);
          }
          return;
        }
      }
      finalizeRound();
    }

    function finalizeRound() {
      if (!round || round.resolved || round.newDown === null) return;
      round.resolved = true;

      let signed;
      if (round.early && round.oldUp !== null) {
        signed = round.newDown - round.cueAt;
      } else if (round.oldUp === null) {
        signed = 500;
        round.cappedOverlap = true;
      } else {
        signed = round.newDown - round.oldUp;
      }

      const transition = {
        index: round.index,
        from: round.from,
        to: round.to,
        cue: round.cueAt,
        oldUp: round.oldUp,
        newDown: round.newDown,
        signed,
        reaction: round.prePressed ? 0 : round.newDown - round.cueAt,
        early: Boolean(round.early),
        capped: Boolean(round.cappedOverlap),
        prePressed: Boolean(round.prePressed),
        clean: !round.early && !round.cappedOverlap && !round.prePressed && signed >= -35 && signed <= 60
      };

      runTransitions.push(transition);
      markDot(round.index, transition.clean);
      updateStrafeHud();
      phase = 'between';

      setCueOrb('HOLD', false);
      setLaneCue(null);
      setTargetZone(null);
      $('#strafeHint').textContent = `保持 ${keyName(next)}… 等下一次提示。`;

      if (runTransitions.length >= 12) {
        session.setTimeout(finishStrafe, 650);
      } else {
        session.setTimeout(() => {
          if (phase === 'between') beginRound();
        }, 900);
      }
    }

    function failRound() {
      if (!round || round.resolved) return;
      round.resolved = true;
      const transition = {
        index: round.index,
        from: round.from,
        to: round.to,
        cue: round.cueAt,
        oldUp: round.oldUp,
        newDown: null,
        signed: null,
        reaction: null,
        early: Boolean(round.early),
        capped: false,
        prePressed: Boolean(round.prePressed),
        failed: true,
        clean: false
      };
      runTransitions.push(transition);
      markDot(round.index, false);
      updateStrafeHud();
      phase = 'between';
      setCueOrb('HOLD', false);
      setLaneCue(null);
      setTargetZone(null);
      $('#strafeHint').textContent = '没有检测到新方向键。下一轮继续，保持当前键。';

      if (runTransitions.length >= 12) {
        session.setTimeout(finishStrafe, 650);
      } else {
        session.setTimeout(() => {
          if (phase === 'between') beginRound();
        }, 1000);
      }
    }

    function finishStrafe() {
      if (phase === 'done') return;
      phase = 'done';
      session.abort();
      if (activeSession === session) activeSession = null;

      const result = {
        transitions: runTransitions,
        rounds: 12,
        duration: Math.max(1, Math.round((now() - startedAt) / 1000)),
        finishedAt: Date.now()
      };

      if (isRetest) {
        state.retest = result;
      } else {
        state.strafe = result;
        state.retest = null;
      }
      state.completed.strafe = true;
      saveState();

      updateStrafeKeys();
      showStrafeSummary(result, isRetest);
      beep(660, 0.12, 0.04);
    }

    const onKeyDown = (event) => {
      if (event.repeat) return;
      if (event.code === 'Space') event.preventDefault();

      if (phase === 'waitHold' && event.code === 'KeyA') {
        startCountdown();
        return;
      }
      if (phase === 'waitCue' || phase === 'between') {
        if (event.code === 'KeyA' || event.code === 'KeyD') {
          if (event.code === current) preReleasedAt = null;
          if (event.code === next) prePressedAt = now();
        }
      }
      if (phase === 'armed' && round && !round.resolved) {
        if (event.code === next && round.newDown === null) {
          round.newDown = now();
          attemptResolve();
        }
      }
      updateStrafeKeys();
    };

    const onKeyUp = (event) => {
      if (phase === 'countdown' && event.code === 'KeyA') {
        phase = 'waitHold';
        showStrafeWait();
        return;
      }
      if (phase === 'armed' && round && !round.resolved && event.code === current) {
        if (round.oldUp === null) {
          round.oldUp = now();
          if (round.cueAt && round.oldUp < round.cueAt - 30) round.early = true;
          attemptResolve();
        }
      } else if ((phase === 'waitCue' || phase === 'between') && event.code === current) {
        preReleasedAt = now();
      }
      updateStrafeKeys();
    };

    session.on(window, 'keydown', onKeyDown);
    session.on(window, 'keyup', onKeyUp);

    if (pressed.has('KeyA')) {
      session.setTimeout(() => {
        if (phase === 'waitHold') startCountdown();
      }, 80);
    }
  }

  /* ------------------------------------------------------------
     HOLD TEST
  ------------------------------------------------------------ */

  const HOLD_DURATION = 15000;

  function holdMetrics(data) {
    if (!data) return null;
    const duration = data.duration || 15;
    const breaks = (data.breaks || [])
      .filter((item) => item && item.end !== null && item.end !== undefined)
      .sort((a, b) => a.start - b.start);
    let totalGap = 0;
    let longest = 0;
    let cursor = 0;
    breaks.forEach((item) => {
      totalGap += Math.max(0, item.end - item.start);
      longest = Math.max(longest, item.start - cursor);
      cursor = Math.max(cursor, item.end);
    });
    longest = Math.max(longest, duration - cursor);
    return {
      duration,
      breaks,
      breakCount: breaks.length,
      totalGap,
      longest
    };
  }

  function resetHoldVisual() {
    $('#holdTimer').textContent = '15.0s';
    $('#holdBreaks').textContent = '0';
    $('#holdBreakSide').textContent = '0';
    $('#holdGapSide').textContent = '0 ms';
    $('#holdState').textContent = '等待开始';
    $('#holdStable').textContent = '稳定时长 —';
    $('#holdHint').textContent = '这项会暴露手抖导致的断触。用你平时架枪的力度按住 D。';
    $('#holdKey').classList.remove('is-active');
    $('#stabilityStrip').innerHTML = '';
  }

  function setHoldOverlay(config) {
    const overlay = $('#holdOverlay');
    if (!overlay) return;
    overlay.classList.remove('hidden');
    $('#holdOverlayTitle').textContent = config.title || '';
    $('#holdOverlayTitle').classList.toggle('countdown', Boolean(config.countdown));
    $('#holdOverlayText').textContent = config.text || '';
    $('#holdStartBtn').classList.toggle('hidden', !config.showStart);
    $('#holdAgainBtn').classList.toggle('hidden', !config.showAgain);
    $('#holdDoneBtn').classList.toggle('hidden', !config.showDone);
    if (config.showDone) $('#holdDoneBtn').textContent = config.doneText || '查看调校方案 →';
  }

  function prepareHold() {
    resetHoldVisual();
    const data = state.hold;
    if (!data) {
      setHoldOverlay({
        title: '准备好悬停了吗？',
        text: '点击开始，然后按住 D 键 15 秒。中途如果断开，重新按住即可，但会记录一次。',
        showStart: true,
        showAgain: false,
        showDone: false
      });
      return;
    }

    const metrics = holdMetrics(data);
    if (!metrics) return;

    $('#holdTimer').textContent = '0.0s';
    $('#holdBreaks').textContent = String(metrics.breakCount);
    $('#holdBreakSide').textContent = String(metrics.breakCount);
    $('#holdGapSide').textContent = `${Math.round(metrics.totalGap * 1000)} ms`;
    $('#holdState').textContent = metrics.breakCount ? '有断开' : '全程稳定';
    $('#holdStable').textContent = `最长稳定 ${metrics.longest.toFixed(1)}s`;
    $('#holdHint').textContent = metrics.breakCount
      ? `上次断开 ${metrics.breakCount} 次，累计 ${Math.round(metrics.totalGap * 1000)}ms。`
      : '上次 15 秒零断开，悬停很稳。';
    $('#stabilityStrip').innerHTML = buildHoldStripFromBreaks(data);

    setHoldOverlay({
      title: metrics.breakCount ? `断开 ${metrics.breakCount} 次` : '15 秒零断开',
      text: metrics.breakCount
        ? `累计断开 ${Math.round(metrics.totalGap * 1000)}ms，最长稳定 ${metrics.longest.toFixed(1)}s。RT 抬起不要设得太灵敏。`
        : '手指悬停很稳定，可以承受更灵敏的 RT。仍然建议先按推荐值实战。',
      showStart: false,
      showAgain: true,
      showDone: true,
      doneText: '查看调校方案 →'
    });
  }

  function buildHoldStripFromBreaks(data) {
    const segments = 150;
    const states = new Array(segments).fill('on');
    (data.breaks || []).forEach((item) => {
      const start = clamp(Math.floor(item.start * 10), 0, segments - 1);
      const end = clamp(Math.ceil((item.end === null || item.end === undefined ? data.duration : item.end) * 10), 0, segments);
      for (let i = start; i < end; i += 1) states[i] = 'off';
    });
    return states.map((item) => `<div class="seg ${item}"></div>`).join('');
  }

  function startHoldFlow() {
    abortActive();
    const session = createSession();
    activeSession = session;

    let phase = 'waitHold';
    let count = 3;
    let startedAt = 0;
    const breaks = [];
    let currentBreak = null;
    let stripStates = new Array(150).fill(null);
    let lastStripIndex = -1;

    resetHoldVisual();
    setHoldOverlay({
      title: '请按住 D 键',
      text: '按住 D 后倒计时开始。保持你平时架枪的力度，不要刻意轻按。',
      showStart: false,
      showAgain: false,
      showDone: false
    });
    $('#holdHint').textContent = '按住 D 开始。';

    function beginBreak(at) {
      if (currentBreak) return;
      currentBreak = { start: at, end: null };
      breaks.push(currentBreak);
      $('#holdState').textContent = '断开！重新按住';
      flashElement($('#holdFlash'));
      beep(240, 0.09, 0.04);
      updateHoldStats();
    }

    function endBreak(at) {
      if (!currentBreak) return;
      currentBreak.end = at;
      currentBreak = null;
      $('#holdState').textContent = '稳定保持';
      beep(660, 0.05, 0.025);
      updateHoldStats();
    }

    function updateHoldStats() {
      const countValue = breaks.length;
      $('#holdBreaks').textContent = String(countValue);
      $('#holdBreakSide').textContent = String(countValue);
      const total = breaks.reduce((sum, item) => {
        const end = item.end === null ? (now() - startedAt) / 1000 : item.end;
        return sum + Math.max(0, end - item.start);
      }, 0);
      $('#holdGapSide').textContent = `${Math.round(total * 1000)} ms`;
    }

    function startCountdown() {
      phase = 'countdown';
      count = 3;
      setHoldOverlay({
        title: '3',
        text: '保持按住 D...',
        countdown: true,
        showStart: false,
        showAgain: false,
        showDone: false
      });

      const step = () => {
        if (session.aborted || phase !== 'countdown') return;
        if (!pressed.has('KeyD')) {
          phase = 'waitHold';
          setHoldOverlay({
            title: '请按住 D 键',
            text: '按住 D 后倒计时开始。保持你平时架枪的力度。',
            showStart: false,
            showAgain: false,
            showDone: false
          });
          $('#holdHint').textContent = '按住 D 开始。';
          return;
        }
        if (count >= 1) {
          $('#holdOverlayTitle').textContent = String(count);
          beep(440, 0.06, 0.03);
          count -= 1;
          session.setTimeout(step, 760);
        } else {
          $('#holdOverlay').classList.add('hidden');
          run();
        }
      };
      step();
    }

    function run() {
      phase = 'running';
      startedAt = now();
      stripStates = new Array(150).fill(null);
      lastStripIndex = -1;
      $('#holdState').textContent = '稳定保持';
      $('#holdHint').textContent = '别松手。保持平时架枪的力度。';
      $('#stabilityStrip').innerHTML = Array.from({ length: 150 }, () => '<div class="seg"></div>').join('');
      $('#holdKey').classList.add('is-active');
      session.setInterval(tick, 50);
    }

    function tick() {
      if (phase !== 'running') return;
      const elapsed = now() - startedAt;
      const remaining = Math.max(0, HOLD_DURATION - elapsed);
      $('#holdTimer').textContent = `${(remaining / 1000).toFixed(1)}s`;

      const isHeld = pressed.has('KeyD');
      if (!isHeld && !currentBreak) beginBreak(elapsed / 1000);
      if (isHeld && currentBreak) endBreak(elapsed / 1000);

      const index = clamp(Math.floor(elapsed / 100), 0, 149);
      for (let i = Math.max(0, lastStripIndex + 1); i <= index; i += 1) {
        stripStates[i] = isHeld ? 'on' : 'off';
      }
      if (index > lastStripIndex) {
        const seg = $('#stabilityStrip')?.children[index];
        if (seg) seg.className = `seg ${stripStates[index] || 'off'}`;
        lastStripIndex = index;
      }

      const stable = currentBreak ? currentBreak.start : (now() - startedAt) / 1000;
      const longestKnown = breaks.reduce((best, item, idx) => {
        const previousEnd = idx === 0 ? 0 : breaks[idx - 1].end || 0;
        return Math.max(best, item.start - previousEnd);
      }, 0);
      const candidate = Math.max(longestKnown, stable);
      $('#holdStable').textContent = `稳定 ${candidate.toFixed(1)}s`;

      if (elapsed >= HOLD_DURATION) finishHold();
    }

    function finishHold() {
      if (phase === 'done') return;
      phase = 'done';
      const elapsed = HOLD_DURATION / 1000;
      if (currentBreak) {
        currentBreak.end = elapsed;
        currentBreak = null;
      }
      session.abort();
      if (activeSession === session) activeSession = null;

      for (let i = 0; i < 150; i += 1) {
        if (!stripStates[i]) stripStates[i] = pressed.has('KeyD') ? 'on' : 'off';
      }
      $('#stabilityStrip').innerHTML = stripStates.map((item) => `<div class="seg ${item}"></div>`).join('');
      $('#holdKey').classList.remove('is-active');
      $('#holdTimer').textContent = '0.0s';

      state.hold = {
        duration: 15,
        breaks: breaks.map((item) => ({ start: item.start, end: item.end === null ? elapsed : item.end })),
        finishedAt: Date.now()
      };
      state.completed.hold = true;
      saveState();

      const metrics = holdMetrics(state.hold);
      $('#holdBreaks').textContent = String(metrics.breakCount);
      $('#holdBreakSide').textContent = String(metrics.breakCount);
      $('#holdGapSide').textContent = `${Math.round(metrics.totalGap * 1000)} ms`;
      $('#holdState').textContent = metrics.breakCount ? '有断开' : '全程稳定';
      $('#holdStable').textContent = `最长稳定 ${metrics.longest.toFixed(1)}s`;
      $('#holdHint').textContent = metrics.breakCount
        ? `记录到 ${metrics.breakCount} 次断开。`
        : '15 秒零断开，悬停很稳。';

      setHoldOverlay({
        title: metrics.breakCount ? `断开 ${metrics.breakCount} 次` : '15 秒零断开',
        text: metrics.breakCount
          ? `累计断开 ${Math.round(metrics.totalGap * 1000)}ms，最长稳定 ${metrics.longest.toFixed(1)}s。建议把 RT 抬起调到 0.12mm 以上。`
          : '手指悬停很稳定，可以承受更灵敏的 RT。先按推荐值实战，再决定是否再降 0.01–0.02mm。',
        showStart: false,
        showAgain: true,
        showDone: true,
        doneText: '查看调校方案 →'
      });
      beep(660, 0.12, 0.04);
    }

    const onKeyDown = (event) => {
      if (event.repeat) return;
      if (event.code === 'Space') event.preventDefault();
      if (phase === 'waitHold' && event.code === 'KeyD') {
        startCountdown();
        return;
      }
      if (phase === 'running' && event.code === 'KeyD') {
        endBreak((now() - startedAt) / 1000);
      }
      if (event.code === 'KeyD' && phase === 'running') {
        $('#holdKey').classList.add('is-active');
      }
    };

    const onKeyUp = (event) => {
      if (event.code !== 'KeyD') return;
      if (phase === 'countdown') {
        phase = 'waitHold';
        setHoldOverlay({
          title: '请按住 D 键',
          text: '按住 D 后倒计时开始。保持你平时架枪的力度。',
          showStart: false,
          showAgain: false,
          showDone: false
        });
        $('#holdHint').textContent = '按住 D 开始。';
        return;
      }
      if (phase === 'running') {
        beginBreak((now() - startedAt) / 1000);
        $('#holdKey').classList.remove('is-active');
      }
    };

    session.on(window, 'keydown', onKeyDown);
    session.on(window, 'keyup', onKeyUp);

    if (pressed.has('KeyD')) {
      session.setTimeout(() => {
        if (phase === 'waitHold') startCountdown();
      }, 80);
    }
  }

  /* ------------------------------------------------------------
     ANALYSIS
  ------------------------------------------------------------ */

  function restMetrics(data) {
    if (!data || data.skipped) return null;
    const events = data.events || [];
    return {
      count: events.length,
      keys: [...new Set(events.map((event) => event.key))]
    };
  }

  function variant(rec, deltaAct, deltaTop, deltaBottom, deltaRt, continuous) {
    const top = clamp(roundTo(rec.top + deltaTop, 0.05), 0.10, 0.32);
    const actuation = clamp(
      roundTo(rec.actuation + deltaAct, 0.05),
      Math.max(0.8, top + 0.4),
      2.6
    );
    return {
      actuation,
      top,
      bottom: clamp(roundTo(rec.bottom + deltaBottom, 0.05), 0.20, 0.55),
      rtPress: clamp(roundTo(rec.rtPress + deltaRt, 0.01), 0.07, 0.30),
      rtRelease: clamp(roundTo(rec.rtRelease + deltaRt, 0.01), 0.08, 0.30),
      continuous
    };
  }

  function buildInsights(context) {
    const {
      p, tap, strafe, hold, rest, rec, profiles, risk, depth, control, tps, cv, priority
    } = context;
    const insights = [];

    if (rest && rest.count > 0) {
      insights.push({
        tag: '误触',
        tone: 'warn',
        title: `静置 ${state.rest?.duration || 20}s 出现 ${rest.count} 次误触`,
        text: `说明当前触发行程或顶部死区偏低。建议顶部死区至少 ${rec.top.toFixed(2)}mm，触发行程不要低于 ${Math.max(rec.actuation, rec.top + 0.5).toFixed(2)}mm。`
      });
    } else if (rest) {
      insights.push({
        tag: '误触',
        tone: 'good',
        title: '静置 20 秒没有误触',
        text: `手指自然搭在键帽上的压力不会触发按键。顶部死区可以从 ${rec.top.toFixed(2)}mm 起步，不必为了防误触牺牲太多行程。`
      });
    } else if (p.misTouch >= 4) {
      insights.push({
        tag: '误触',
        tone: 'warn',
        title: '自评误触频率偏高',
        text: `静置测试被跳过，但你的主观感受更重要。建议先采用稳健档，把顶部死区加到 ${profiles.safe.top.toFixed(2)}mm，适应后再降。`
      });
    }

    if (strafe) {
      if (strafe.overlapRate >= 0.25) {
        insights.push({
          tag: '急停时序',
          tone: 'warn',
          title: `${Math.round(strafe.overlapRate * 100)}% 的切换出现双键重叠`,
          text: `中位时序 ${timingLabel(strafe.medianSigned)}，你倾向先按新方向、后松旧方向。先把 RT 抬起设为 ${rec.rtRelease.toFixed(2)}mm，并在死斗里练「先松后按」；否则会出现斜向输入。`
        });
      } else if (strafe.gapRate >= 0.25) {
        insights.push({
          tag: '急停时序',
          tone: 'warn',
          title: `${Math.round(strafe.gapRate * 100)}% 的切换有超过 90ms 空档`,
          text: `中位时序 ${timingLabel(strafe.medianSigned)}，新方向按得偏晚。可以把触发行程降到 ${rec.actuation.toFixed(2)}mm，RT 按下设为 ${rec.rtPress.toFixed(2)}mm，让新键更快触发。`
        });
      } else {
        insights.push({
          tag: '急停时序',
          tone: 'good',
          title: '切换时序比较干净',
          text: `干净切换 ${Math.round(strafe.cleanRate * 100)}%，中位时序 ${timingLabel(strafe.medianSigned)}。保持这个节奏，RT 按推荐值走即可。`
        });
      }

      if (strafe.earlyRate >= 0.2) {
        insights.push({
          tag: '提前松键',
          tone: 'warn',
          title: `${Math.round(strafe.earlyRate * 100)}% 的切换在提示前就松开了旧键`,
          text: '急停会提前失去速度。先练「看到提示再松键」，这会比继续降低 RT 更有效。'
        });
      }

      if (strafe.reactionMedian > 260) {
        insights.push({
          tag: '切换反应',
          tone: 'warn',
          title: `切换反应中位 ${Math.round(strafe.reactionMedian)}ms`,
          text: '瓶颈更像在松键决策，而不是轴体触发。先用死斗练固定节奏：看到人 → 松旧键 → 按新键，不要同时操作。'
        });
      } else if (strafe.reactionMedian > 0 && strafe.reactionMedian < 170) {
        insights.push({
          tag: '切换反应',
          tone: 'good',
          title: `切换反应中位 ${Math.round(strafe.reactionMedian)}ms`,
          text: '你的决策和手速都很快，可以承受更灵敏的 RT。但仍建议先按推荐值打 2–3 局，避免只在测试里快。'
        });
      }
    }

    if (tap) {
      if (tps >= 7) {
        insights.push({
          tag: '释放控制',
          tone: 'good',
          title: `连点 ${tps.toFixed(1)} 次/秒，节奏波动 ${Math.round(cv * 100)}%`,
          text: `释放控制足够支撑更灵敏的 RT。可以从 ${rec.rtRelease.toFixed(2)}mm 起步；如果出现断触，再每次加 0.01mm。`
        });
      } else if (tps < 4) {
        insights.push({
          tag: '释放控制',
          tone: 'warn',
          title: `连点 ${tps.toFixed(1)} 次/秒，释放节奏偏保守`,
          text: `RT 不要设得太灵敏。建议抬起不低于 ${Math.max(rec.rtRelease, 0.13).toFixed(2)}mm，否则手还没完全复位就再次按下，容易出现断触。`
        });
      }
      if (cv > 0.38) {
        insights.push({
          tag: '节奏稳定',
          tone: 'warn',
          title: `连点节奏波动 ${Math.round(cv * 100)}%`,
          text: '你的释放间隔不太稳定。先不要追求极限 RT，保持在 0.12–0.16mm，用稳定的节奏换更可预测的急停。'
        });
      }
    }

    if (hold && hold.breakCount > 0) {
      insights.push({
        tag: '悬停稳定',
        tone: 'warn',
        title: `15 秒悬停断开 ${hold.breakCount} 次`,
        text: `累计断开 ${Math.round(hold.totalGap * 1000)}ms。RT 抬起建议不低于 ${Math.max(rec.rtRelease, 0.12).toFixed(2)}mm，顶部死区可以再加 0.05mm；否则架枪时手指张力变化会被读成断触。`
      });
    } else if (hold) {
      insights.push({
        tag: '悬停稳定',
        tone: 'good',
        title: '15 秒悬停零断开',
        text: '手指持续压键很稳定。RT 可以更灵敏，但架枪和压枪是两回事，仍要在实战里验证。'
      });
    }

    if (p.pressStyle === 'light') {
      insights.push({
        tag: '按键风格',
        tone: 'info',
        title: '轻点悬停型',
        text: `触发行程高于 ${(rec.actuation + 0.2).toFixed(2)}mm 会让你觉得「按不到」。先把顶部死区控制住，再谈高触发；不要为了防误触直接把触发行程拉到 2.0mm 以上。`
      });
    } else if (p.pressStyle === 'heavy') {
      insights.push({
        tag: '按键风格',
        tone: 'info',
        title: '习惯到底型',
        text: `你每次都会按到底，触发行程对速度影响较小。可以用 ${rec.actuation.toFixed(2)}mm 左右换稳定；重点放在 RT 抬起，避免到底后复位不干净。`
      });
    }

    if (p.currentActuation - rec.actuation >= 0.2) {
      insights.push({
        tag: '当前对比',
        tone: 'info',
        title: `当前 ${p.currentActuation.toFixed(2)}mm，偏深`,
        text: `推荐 ${rec.actuation.toFixed(2)}mm。先降 0.1–0.2mm 适应，不要一次跳到最低；如果急停变快但误触增加，再把顶部死区加 0.05mm。`
      });
    } else if (rec.actuation - p.currentActuation >= 0.2) {
      insights.push({
        tag: '当前对比',
        tone: 'info',
        title: `当前 ${p.currentActuation.toFixed(2)}mm，偏浅`,
        text: `推荐 ${rec.actuation.toFixed(2)}mm。先检查静置误触；如果手指搭在键上就触发，优先加顶部死区，而不是继续降触发。`
      });
    }

    if (!p.rtOn) {
      insights.push({
        tag: 'RT',
        tone: 'info',
        title: '你还没有开启 Rapid Trigger',
        text: `RT 对急停是质变，但不要直接拉到最低。先开 ${rec.rtPress.toFixed(2)}mm / ${rec.rtRelease.toFixed(2)}mm，打 3 局再决定是否降 0.01–0.02mm。`
      });
    }

    const fallback = [
      {
        tag: '应用',
        tone: 'info',
        title: '只改游戏键',
        text: '优先设置 WASD、空格、Shift、Ctrl；其他键保持保守，避免打字和日常使用误触。'
      },
      {
        tag: '微调',
        tone: 'info',
        title: '一次只改一个参数',
        text: '每次只改 0.05–0.1mm，打 2–3 局再判断。同时改三个参数，你很难知道是哪一个起了作用。'
      },
      {
        tag: '校准',
        tone: 'info',
        title: '先做满程校准',
        text: '松开所有键，再逐个按到底完成驱动校准。校准不准，顶部和底部死区就没有参考意义。'
      }
    ];
    for (const item of fallback) {
      if (insights.length >= 6) break;
      insights.push(item);
    }

    return insights.slice(0, 6);
  }

  function buildAnalysis() {
    const p = { ...state.profile };
    const tap = tapMetrics(state.tap);
    const strafe = strafeMetrics(state.strafe);
    const hold = holdMetrics(state.hold);
    const rest = restMetrics(state.rest);

    const tps = tap ? tap.tps : 4.5;
    const cv = tap ? tap.cv : 0.25;
    const bounce = tap ? tap.bounce : 0;

    const selfRisk = norm(Number(p.misTouch), 1, 5);
    const restRisk = rest ? clamp(rest.count / 4, 0, 1) : selfRisk;
    const styleRisk = { light: 0.18, balanced: 0.45, heavy: 0.72 }[p.pressStyle] || 0.45;
    let risk = 0.42 * selfRisk + 0.28 * restRisk + 0.12 * (p.fingersRest ? 1 : 0) + 0.18 * styleRisk;
    if (Number(p.currentActuation) < 1.2) risk += 0.06;
    if (hold) risk += Math.min(hold.breakCount * 0.04, 0.12);
    risk = clamp(risk, 0, 1);

    const speed = norm(tps, 2.5, 9);
    const steadiness = clamp(1 - cv / 0.65, 0, 1);
    const holdStability = hold ? clamp(1 - hold.breakCount / 4, 0, 1) : 0.6;
    const control = clamp(
      0.42 * speed +
      0.28 * steadiness +
      0.18 * holdStability +
      0.12 * (1 - clamp(bounce / 5, 0, 1)),
      0, 1
    );

    const cleanRate = strafe ? strafe.cleanRate : 0.5;
    const reactionMedian = strafe ? strafe.reactionMedian : 220;
    const mad = strafe ? strafe.mad : 50;
    const reactScore = strafe && strafe.reactions.length ? 1 - norm(reactionMedian, 100, 320) : 0;
    const consistency = strafe && strafe.signed.length ? 1 - norm(mad, 20, 130) : 0;
    const strafeScore = clamp(0.5 * cleanRate + 0.3 * reactScore + 0.2 * consistency, 0, 1);

    const depthStyle = { light: 0.2, balanced: 0.5, heavy: 0.8 }[p.pressStyle] || 0.5;
    const holdDepth = tap ? norm(tap.holdMedian, 45, 140) : 0.5;
    const depth = clamp(0.65 * depthStyle + 0.35 * holdDepth, 0, 1);

    const styleSpeed = { entry: 0.9, rifler: 0.7, awper: 0.5, igl: 0.45, casual: 0.3 }[p.playstyle] || 0.55;
    let speedNeed = styleSpeed;
    if (p.goals.includes('speed')) speedNeed += 0.12;
    if (p.goals.includes('safe')) speedNeed -= 0.12;
    speedNeed = clamp(speedNeed, 0.1, 1);

    let actuation = 1.55;
    actuation += (depth - 0.5) * 0.55;
    actuation += (risk - 0.4) * 0.55;
    actuation -= (speedNeed - 0.5) * 0.30;
    actuation -= clamp((tps - 6.5) / 5, 0, 1) * 0.12;
    actuation = clamp(actuation, 0.8, 2.4);

    let top = 0.10 + risk * 0.15 + (p.fingersRest ? 0.02 : 0);
    top = clamp(roundTo(top, 0.05), 0.10, 0.30);

    let bottom = 0.24 + depth * 0.14 + (hold && hold.breakCount > 1 ? 0.05 : 0);
    bottom += Number(p.totalTravel) >= 3.8 ? 0.04 : 0;
    bottom = clamp(roundTo(bottom, 0.05), 0.20, 0.50);

    const rtBase = 0.17 - control * 0.08 + risk * 0.04 + (strafe && strafe.overlapRate > 0.3 ? -0.015 : 0);
    const rtPress = clamp(roundTo(rtBase - 0.01, 0.01), 0.07, 0.25);
    const rtRelease = clamp(roundTo(rtBase + 0.01, 0.01), 0.08, 0.26);

    actuation = clamp(roundTo(Math.max(actuation, top + 0.45), 0.05), 0.8, 2.5);

    const continuous = control > 0.55 && risk < 0.68 && !(hold && hold.breakCount > 2) ? '开启' : '关闭';
    const rec = { actuation, top, bottom, rtPress, rtRelease, continuous };
    const safe = variant(rec, 0.15, 0.05, 0.05, 0.04, '关闭');
    const aggressive = variant(rec, -0.15, -0.05, -0.05, -0.03, '开启');
    const profiles = { safe, rec, aggressive };

    const radar = {
      reaction: 100 * clamp(0.55 * reactScore + 0.45 * speed, 0, 1),
      stable: 100 * clamp(0.5 * steadiness + 0.3 * consistency + 0.2 * (1 - clamp(bounce / 5, 0, 1)), 0, 1),
      strafe: 100 * strafeScore,
      control: 100 * control,
      safe: 100 * (1 - risk)
    };

    let name = '均衡进化型';
    if (risk >= 0.62) name = '稳健防误触型';
    else if (strafe && strafe.cleanRate >= 0.72 && tps >= 6) name = '高速急停型';
    else if (depth >= 0.66) name = '重压稳枪型';
    else if (control >= 0.68) name = '微操控制型';

    let priority = '整体跟手度';
    if (risk >= 0.6) priority = '静置误触';
    else if (strafe && strafe.overlapRate >= 0.25) priority = '松键时序';
    else if (strafe && strafe.gapRate >= 0.25) priority = '新键触发速度';
    else if (hold && hold.breakCount > 0) priority = '悬停断触';
    else if (tap && tap.tps < 4) priority = '释放控制';

    const sub = `你的急停${strafe ? `干净率 ${Math.round(strafe.cleanRate * 100)}%` : '数据不足'}，连点 ${tps.toFixed(1)} 次/秒。推荐从 ${rec.actuation.toFixed(2)}mm 触发、${rec.rtRelease.toFixed(2)}mm RT 抬起开始，优先解决${priority}。`;

    let confidence = '高';
    if (!state.tap || !state.strafe) confidence = '低';
    else if (!state.rest || state.rest.skipped || !state.hold) confidence = '中';
    if ((tap && tap.count < 8) || (strafe && strafe.valid.length < 6)) confidence = '低';

    const insights = buildInsights({
      p, tap, strafe, hold, rest, rec, profiles, risk, depth, control, tps, cv, priority
    });

    return {
      p,
      tap,
      strafe,
      hold,
      rest,
      risk,
      control,
      strafeScore,
      depth,
      tps,
      cv,
      bounce,
      rec,
      profiles,
      radar,
      name,
      sub,
      confidence,
      insights,
      priority
    };
  }

  /* ------------------------------------------------------------
     RESULT RENDERING
  ------------------------------------------------------------ */

  function renderRadar(radar) {
    const axes = [
      ['反应', radar.reaction],
      ['稳定', radar.stable],
      ['急停', radar.strafe],
      ['微操', radar.control],
      ['防误触', radar.safe]
    ];
    const cx = 150;
    const cy = 150;
    const radius = 96;
    const angle = (index) => -Math.PI / 2 + (index * 2 * Math.PI) / axes.length;
    const point = (index, value) => {
      const a = angle(index);
      return [
        cx + Math.cos(a) * radius * (value / 100),
        cy + Math.sin(a) * radius * (value / 100)
      ];
    };

    let grid = '';
    [20, 40, 60, 80, 100].forEach((level) => {
      const points = axes.map((_, index) => point(index, level).map((n) => n.toFixed(1)).join(',')).join(' ');
      grid += `<polygon class="radar-grid" points="${points}"/>`;
    });

    const axisLines = axes.map((_, index) => {
      const [x, y] = point(index, 100);
      return `<line class="radar-axis" x1="${cx}" y1="${cy}" x2="${x.toFixed(1)}" y2="${y.toFixed(1)}"/>`;
    }).join('');

    const dataPoints = axes.map(([, value], index) => point(index, clamp(value, 0, 100)).map((n) => n.toFixed(1)).join(',')).join(' ');
    const dots = axes.map(([, value], index) => {
      const [x, y] = point(index, clamp(value, 0, 100));
      return `<circle class="radar-dot" cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="2.6"/>`;
    }).join('');

    const labels = axes.map(([label], index) => {
      const a = angle(index);
      const x = cx + Math.cos(a) * (radius + 25);
      const y = cy + Math.sin(a) * (radius + 25) + 3;
      let anchor = 'middle';
      if (Math.cos(a) > 0.3) anchor = 'start';
      else if (Math.cos(a) < -0.3) anchor = 'end';
      return `<text class="radar-label" x="${x.toFixed(1)}" y="${y.toFixed(1)}" text-anchor="${anchor}">${label}</text>`;
    }).join('');

    return `<svg viewBox="0 0 300 300" role="img" aria-label="习惯雷达图">${grid}${axisLines}<polygon class="radar-poly" points="${dataPoints}"/>${dots}${labels}</svg>`;
  }

  function renderParams(analysis) {
    const { p, rec } = analysis;
    const travelPercent = Math.round((rec.actuation / Number(p.totalTravel)) * 100);
    const currentActuation = Number(p.currentActuation);
    const actuationDelta = currentActuation - rec.actuation;
    const currentActuationText = Math.abs(actuationDelta) < 0.03
      ? '与当前接近'
      : `当前 ${currentActuation.toFixed(2)}mm · ${actuationDelta > 0 ? '深' : '浅'} ${Math.abs(actuationDelta).toFixed(2)}mm`;

    const rtCurrent = p.rtOn ? `当前 ${Number(p.currentRt).toFixed(2)}mm` : '当前 RT 未开启';
    const deadRange = (value) => `建议范围 ${Math.max(0.05, value - 0.05).toFixed(2)}–${(value + 0.05).toFixed(2)}mm`;

    const cards = [
      {
        label: '触发行程',
        value: rec.actuation.toFixed(2),
        unit: 'mm',
        sub: `总键程的 ${travelPercent}% · 推荐核心值`,
        current: currentActuationText,
        primary: true
      },
      {
        label: '顶部死区',
        value: rec.top.toFixed(2),
        unit: 'mm',
        sub: '挡住静置抖动与误触',
        current: deadRange(rec.top)
      },
      {
        label: '底部死区',
        value: rec.bottom.toFixed(2),
        unit: 'mm',
        sub: '吸收按到底的过冲',
        current: deadRange(rec.bottom)
      },
      {
        label: 'RT 按下',
        value: rec.rtPress.toFixed(2),
        unit: 'mm',
        sub: '重新触发距离 · 越小越快',
        current: rtCurrent
      },
      {
        label: 'RT 抬起',
        value: rec.rtRelease.toFixed(2),
        unit: 'mm',
        sub: '复位距离 · 越小越灵敏',
        current: rtCurrent
      },
      {
        label: '连续 RT',
        value: rec.continuous,
        text: true,
        sub: rec.continuous === '开启' ? '全程持续更新复位点' : '仅在触发后复位，更保守',
        current: p.rtOn ? '当前 RT 已开启' : '当前 RT 未开启'
      }
    ];

    $('#paramGrid').innerHTML = cards.map((card) => `
      <div class="param-card ${card.primary ? 'primary' : ''}">
        <span class="param-label">${card.label}</span>
        <div class="param-value ${card.text ? 'text' : ''}">${card.value}${card.unit ? `<small>${card.unit}</small>` : ''}</div>
        <div class="param-sub">${card.sub}</div>
        <div class="param-current">${card.current}</div>
      </div>
    `).join('');
  }

  function renderTravelMap(analysis) {
    const { p, rec } = analysis;
    const total = Math.max(2.5, Number(p.totalTravel));
    const topPct = clamp((rec.top / total) * 100, 0, 100);
    const actPct = clamp((rec.actuation / total) * 100, topPct, 100);
    const bottomPct = clamp((rec.bottom / total) * 100, 0, 100 - actPct);
    const prePct = Math.max(0, actPct - topPct);
    const postPct = Math.max(0, 100 - actPct - bottomPct);
    const container = $('#travelMap');
    if (!container) return;
    container.innerHTML = `
      <div class="travel-map-head"><span>有效行程结构</span><b>总键程 ${total.toFixed(2)} mm</b></div>
      <div class="travel-track" aria-label="行程结构示意图">
        <div class="tm-seg top" style="width:${topPct.toFixed(2)}%"></div>
        <div class="tm-seg pre" style="width:${prePct.toFixed(2)}%"></div>
        <div class="tm-seg post" style="width:${postPct.toFixed(2)}%"></div>
        <div class="tm-seg bottom" style="width:${bottomPct.toFixed(2)}%"></div>
      </div>
      <div class="travel-legend">
        <span><i class="top"></i>顶部死区 ${rec.top.toFixed(2)}</span>
        <span><i class="pre"></i>触发点 ${rec.actuation.toFixed(2)}</span>
        <span><i class="post"></i>触发后余量 ${Math.max(0, total - rec.actuation - rec.bottom).toFixed(2)}</span>
        <span><i class="bottom"></i>底部死区 ${rec.bottom.toFixed(2)}</span>
      </div>
    `;
  }

  function renderMetrics(analysis) {
    const { strafe, tap, risk, cv } = analysis;
    const cleanRate = strafe ? strafe.cleanRate : 0;
    const reaction = strafe ? strafe.reactionMedian : 0;
    const tps = tap ? tap.tps : 0;
    const releaseStability = clamp(1 - cv, 0, 1);
    const items = [
      {
        label: '急停干净度',
        value: `${Math.round(cleanRate * 100)}%`,
        percent: cleanRate * 100
      },
      {
        label: '切换反应',
        value: strafe ? `${Math.round(reaction)}ms` : '—',
        percent: strafe ? 100 - norm(reaction, 80, 350) * 100 : 0
      },
      {
        label: '连点频率',
        value: tap ? `${tps.toFixed(1)} 次/秒` : '—',
        percent: tap ? norm(tps, 2, 10) * 100 : 0
      },
      {
        label: '释放稳定',
        value: `${Math.round(releaseStability * 100)}%`,
        percent: releaseStability * 100
      },
      {
        label: '误触风险',
        value: `${Math.round(risk * 100)} / 100`,
        percent: risk * 100,
        risk: true
      }
    ];

    $('#metricList').innerHTML = items.map((item) => `
      <div class="metric-row">
        <div class="metric-top"><span>${item.label}</span><b>${item.value}</b></div>
        <div class="metric-bar"><div class="metric-fill ${item.risk ? 'risk' : ''}" style="width:${clamp(item.percent, 0, 100).toFixed(1)}%"></div></div>
      </div>
    `).join('');
  }

  function renderProfileTable(analysis) {
    const { profiles } = analysis;
    const rows = [
      ['稳健', profiles.safe],
      ['推荐', profiles.rec],
      ['激进', profiles.aggressive]
    ];
    $('#profileTable').innerHTML = `
      <thead>
        <tr>
          <th>档位</th><th>触发行程</th><th>顶部死区</th><th>底部死区</th><th>RT 按下</th><th>RT 抬起</th><th>连续 RT</th>
        </tr>
      </thead>
      <tbody>
        ${rows.map(([label, item], index) => `
          <tr class="${index === 1 ? 'recommended' : ''}">
            <td>${label}</td>
            <td>${item.actuation.toFixed(2)} mm</td>
            <td>${item.top.toFixed(2)} mm</td>
            <td>${item.bottom.toFixed(2)} mm</td>
            <td>${item.rtPress.toFixed(2)} mm</td>
            <td>${item.rtRelease.toFixed(2)} mm</td>
            <td>${item.continuous}</td>
          </tr>
        `).join('')}
      </tbody>
    `;
  }

  function renderInsights(insights) {
    $('#insights').innerHTML = insights.map((item) => `
      <article class="insight-card ${item.tone || ''}">
        <span class="insight-tag">${item.tag}</span>
        <h4>${item.title}</h4>
        <p>${item.text}</p>
      </article>
    `).join('');
  }

  function renderCompare() {
    const panel = $('#comparePanel');
    if (!state.retest || !state.strafe) {
      panel?.classList.add('hidden');
      return;
    }
    const before = strafeMetrics(state.strafe);
    const after = strafeMetrics(state.retest);
    if (!before || !after) {
      panel?.classList.add('hidden');
      return;
    }

    panel.classList.remove('hidden');
    const time = state.retest.finishedAt
      ? new Date(state.retest.finishedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
      : '';
    $('#compareNote').textContent = `${time ? `${time} · ` : ''}回测 ${after.valid.length} 次有效切换`;

    const items = [
      {
        label: '干净切换率',
        before: `${Math.round(before.cleanRate * 100)}%`,
        after: `${Math.round(after.cleanRate * 100)}%`,
        delta: (after.cleanRate - before.cleanRate) * 100,
        unit: 'pp',
        goodUp: true
      },
      {
        label: '切换反应',
        before: `${Math.round(before.reactionMedian)}ms`,
        after: `${Math.round(after.reactionMedian)}ms`,
        delta: after.reactionMedian - before.reactionMedian,
        unit: 'ms',
        goodUp: false
      },
      {
        label: '时序偏差 |中位|',
        before: `${Math.round(Math.abs(before.medianSigned))}ms`,
        after: `${Math.round(Math.abs(after.medianSigned))}ms`,
        delta: Math.abs(after.medianSigned) - Math.abs(before.medianSigned),
        unit: 'ms',
        goodUp: false
      }
    ];

    $('#compareGrid').innerHTML = items.map((item) => {
      const rounded = Math.round(item.delta);
      const good = item.goodUp ? rounded > 0 : rounded < 0;
      const cls = rounded === 0 ? '' : good ? 'good' : 'bad';
      return `
        <div class="compare-item">
          <div class="compare-label">${item.label}</div>
          <div class="compare-values">
            <span class="compare-before">${item.before}</span>
            <span class="compare-arrow">→</span>
            <span class="compare-after">${item.after}</span>
          </div>
          <div class="compare-delta ${cls}">${rounded > 0 ? '+' : ''}${rounded} ${item.unit}</div>
        </div>
      `;
    }).join('');
  }

  function renderResult() {
    const analysis = buildAnalysis();
    lastAnalysis = analysis;
    $('#demoBanner')?.classList.toggle('hidden', !demoMode);

    $('#resultName').textContent = !state.tap || !state.strafe ? '还差关键测试' : analysis.name;
    $('#resultSub').textContent = !state.tap || !state.strafe
      ? '完成连点测试和急停测试后，才能生成可靠的调校方案。'
      : analysis.sub;
    $('#resultConfidence').textContent = analysis.confidence;
    $('#resultDate').textContent = new Date().toLocaleDateString('zh-CN');

    renderParams(analysis);
    renderTravelMap(analysis);
    $('#radarChart').innerHTML = renderRadar(analysis.radar);
    renderMetrics(analysis);
    renderProfileTable(analysis);
    renderInsights(analysis.insights);
    renderCompare();

    const note = $('#paramNote');
    if (note) {
      note.textContent = `RT 总开关：开启。推荐值按 0.05mm 取整；如果你的驱动只支持 0.1mm，按最接近的值取整。当前触发行程 ${Number(analysis.p.currentActuation).toFixed(2)}mm。`;
    }
  }

  /* ------------------------------------------------------------
     Summary / Export
  ------------------------------------------------------------ */

  function buildSummaryText(analysis) {
    const { p, rec, profiles, strafe, tap, hold, risk, name, confidence } = analysis;
    const lines = [
      'MAGLAB · 磁轴调校方案',
      `日期：${new Date().toLocaleString('zh-CN')}`,
      `画像：${name}（置信度：${confidence}）`,
      '',
      '【推荐参数】',
      `触发行程：${rec.actuation.toFixed(2)} mm`,
      `顶部死区：${rec.top.toFixed(2)} mm`,
      `底部死区：${rec.bottom.toFixed(2)} mm`,
      `RT：开启 · 按下 ${rec.rtPress.toFixed(2)} mm / 抬起 ${rec.rtRelease.toFixed(2)} mm`,
      `连续 RT：${rec.continuous}`,
      '',
      '【三档方案】',
      `稳健：触发 ${profiles.safe.actuation.toFixed(2)} / 顶 ${profiles.safe.top.toFixed(2)} / 底 ${profiles.safe.bottom.toFixed(2)} / RT ${profiles.safe.rtPress.toFixed(2)}-${profiles.safe.rtRelease.toFixed(2)} / 连续 ${profiles.safe.continuous}`,
      `推荐：触发 ${profiles.rec.actuation.toFixed(2)} / 顶 ${profiles.rec.top.toFixed(2)} / 底 ${profiles.rec.bottom.toFixed(2)} / RT ${profiles.rec.rtPress.toFixed(2)}-${profiles.rec.rtRelease.toFixed(2)} / 连续 ${profiles.rec.continuous}`,
      `激进：触发 ${profiles.aggressive.actuation.toFixed(2)} / 顶 ${profiles.aggressive.top.toFixed(2)} / 底 ${profiles.aggressive.bottom.toFixed(2)} / RT ${profiles.aggressive.rtPress.toFixed(2)}-${profiles.aggressive.rtRelease.toFixed(2)} / 连续 ${profiles.aggressive.continuous}`,
      '',
      '【实测摘要】',
      `当前触发行程：${Number(p.currentActuation).toFixed(2)} mm`,
      `急停：干净切换 ${strafe ? Math.round(strafe.cleanRate * 100) : 0}% · 中位时序 ${timingLabel(strafe?.medianSigned)} · 反应 ${strafe ? Math.round(strafe.reactionMedian) : 0}ms`,
      `连点：${tap ? tap.tps.toFixed(1) : '0.0'} 次/秒 · 节奏波动 ${tap ? Math.round(tap.cv * 100) : 0}%`,
      `悬停：${hold ? hold.breakCount : 0} 次断开 · 累计 ${hold ? Math.round(hold.totalGap * 1000) : 0}ms`,
      `误触风险：${Math.round(risk * 100)} / 100`,
      '',
      '【使用提示】',
      '1. 先做键盘满程校准，再写入参数。',
      '2. 优先设置 WASD、空格、Shift、Ctrl，其他键保持保守。',
      '3. 每次只改 0.05–0.1mm，打 2–3 局再判断。',
      '4. 参数是起点，不是终点；以实战手感为准。'
    ];
    return lines.join('\n');
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (error) {
      try {
        const textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(textarea);
        return ok;
      } catch (fallbackError) {
        return false;
      }
    }
  }

  function downloadText(filename, text) {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1500);
  }

  /* ------------------------------------------------------------
     Global bindings
  ------------------------------------------------------------ */

  function updateSoundButton() {
    const button = $('#soundToggle');
    if (button) button.textContent = state.sound ? '声音 开' : '声音 关';
  }

  function bindNavigation() {
    document.addEventListener('click', (event) => {
      const target = event.target.closest('[data-go]');
      if (!target) return;
      event.preventDefault();
      const destination = target.dataset.go;
      if (destination === 'result' && (!state.tap || !state.strafe)) {
        showToast('先完成连点和急停测试，才能生成方案');
        return;
      }
      if (destination === 'strafe') retestMode = false;
      go(destination);
    });

    $$('.step').forEach((step) => {
      step.addEventListener('click', () => {
        const destination = step.dataset.step;
        if (destination === 'result' && (!state.tap || !state.strafe)) {
          showToast('先完成连点和急停测试，才能生成方案');
          return;
        }
        if (destination === 'strafe') retestMode = false;
        go(destination);
      });
    });

    $('#soundToggle')?.addEventListener('click', () => {
      state.sound = !state.sound;
      updateSoundButton();
      saveState();
      if (state.sound) beep(720, 0.08, 0.03);
      showToast(state.sound ? '提示音已开启' : '提示音已关闭');
    });

    $('#resetBtn')?.addEventListener('click', () => {
      if (!window.confirm('确定清空所有测试数据？此操作不可撤销。')) return;
      try {
        localStorage.removeItem(STATE_KEY);
      } catch (error) {
        /* ignore */
      }
      state = freshState();
      demoMode = false;
      demoBackup = null;
      retestMode = false;
      lastAnalysis = null;
      updateSoundButton();
      go('home');
      showToast('已清空所有数据');
    });

    $('#demoBtn')?.addEventListener('click', loadDemo);
    $('#exitDemo')?.addEventListener('click', exitDemo);
    $('#resumeBtn')?.addEventListener('click', () => {
      if (!state.tap || !state.strafe) return;
      go('result');
    });
  }

  function bindTestButtons() {
    $('#restStart')?.addEventListener('click', startRestTest);
    $('#restSkip')?.addEventListener('click', skipRest);

    $('#tapStart')?.addEventListener('click', startTapTest);

    $('#strafeStartBtn')?.addEventListener('click', startStrafeFlow);
    $('#strafeAgainBtn')?.addEventListener('click', startStrafeFlow);
    $('#strafeDoneBtn')?.addEventListener('click', () => {
      if (lastStrafeSummaryRetest) {
        retestMode = false;
        go('result');
      } else {
        go('hold');
      }
    });

    $('#holdStartBtn')?.addEventListener('click', startHoldFlow);
    $('#holdAgainBtn')?.addEventListener('click', startHoldFlow);
    $('#holdDoneBtn')?.addEventListener('click', () => go('result'));
  }

  function bindResultButtons() {
    $('#resultHome')?.addEventListener('click', () => {
      if (demoMode) exitDemo();
      else go('home');
    });

    $('#resultRetest')?.addEventListener('click', () => {
      if (demoMode) {
        showToast('示例模式不能回测，请先退出示例');
        return;
      }
      if (!state.tap || !state.strafe) {
        showToast('先完成连点和急停测试');
        return;
      }
      state.retest = null;
      retestMode = true;
      saveState();
      go('strafe');
      showToast('先把推荐参数写入键盘，再开始回测');
    });

    $('#resultCopy')?.addEventListener('click', async () => {
      if (!lastAnalysis) renderResult();
      if (!lastAnalysis) return;
      const ok = await copyText(buildSummaryText(lastAnalysis));
      showToast(ok ? '参数已复制到剪贴板' : '复制失败，请手动选择文本');
    });

    $('#resultDownload')?.addEventListener('click', () => {
      if (!lastAnalysis) renderResult();
      if (!lastAnalysis) return;
      const date = new Date();
      const stamp = `${date.getFullYear()}${String(date.getMonth() + 1).padStart(2, '0')}${String(date.getDate()).padStart(2, '0')}-${String(date.getHours()).padStart(2, '0')}${String(date.getMinutes()).padStart(2, '0')}`;
      downloadText(`MAGLAB-${stamp}.txt`, buildSummaryText(lastAnalysis));
      showToast('参数文件已导出');
    });

    $('#resultPrint')?.addEventListener('click', () => {
      if (!lastAnalysis) renderResult();
      window.print();
    });
  }

  function bindGlobalKeys() {
    window.addEventListener('keydown', (event) => {
      if (!event.repeat) pressed.add(event.code);
    });
    window.addEventListener('keyup', (event) => {
      pressed.delete(event.code);
    });
    window.addEventListener('blur', () => pressed.clear());

    document.addEventListener('visibilitychange', () => {
      if (!document.hidden || !activeSession) return;
      abortActive();
      pressed.clear();
      showToast('检测到切换窗口，本次测试已取消');
      if (currentScreen === 'rest') prepareRest();
      if (currentScreen === 'tap') prepareTap();
      if (currentScreen === 'strafe') prepareStrafe();
      if (currentScreen === 'hold') prepareHold();
    });
  }

  /* ------------------------------------------------------------
     Init
  ------------------------------------------------------------ */

  function init() {
    renderHeroKeyboard();
    bindProfile();
    bindNavigation();
    bindTestButtons();
    bindResultButtons();
    bindGlobalKeys();
    syncProfileUI();
    updateSoundButton();
    updateStepper();
    updateProgress();
    updateHomeResume();

    /* 首次打开时，如果已经完成过测试，步进器显示进度 */
    if (state.completed.profile) updateStepper();

    /* 支持 #profile、#result 等直达（也方便截图与调试） */
    const hash = window.location.hash.replace('#', '');
    if (hash === 'demo') {
      setTimeout(loadDemo, 0);
    } else if (SCREENS.includes(hash) && hash !== 'home') {
      setTimeout(() => go(hash), 0);
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
