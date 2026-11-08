/* ============================================================
   MAGLAB PRO · CS2 Counter-Strafe Lab
   Source movement model + real keyboard/mouse input.
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

  function mean(arr) {
    return arr && arr.length ? arr.reduce((a, b) => a + b, 0) / arr.length : 0;
  }

  function median(arr) {
    if (!arr || !arr.length) return 0;
    const sorted = [...arr].sort((a, b) => a - b);
    const mid = Math.floor(sorted.length / 2);
    return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
  }

  function percentile(arr, p) {
    if (!arr || !arr.length) return 0;
    const sorted = [...arr].sort((a, b) => a - b);
    const index = clamp((p / 100) * (sorted.length - 1), 0, sorted.length - 1);
    const low = Math.floor(index);
    const high = Math.ceil(index);
    if (low === high) return sorted[low];
    return sorted[low] + (sorted[high] - sorted[low]) * (index - low);
  }

  function std(arr) {
    if (!arr || arr.length < 2) return 0;
    const m = mean(arr);
    return Math.sqrt(mean(arr.map((v) => (v - m) ** 2)));
  }

  function cv(arr) {
    const m = mean(arr);
    return m ? std(arr) / m : 0;
  }

  function fmt(value, digits = 0) {
    return Number.isFinite(value) ? Number(value).toFixed(digits) : '—';
  }

  function pct(value, digits = 0) {
    return `${(value * 100).toFixed(digits)}%`;
  }

  function signed(value) {
    if (value === null || value === undefined || Number.isNaN(value)) return '—';
    const v = Math.round(value);
    return `${v > 0 ? '+' : ''}${v}ms`;
  }

  function dateLabel(timestamp) {
    if (!timestamp) return '—';
    return new Date(timestamp).toLocaleString('zh-CN', {
      month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
    });
  }

  /* ------------------------------------------------------------
     Physics / weapon presets
  ------------------------------------------------------------ */

  const PHYSICS = {
    accelerate: 5.5,
    friction: 5.2,
    stopspeed: 80,
    dt: 0.001
  };

  const WEAPONS = {
    ak47: { id: 'ak47', name: 'AK-47', speed: 215 },
    m4a4: { id: 'm4a4', name: 'M4A4', speed: 225 },
    awp: { id: 'awp', name: 'AWP', speed: 200 },
    ssg08: { id: 'ssg08', name: 'SSG 08', speed: 230 }
  };

  const ACCURACY_BAND = 0.34;

  function stepVelocity(v, wishDir, maxSpeed, dt) {
    const speed = Math.abs(v);
    if (speed > 0) {
      const control = Math.max(speed, PHYSICS.stopspeed);
      const drop = control * PHYSICS.friction * dt;
      const nextSpeed = Math.max(0, speed - drop);
      v *= nextSpeed / speed;
    }
    if (wishDir !== 0) {
      const wishSpeed = maxSpeed;
      const currentSpeed = v * wishDir;
      const addSpeed = wishSpeed - currentSpeed;
      if (addSpeed > 0) {
        const accelSpeed = Math.min(PHYSICS.accelerate * dt * wishSpeed, addSpeed);
        v += accelSpeed * wishDir;
      }
    }
    return v;
  }

  function simulateIdealCounter(dirSign, maxSpeed, threshold) {
    let v = dirSign * maxSpeed;
    let t = 0;
    let band = null;
    let stop = null;
    const dt = 0.001;
    while (t < 1.5) {
      const prev = v;
      v = stepVelocity(v, -dirSign, maxSpeed, dt);
      t += dt;
      if (band === null && Math.abs(v) <= threshold) band = t * 1000;
      if (prev !== 0 && v !== 0 && prev * v < 0) {
        stop = t * 1000 - (dt * 1000) * (v / (v - prev));
        break;
      }
      if (v === 0) {
        stop = t * 1000;
        break;
      }
    }
    return { band, stop };
  }

  /**
   * Simulate one counter-strafe rep.
   * raw: { dir:'A'|'D', oldUp, newDown, newUp, shotAt } in ms relative to cue.
   * Returns the sample used by the report.
   */
  function simulateRep(raw, maxSpeed, threshold) {
    const dirSign = raw.dir === 'A' ? -1 : 1;
    const newSign = -dirSign;
    const oldUp = raw.oldUp;
    const newDown = raw.newDown;
    const newUp = raw.newUp;
    const shotAt = raw.shotAt;

    const end = Math.min(3200, Math.max(
      (shotAt === null || shotAt === undefined ? 0 : shotAt) + 900,
      (oldUp === null || oldUp === undefined ? 0 : oldUp) + 1800,
      (newUp === null || newUp === undefined ? 0 : newUp) + 700,
      1200
    ));

    const events = [];
    if (oldUp !== null && oldUp !== undefined) events.push({ t: Math.round(oldUp), type: 'oldUp' });
    if (newDown !== null && newDown !== undefined) events.push({ t: Math.round(newDown), type: 'newDown' });
    if (newUp !== null && newUp !== undefined) events.push({ t: Math.round(newUp), type: 'newUp' });
    events.sort((a, b) => a.t - b.t);

    let oldHeld = true;
    let newHeld = false;
    let eventIndex = 0;
    let v = dirSign * maxSpeed;
    let prevV = v;
    let shotSpeed = null;
    let bandAt = null;
    let stopAt = null;
    let overshoot = 0;
    let stopDistance = 0;
    let crossedZero = false;
    const trace = [];
    const shotMs = shotAt === null || shotAt === undefined ? null : Math.round(shotAt);
    const oldUpMs = oldUp === null || oldUp === undefined ? null : Math.round(oldUp);

    for (let time = 0; time <= end; time += 1) {
      while (eventIndex < events.length && events[eventIndex].t <= time) {
        const event = events[eventIndex];
        if (event.type === 'oldUp') oldHeld = false;
        if (event.type === 'newDown') newHeld = true;
        if (event.type === 'newUp') newHeld = false;
        eventIndex += 1;
      }

      if (shotMs !== null && shotSpeed === null && time >= shotMs) {
        shotSpeed = Math.abs(v);
      }

      if (time % 5 === 0) trace.push({ t: time, v });

      const wishRaw = (oldHeld ? dirSign : 0) + (newHeld ? newSign : 0);
      const wishDir = wishRaw > 0 ? 1 : wishRaw < 0 ? -1 : 0;
      prevV = v;
      v = stepVelocity(v, wishDir, maxSpeed, 0.001);

      const nextTime = time + 1;
      if (oldUpMs !== null && nextTime >= oldUpMs) {
        const a = Math.abs(prevV);
        const b = Math.abs(v);
        if (bandAt === null && b <= threshold) {
          if (a > threshold && b !== a) {
            bandAt = time + (threshold - a) / (b - a);
          } else {
            bandAt = nextTime;
          }
        }
        if (stopAt === null && prevV !== 0 && v !== 0 && prevV * v < 0) {
          stopAt = time + (0 - prevV) / (v - prevV);
        } else if (stopAt === null && v === 0) {
          stopAt = nextTime;
        }
        if (stopAt !== null) crossedZero = true;
        if (crossedZero) overshoot = Math.max(overshoot, v * newSign);
        stopDistance += ((a + b) / 2) * 0.001;
      }
    }

    if (shotSpeed === null) shotSpeed = Math.abs(v);

    const ideal = simulateIdealCounter(dirSign, maxSpeed, threshold);
    const actualBand = bandAt !== null && oldUpMs !== null ? bandAt - oldUpMs : null;
    const actualStop = stopAt !== null && oldUpMs !== null ? stopAt - oldUpMs : null;
    const noRelease = oldUp === null || oldUp === undefined;
    const earlyShot = shotMs !== null && oldUpMs !== null && shotMs < oldUpMs;
    const accurate = !noRelease && !earlyShot && shotSpeed <= threshold;
    const gap = (oldUp !== null && oldUp !== undefined && newDown !== null && newDown !== undefined)
      ? newDown - oldUp
      : null;
    const tap = (newDown !== null && newDown !== undefined && newUp !== null && newUp !== undefined)
      ? newUp - newDown
      : null;
    const shotLate = accurate && bandAt !== null && shotMs !== null && shotMs >= bandAt
      ? shotMs - bandAt
      : null;
    const shotEarly = !accurate && bandAt !== null && shotMs !== null && shotMs < bandAt
      ? bandAt - shotMs
      : null;
    const efficiency = actualBand && actualBand > 0 && ideal.band
      ? clamp((ideal.band / actualBand) * 100, 0, 100)
      : 0;

    const shotScore = accurate
      ? 100
      : clamp(100 - ((shotSpeed - threshold) / Math.max(threshold, 1)) * 100, 0, 90);
    const efficiencyScore = efficiency;
    const overshootScore = 100 - clamp((overshoot / Math.max(threshold * 1.2, 1)) * 100, 0, 100);
    const timingScore = accurate
      ? clamp(100 - Math.max(0, (shotLate || 0) - 40) * 1.1, 0, 100)
      : 0;
    const score = (noRelease || earlyShot)
      ? 0
      : 0.5 * shotScore + 0.25 * efficiencyScore + 0.15 * overshootScore + 0.1 * timingScore;

    return {
      dir: raw.dir,
      dirSign,
      oldUp: oldUp === null || oldUp === undefined ? null : oldUp,
      newDown: newDown === null || newDown === undefined ? null : newDown,
      newUp: newUp === null || newUp === undefined ? null : newUp,
      shotAt: shotAt === null || shotAt === undefined ? null : shotAt,
      reaction: oldUp !== null && oldUp !== undefined ? oldUp : null,
      shotReaction: shotAt,
      shotSpeed,
      accurate,
      noRelease,
      earlyShot,
      bandAt: actualBand,
      stopAt: actualStop,
      idealBand: ideal.band,
      idealStop: ideal.stop,
      efficiency,
      gap,
      tap,
      overshoot,
      stopDistance,
      shotLate,
      shotEarly,
      score,
      trace
    };
  }

  /* ------------------------------------------------------------
     State
  ------------------------------------------------------------ */

  const STORAGE_KEY = 'maglab_pro_v1';

  // 默认值对齐社区证据：Wooting CS2 新手基线 WASD 0.7 / RT 0.5；
  // WASD 推荐 0.4–0.7mm，RT 释放 0.2–0.5mm，连续 RT 默认关，RT 只开 WASD。
  const DEFAULT_HARDWARE = {
    actuation: 0.70,
    top: 0.10,
    bottom: 0.30,
    rtOn: true,
    rtPress: 0.25,
    rtRelease: 0.30,
    continuous: false,
    misTouch: 2,
    pressStyle: 'balanced',
    fingersRest: true
  };

  function defaultSettings() {
    return {
      weapon: 'ak47',
      reps: 20,
      mode: 'blind',
      pressure: true,
      label: '',
      hardware: { ...DEFAULT_HARDWARE }
    };
  }

  function freshState() {
    return {
      version: 1,
      settings: defaultSettings(),
      baseline: null,
      ab: null,
      sound: true
    };
  }

  function loadState() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return freshState();
      const parsed = JSON.parse(raw);
      const state = freshState();
      if (!parsed || typeof parsed !== 'object') return state;
      if (parsed.settings && typeof parsed.settings === 'object') {
        state.settings = {
          ...state.settings,
          ...parsed.settings,
          hardware: { ...DEFAULT_HARDWARE, ...(parsed.settings.hardware || {}) }
        };
      }
      if (parsed.baseline && typeof parsed.baseline === 'object') state.baseline = parsed.baseline;
      if (parsed.ab && typeof parsed.ab === 'object') state.ab = parsed.ab;
      if (typeof parsed.sound === 'boolean') state.sound = parsed.sound;

      const settings = state.settings;
      if (!WEAPONS[settings.weapon]) settings.weapon = 'ak47';
      settings.reps = settings.reps === 12 ? 12 : 20;
      settings.mode = settings.mode === 'guided' ? 'guided' : 'blind';
      settings.pressure = Boolean(settings.pressure);
      if (typeof settings.label !== 'string') settings.label = '';

      const hw = settings.hardware;
      hw.actuation = clamp(Number(hw.actuation) || 1.5, 0.1, 4);
      hw.top = clamp(Number(hw.top) || 0.15, 0, 0.5);
      hw.bottom = clamp(Number(hw.bottom) || 0.30, 0, 0.6);
      hw.rtPress = clamp(Number(hw.rtPress) || 0.10, 0.01, 0.5);
      hw.rtRelease = clamp(Number(hw.rtRelease) || 0.10, 0.01, 0.5);
      hw.rtOn = Boolean(hw.rtOn);
      hw.continuous = Boolean(hw.continuous);
      hw.fingersRest = Boolean(hw.fingersRest);
      hw.misTouch = clamp(Number(hw.misTouch) || 2, 1, 5);
      if (!['light', 'balanced', 'heavy'].includes(hw.pressStyle)) hw.pressStyle = 'balanced';
      return state;
    } catch (error) {
      return freshState();
    }
  }

  let state = loadState();
  let demoMode = false;
  let demoBackup = null;
  let currentScreen = 'setup';
  let pendingRole = 'baseline';
  let reportRole = 'baseline';
  let preLockSettings = null;
  let run = null;
  let rep = null;
  let repPhase = 'idle';
  let activeSession = null;
  let liveV = 0;
  let liveLast = 0;
  let liveTrace = [];
  let liveTraceStart = 0;
  let traceSampleMode = 'median';
  let lastReportMetrics = null;
  let toastTimer = null;
  let audioCtx = null;
  const pressed = new Set();

  function saveState() {
    if (demoMode) return;
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    } catch (error) {
      /* local only */
    }
  }

  function labelFromHardware(hw) {
    const rt = hw.rtOn ? `RT ${Number(hw.rtRelease).toFixed(2)}` : 'RT OFF';
    return `${Number(hw.actuation).toFixed(2)}mm / ${rt}`;
  }

  /* ------------------------------------------------------------
     Audio / toast
  ------------------------------------------------------------ */

  function getAudio() {
    if (!audioCtx) {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      if (!Ctx) return null;
      audioCtx = new Ctx();
    }
    if (audioCtx.state === 'suspended') audioCtx.resume().catch(() => {});
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
      /* ignore */
    }
  }

  function toast(message) {
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
     Session / timers
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

  /* ------------------------------------------------------------
     Navigation
  ------------------------------------------------------------ */

  function updateTopConfig() {
    const el = $('#topConfigLabel');
    if (!el) return;
    if (currentScreen === 'test' && run) {
      el.textContent = `${run.role === 'practice' ? 'PRACTICE' : run.role.toUpperCase()} · ${run.label}`;
      return;
    }
    if (currentScreen === 'report') {
      const result = state[reportRole];
      el.textContent = result ? `${reportRole.toUpperCase()} · ${result.label}` : '未开始';
      return;
    }
    el.textContent = `${pendingRole === 'ab' ? 'A/B' : 'BASELINE'} · ${state.settings.label || labelFromHardware(state.settings.hardware)}`;
  }

  function updateProgress() {
    const bar = $('#progressBar');
    if (!bar) return;
    let percent = 0;
    if (currentScreen === 'test' && run) {
      const totalReps = run.reps + (run.pressure && !run.practice ? run.pressureReps : 0);
      const doneReps = run.samples.length + (run.pressure && !run.practice ? run.pressureSamples.length : 0);
      percent = clamp((doneReps / Math.max(1, totalReps)) * 100, 0, 100);
    } else if (currentScreen === 'report') {
      percent = 100;
    }
    bar.style.width = `${percent}%`;
  }

  function showScreen(name, options = {}) {
    if (demoMode && name !== 'report') {
      exitDemo();
      return;
    }
    if (name === 'test' && !run) return;
    if (name === 'report' && !state[reportRole]) {
      toast('还没有可查看的报告');
      return;
    }
    if (currentScreen === 'test' && name !== 'test' && run && !run.finished && !options.force) {
      if (!window.confirm('退出本次测试？当前数据不会保存。')) return;
      abortRun(false);
    }
    if (name !== 'test' && activeSession) {
      activeSession.abort();
      activeSession = null;
    }
    currentScreen = name;
    $$('.screen').forEach((screen) => {
      screen.classList.toggle('is-active', screen.id === `screen-${name}`);
    });
    window.scrollTo(0, 0);
    updateTopConfig();
    updateProgress();
    if (name === 'setup') syncSetupUI();
    if (name === 'report') renderReport();
  }

  /* ------------------------------------------------------------
     Setup UI
  ------------------------------------------------------------ */

  function setSelected(container, selector, value) {
    if (!container) return;
    $$(selector, container).forEach((button) => {
      button.classList.toggle('is-selected', button.dataset.weapon === value
        || button.dataset.reps === value
        || button.dataset.mode === value
        || button.dataset.value === value);
    });
  }

  function syncHardwareUI() {
    const hw = state.settings.hardware;
    const bindings = [
      ['#hwActuation', hw.actuation],
      ['#hwTop', hw.top],
      ['#hwBottom', hw.bottom],
      ['#hwRtPress', hw.rtPress],
      ['#hwRtRelease', hw.rtRelease]
    ];
    bindings.forEach(([selector, value]) => {
      const input = $(selector);
      if (input && document.activeElement !== input) input.value = Number(value).toFixed(2);
    });
    $('#hwRtToggle')?.setAttribute('aria-checked', String(hw.rtOn));
    $('#hwContinuousToggle')?.setAttribute('aria-checked', String(hw.continuous));
    $('#hwFingersToggle')?.setAttribute('aria-checked', String(hw.fingersRest));
    if ($('#hwRtLabel')) $('#hwRtLabel').textContent = hw.rtOn ? 'RT 已开启' : 'RT 已关闭';
    if ($('#hwContinuousLabel')) $('#hwContinuousLabel').textContent = hw.continuous ? '连续 RT 开启' : '连续 RT 关闭';
    if ($('#hwFingersLabel')) $('#hwFingersLabel').textContent = hw.fingersRest ? '手指常搭 WASD' : '不常搭键';
    const mis = $('#hwMisTouch');
    if (mis) setSelected(mis, 'button', String(hw.misTouch));
    const style = $('#hwStyle');
    if (style) setSelected(style, 'button', hw.pressStyle);
  }

  function syncSetupUI() {
    const s = state.settings;
    const abLocked = pendingRole === 'ab' && Boolean(state.baseline);

    setSelected($('#weaponChips'), 'button', s.weapon);
    setSelected($('#repsChips'), 'button', String(s.reps));
    setSelected($('#modeChips'), 'button', s.mode);

    $$('#weaponChips button, #repsChips button, #modeChips button').forEach((button) => {
      button.classList.toggle('is-locked', abLocked);
      button.setAttribute('aria-disabled', String(abLocked));
    });
    $('#pressureToggle')?.classList.toggle('is-locked', abLocked);
    $('#pressureToggle')?.setAttribute('aria-disabled', String(abLocked));
    $('#pressureToggle')?.setAttribute('aria-checked', String(Boolean(s.pressure)));
    if ($('#pressureLabel')) {
      $('#pressureLabel').textContent = s.pressure ? '包含 8 次比赛模拟' : '不包含比赛模拟';
    }

    const labelInput = $('#configLabel');
    if (labelInput && document.activeElement !== labelInput) {
      labelInput.value = s.label || labelFromHardware(s.hardware);
    }
    syncHardwareUI();

    $$('#roleRow .role-chip').forEach((button) => {
      button.classList.toggle('is-selected', button.dataset.role === pendingRole);
    });
    const abButton = $('#roleABBtn');
    if (abButton) abButton.disabled = !state.baseline;
    const roleLabel = $('#setupRoleLabel');
    if (roleLabel) roleLabel.textContent = abLocked ? 'A/B SECOND RUN' : 'BASELINE';
    const abNote = $('#abLockNote');
    if (abNote) {
      abNote.classList.toggle('hidden', !abLocked);
      if (abLocked) {
        abNote.textContent = `已锁定基准条件：${WEAPONS[state.baseline.weapon]?.name || 'AK-47'} · ${state.baseline.reps} 次 · ${state.baseline.mode === 'blind' ? '盲测' : '训练'}${state.baseline.pressure ? ' · 含比赛模拟' : ''}。只改变键盘设置，再测一组。`;
      }
    }
    const start = $('#startRunBtn');
    if (start) {
      start.textContent = abLocked
        ? '开始 A/B 第二组 →'
        : s.pressure
          ? `开始 ${s.reps + 8} 次 · 含比赛模拟 →`
          : `开始 ${s.reps} 次测试 →`;
    }
    const practice = $('#practiceBtn');
    if (practice) practice.disabled = false;

    renderBaselineStatus();
    updateTopConfig();
  }

  function renderBaselineStatus() {
    const el = $('#baselineStatus');
    if (!el) return;
    if (!state.baseline) {
      el.classList.add('hidden');
      el.innerHTML = '';
      return;
    }
    const base = state.baseline;
    const metrics = computeMetrics(base.samples, base.threshold);
    el.classList.remove('hidden');
    el.innerHTML = `
      <div>上次基准 · <b>${escapeHtml(base.label)}</b> · ${fmt(metrics.score, 0)} 分 · ${dateLabel(base.finishedAt)}</div>
      <div style="margin-top:6px">
        <a href="#" data-setup-action="view-baseline">查看报告</a>
        <a href="#" data-setup-action="run-ab">A/B 第二组</a>
      </div>
    `;
  }

  function escapeHtml(value) {
    return String(value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function readHardwareFromUI() {
    const hw = state.settings.hardware;
    const pairs = [
      ['#hwActuation', 'actuation', 0.1, 4],
      ['#hwTop', 'top', 0, 0.5],
      ['#hwBottom', 'bottom', 0, 0.6],
      ['#hwRtPress', 'rtPress', 0.01, 0.5],
      ['#hwRtRelease', 'rtRelease', 0.01, 0.5]
    ];
    pairs.forEach(([selector, key, min, max]) => {
      const input = $(selector);
      if (!input) return;
      const value = clamp(Number(input.value) || hw[key], min, max);
      hw[key] = value;
      input.value = value.toFixed(2);
    });
  }

  function bindSetup() {
    $('#weaponChips')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-weapon]');
      if (!button || (pendingRole === 'ab' && state.baseline)) return;
      state.settings.weapon = button.dataset.weapon;
      saveState();
      syncSetupUI();
    });

    $('#repsChips')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-reps]');
      if (!button || (pendingRole === 'ab' && state.baseline)) return;
      state.settings.reps = Number(button.dataset.reps);
      saveState();
      syncSetupUI();
    });

    $('#modeChips')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-mode]');
      if (!button || (pendingRole === 'ab' && state.baseline)) return;
      state.settings.mode = button.dataset.mode;
      saveState();
      syncSetupUI();
    });

    $('#pressureToggle')?.addEventListener('click', () => {
      if (pendingRole === 'ab' && state.baseline) return;
      state.settings.pressure = !state.settings.pressure;
      saveState();
      syncSetupUI();
    });

    $('#configLabel')?.addEventListener('input', (event) => {
      state.settings.label = event.target.value;
      saveState();
    });

    const hardwareInputs = ['#hwActuation', '#hwTop', '#hwBottom', '#hwRtPress', '#hwRtRelease'];
    hardwareInputs.forEach((selector) => {
      $(selector)?.addEventListener('input', () => {
        readHardwareFromUI();
        if (!$('#configLabel')?.value.trim()) {
          state.settings.label = labelFromHardware(state.settings.hardware);
          if ($('#configLabel')) $('#configLabel').value = state.settings.label;
        }
        saveState();
        renderBaselineStatus();
      });
      $(selector)?.addEventListener('blur', () => {
        readHardwareFromUI();
        syncHardwareUI();
        saveState();
      });
    });

    $('#hwRtToggle')?.addEventListener('click', () => {
      state.settings.hardware.rtOn = !state.settings.hardware.rtOn;
      syncHardwareUI();
      saveState();
    });
    $('#hwContinuousToggle')?.addEventListener('click', () => {
      state.settings.hardware.continuous = !state.settings.hardware.continuous;
      syncHardwareUI();
      saveState();
    });
    $('#hwFingersToggle')?.addEventListener('click', () => {
      state.settings.hardware.fingersRest = !state.settings.hardware.fingersRest;
      syncHardwareUI();
      saveState();
    });

    $('#hwMisTouch')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-value]');
      if (!button) return;
      state.settings.hardware.misTouch = Number(button.dataset.value);
      syncHardwareUI();
      saveState();
    });

    $('#hwStyle')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-value]');
      if (!button) return;
      state.settings.hardware.pressStyle = button.dataset.value;
      syncHardwareUI();
      saveState();
    });

    $('#roleRow')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-role]');
      if (!button) return;
      setPendingRole(button.dataset.role);
    });

    $('#baselineStatus')?.addEventListener('click', (event) => {
      const target = event.target.closest('[data-setup-action]');
      if (!target) return;
      event.preventDefault();
      const action = target.dataset.setupAction;
      if (action === 'view-baseline') {
        reportRole = 'baseline';
        showScreen('report');
      }
      if (action === 'run-ab') {
        setPendingRole('ab');
      }
    });

    $('#startRunBtn')?.addEventListener('click', () => {
      if (pendingRole === 'ab' && !state.baseline) {
        toast('先完成一次基准测试，再做 A/B');
        return;
      }
      if (pendingRole === 'baseline' && state.baseline) {
        if (!window.confirm('已有基准数据。重新测试会覆盖基准和现有 A/B 对比，继续？')) return;
      }
      startNewRun(pendingRole, false);
    });

    $('#practiceBtn')?.addEventListener('click', () => startNewRun('practice', true));

    $('#heroStartBtn')?.addEventListener('click', () => {
      $('#setupCard')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
      setTimeout(() => $('#startRunBtn')?.focus(), 350);
    });
  }

  function setPendingRole(role) {
    if (role === 'ab' && !state.baseline) {
      toast('先完成一次基准测试');
      return;
    }
    if (role === pendingRole) return;

    if (role === 'ab' && state.baseline) {
      preLockSettings = {
        weapon: state.settings.weapon,
        reps: state.settings.reps,
        mode: state.settings.mode,
        pressure: state.settings.pressure
      };
      state.settings.weapon = state.baseline.weapon;
      state.settings.reps = state.baseline.reps;
      state.settings.mode = state.baseline.mode;
      state.settings.pressure = Boolean(state.baseline.pressure);
      state.settings.label = state.ab?.label || '配置 B';
    } else if (role === 'baseline') {
      if (preLockSettings) {
        state.settings.weapon = preLockSettings.weapon;
        state.settings.reps = preLockSettings.reps;
        state.settings.mode = preLockSettings.mode;
        state.settings.pressure = preLockSettings.pressure;
        preLockSettings = null;
      }
      if (state.baseline) {
        state.settings.weapon = state.baseline.weapon;
        state.settings.reps = state.baseline.reps;
        state.settings.mode = state.baseline.mode;
        state.settings.pressure = Boolean(state.baseline.pressure);
        state.settings.label = state.baseline.label;
      }
    }
    pendingRole = role;
    syncSetupUI();
  }

  /* ------------------------------------------------------------
     Run lifecycle
  ------------------------------------------------------------ */

  function startNewRun(role, practice) {
    if (run && !run.finished) {
      if (!window.confirm('退出当前测试并开始新测试？')) return;
      abortRun(false);
    }
    readHardwareFromUI();
    let weaponId;
    let reps;
    let mode;
    let label;
    let pressure;

    if (role === 'ab' && state.baseline) {
      weaponId = state.baseline.weapon;
      reps = state.baseline.reps;
      mode = state.baseline.mode;
      pressure = Boolean(state.baseline.pressure);
      label = $('#configLabel')?.value.trim() || state.settings.label || '配置 B';
    } else if (practice) {
      weaponId = state.settings.weapon;
      reps = 5;
      mode = state.settings.mode;
      pressure = false;
      label = `练习 · ${state.settings.label || labelFromHardware(state.settings.hardware)}`;
    } else {
      weaponId = state.settings.weapon;
      reps = state.settings.reps;
      mode = state.settings.mode;
      pressure = Boolean(state.settings.pressure);
      label = $('#configLabel')?.value.trim() || labelFromHardware(state.settings.hardware);
    }

    const weapon = WEAPONS[weaponId] || WEAPONS.ak47;
    if (!practice) {
      state.settings.label = label;
      saveState();
    }

    run = {
      role: practice ? 'practice' : role,
      practice: Boolean(practice),
      weaponId: weapon.id,
      weaponName: weapon.name,
      maxSpeed: weapon.speed,
      threshold: weapon.speed * ACCURACY_BAND,
      reps,
      mode,
      pressure,
      pressureReps: 8,
      pressureWindow: 900,
      label,
      hardware: { ...state.settings.hardware },
      samples: [],
      pressureSamples: [],
      phase: 'main',
      blockAttempts: 0,
      invalid: 0,
      attempts: 0,
      startedAt: Date.now(),
      finishedAt: null,
      finished: false
    };
    rep = null;
    repPhase = 'idle';
    reportRole = role === 'ab' ? 'ab' : 'baseline';

    showScreen('test', { force: true });
    prepareTestUI();
    startTestSession();
    beginRep();
  }

  function prepareTestUI() {
    const weapon = WEAPONS[run.weaponId] || WEAPONS.ak47;
    $('#testRoleLabel').textContent = run.role === 'practice' ? 'PRACTICE' : run.role.toUpperCase();
    $('#testTitle').textContent = `${run.role === 'practice' ? 'PRACTICE' : 'REP'} 01 / ${run.reps}`;
    $('#testConfigLabel').textContent = run.label;
    $('#csWeaponLabel').textContent = `${weapon.name} · ${weapon.speed} u/s`;
    $('#csBandHud').textContent = `≤ ${Math.round(run.threshold)}`;
    $('#liveBand').textContent = `≤ ${Math.round(run.threshold)} u/s`;
    const ideal = simulateIdealCounter(-1, run.maxSpeed, run.threshold);
    $('#csIdealSide').textContent = ideal.band ? `${Math.round(ideal.band)} ms` : '—';
    $('#liveWeapon').textContent = weapon.name;
    $('#liveModeLabel').textContent = run.mode === 'blind' ? 'BLIND' : 'GUIDED';
    $('#liveModeText').textContent = run.mode === 'blind' ? '盲测' : '训练';
    $('#csModeLabel').textContent = run.mode === 'blind' ? 'BLIND TEST' : 'GUIDED';
    $('#csStage').classList.toggle('blind', run.mode === 'blind');
    $('#csStage').classList.remove('pressure');
    $('#csWindow').classList.add('hidden');
    $('#csSpeedHud').textContent = run.mode === 'blind' ? '—' : '0';
    $('#csLastShotHud').textContent = '—';
    $('#csLastShotState').textContent = '—';
    $('#csRepLabel').textContent = `REP 01 / ${run.reps}`;
    $('#csSpeedFill').style.width = '0%';
    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#testOverlay').classList.add('hidden');
    $('#csFloatResult').classList.add('hidden');
    $('#csHint').textContent = '按住 A 开始。目标出现后松开 A、点按 D，并在速度进入精度带后点击靶区。';
    buildDots();
    updateLiveStats();
    updateTopConfig();
    updateProgress();
  }

  function buildDots() {
    const container = $('#csDots');
    if (!container) return;
    container.innerHTML = Array.from({ length: activeReps() }, (_, index) => (
      `<div class="dot" data-index="${index}"></div>`
    )).join('');
  }

  function markDots() {
    const container = $('#csDots');
    if (!container) return;
    const samples = activeSamples();
    Array.from(container.children).forEach((dot, index) => {
      dot.classList.remove('done', 'bad', 'now');
      if (index < samples.length) {
        dot.classList.add(samples[index].accurate ? 'done' : 'bad');
      } else if (index === samples.length) {
        dot.classList.add('now');
      }
    });
  }

  function startTestSession() {
    const session = createSession();
    activeSession = session;
    session.on(window, 'keydown', onTestKeyDown);
    session.on(window, 'keyup', onTestKeyUp);
    session.on($('#csRange'), 'pointerdown', onShot);
    session.on(document, 'visibilitychange', () => {
      if (document.hidden && run && !run.finished) {
        abortRun(false);
        showScreen('setup', { force: true });
        toast('检测到切换窗口，本次测试已取消');
      }
    });
    liveLast = now();
    const tick = () => {
      liveTick();
      session.raf(tick);
    };
    session.raf(tick);
  }

  function abortRun(showMessage = true) {
    if (!run) return;
    if (activeSession) {
      activeSession.abort();
      activeSession = null;
    }
    run = null;
    rep = null;
    repPhase = 'idle';
    if (showMessage) toast('测试已退出');
    updateProgress();
  }

  /* ------------------------------------------------------------
     Rep flow
  ------------------------------------------------------------ */

  function activeSamples() {
    if (!run) return [];
    return run.phase === 'pressure' ? run.pressureSamples : run.samples;
  }

  function activeReps() {
    if (!run) return 0;
    return run.phase === 'pressure' ? run.pressureReps : run.reps;
  }

  function activePrefix() {
    if (!run) return 'REP';
    if (run.phase === 'pressure') return 'MATCH';
    return run.practice ? 'PRACTICE' : 'REP';
  }

  function beginRep() {
    if (!run || run.finished) return;
    if (activeSamples().length >= activeReps()) {
      if (run.phase === 'pressure') finishRun();
      else finishMainBlock();
      return;
    }
    if (activeSession && activeSession.aborted) return;

    const samples = activeSamples();
    const dir = samples.length % 2 === 0 ? 'A' : 'D';
    const newDir = dir === 'A' ? 'D' : 'A';
    rep = {
      uid: `${Date.now()}-${Math.random()}`,
      index: samples.length + 1,
      dir,
      newDir,
      pressure: run.phase === 'pressure',
      dirCode: `Key${dir}`,
      newDirCode: `Key${newDir}`,
      dirSign: dir === 'A' ? -1 : 1,
      phase: 'waiting',
      holdStart: 0,
      cueAt: 0,
      oldUp: null,
      newDown: null,
      newUp: null,
      shotAt: null,
      finalized: false,
      invalid: false
    };
    repPhase = 'waiting';
    run.attempts += 1;
    run.blockAttempts += 1;
    liveV = 0;
    liveLast = now();
    liveTrace = [];
    liveTraceStart = now();

    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#testOverlay').classList.add('hidden');
    $('#csFloatResult').classList.add('hidden');
    $('#csShotFlash').classList.remove('active');
    $('#csDirection').classList.remove('armed');
    $('#csDirection').textContent = `HOLD ${dir}`;
    $('#csHint').textContent = rep.pressure
      ? `比赛模拟：按住 ${dir}，目标出现后 900ms 内完成急停 + 开枪。`
      : `按住 ${dir}，保持到目标出现。不要提前松开。`;
    $('#csWindow').classList.add('hidden');
    $('#csSpeedFill').style.width = '0%';
    $('#csSpeedHud').textContent = run.mode === 'blind' ? '—' : '0';
    $('#csLastShotHud').textContent = '—';
    $('#csLastShotState').textContent = '—';
    $('#csTrace').getContext('2d').clearRect(0, 0, $('#csTrace').width, $('#csTrace').height);
    updateKeyUI();
    markDots();
    updateTestHeader();

    if (pressed.has(rep.dirCode) && !pressed.has(rep.newDirCode)) {
      const currentRep = rep;
      activeSession?.setTimeout(() => {
        if (run && rep === currentRep && repPhase === 'waiting' && pressed.has(rep.dirCode)) startHold();
      }, 120);
    } else if (pressed.size > 0) {
      $('#csHint').textContent = `先松开所有方向键，再按住 ${dir}。`;
    }
  }

  function updateTestHeader() {
    if (!run || !rep) return;
    const prefix = activePrefix();
    $('#testTitle').textContent = `${prefix} ${String(rep.index).padStart(2, '0')} / ${activeReps()}`;
    $('#csRepLabel').textContent = `${prefix} ${String(rep.index).padStart(2, '0')} / ${activeReps()}`;
    updateProgress();
  }

  function startHold() {
    if (!rep || repPhase !== 'waiting') return;
    if (pressed.has(rep.newDirCode)) {
      failRep('提示出现前按下了反键。先松开所有方向键，再按住旧方向。');
      return;
    }
    repPhase = 'holding';
    rep.phase = 'holding';
    rep.holdStart = now();
    liveLast = now();
    $('#csDirection').textContent = `HOLD ${rep.dir}`;
    $('#csHint').textContent = '保持方向键。目标即将出现。';
    const delay = 1050 + Math.random() * 750;
    const currentRep = rep;
    activeSession?.setTimeout(() => {
      if (run && rep === currentRep && repPhase === 'holding') showCue();
    }, delay);
  }

  function showCue() {
    if (!rep || repPhase !== 'holding') return;
    if (!pressed.has(rep.dirCode)) {
      failRep('目标出现前松开了方向键。');
      return;
    }
    if (pressed.has(rep.newDirCode)) {
      failRep('目标出现前按下了反键。');
      return;
    }
    repPhase = 'armed';
    rep.phase = 'armed';
    rep.cueAt = now();
    rep.cue = 0;
    $('#csTarget').classList.add('is-live');
    $('#csDirection').textContent = `松开 ${rep.dir} → 点按 ${rep.newDir}`;
    $('#csDirection').classList.add('armed');
    $('#csHint').textContent = rep.pressure
      ? `比赛模拟：${run.pressureWindow}ms 内完成急停并点击靶区。`
      : '松开旧键 + 点按反键。速度进入精度带后点击靶区。';
    flashElement($('#csShotFlash'));
    beep(880, 0.07, 0.045);
    const currentRep = rep;
    if (rep.pressure) {
      const windowEl = $('#csWindow');
      const fill = $('#csWindowFill');
      windowEl.classList.remove('hidden');
      fill.style.transition = 'none';
      fill.style.width = '100%';
      void fill.offsetWidth;
      fill.style.transition = `width ${run.pressureWindow}ms linear`;
      fill.style.width = '0%';
      activeSession?.setTimeout(() => {
        if (run && rep === currentRep && repPhase === 'armed' && rep.shotAt === null) {
          pressureTimeout();
        }
      }, run.pressureWindow);
    } else {
      activeSession?.setTimeout(() => {
        if (run && rep === currentRep && repPhase === 'armed' && rep.shotAt === null) {
          failRep('未检测到开枪。目标出现后请在精度带内点击靶区。');
        }
      }, 2500);
    }
  }

  function pressureTimeout() {
    if (!run || !rep || rep.finalized || repPhase !== 'armed') return;
    rep.finalized = true;
    repPhase = 'feedback';
    $('#csWindow').classList.add('hidden');
    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#csDirection').textContent = 'TIMEOUT';
    $('#csDirection').classList.remove('armed');

    const sample = {
      index: activeSamples().length + 1,
      dir: rep.dir,
      dirSign: rep.dirSign,
      pressure: true,
      timeout: true,
      noRelease: true,
      accurate: false,
      shotAt: null,
      oldUp: rep.oldUp,
      newDown: rep.newDown,
      newUp: rep.newUp,
      shotSpeed: null,
      bandAt: null,
      stopAt: null,
      idealBand: null,
      idealStop: null,
      efficiency: 0,
      gap: null,
      tap: null,
      overshoot: 0,
      shotLate: null,
      shotEarly: null,
      score: 0,
      trace: []
    };
    activeSamples().push(sample);
    updateLiveStats();
    markDots();

    const flash = $('#csFloatResult');
    flash.classList.remove('hidden', 'good');
    flash.classList.add('bad');
    flash.innerHTML = `MATCH ${String(sample.index).padStart(2, '0')} · TIMEOUT<br><b>${run.pressureWindow}ms</b> <span>窗口内未开枪</span>`;
    $('#csSpeedHud').textContent = '—';
    $('#csLastShotHud').textContent = 'TIMEOUT';
    $('#csLastShotState').textContent = '超时';
    $('#csLastShotState').style.color = 'var(--red)';
    $('#csKeyMouse').classList.remove('is-active');
    updateKeyUI();
    beep(240, 0.1, 0.035);

    const currentRun = run;
    const currentRep = rep;
    if (activeSamples().length >= activeReps()) {
      activeSession?.setTimeout(() => {
        if (run === currentRun && !run.finished) finishRun();
      }, 1150);
    } else {
      activeSession?.setTimeout(() => {
        if (run === currentRun && rep === currentRep && !run.finished) beginRep();
      }, 1150);
    }
  }

  function onTestKeyDown(event) {
    if (!run || !rep || run.finished) return;
    if (event.repeat) return;
    if (event.code !== 'KeyA' && event.code !== 'KeyD') return;
    updateKeyUI();

    if (repPhase === 'waiting') {
      if (event.code === rep.dirCode) {
        if (pressed.has(rep.newDirCode)) {
          failRep('先松开反键，再按住旧方向。');
        } else {
          startHold();
        }
      } else if (event.code === rep.newDirCode) {
        failRep('提示出现前按下了反键。');
      }
      return;
    }

    if (repPhase === 'holding' && event.code === rep.newDirCode) {
      failRep('目标出现前按下了反键。');
      return;
    }

    if (repPhase === 'armed') {
      if (event.code === rep.newDirCode && rep.newDown === null) {
        rep.newDown = now() - rep.cueAt;
        flashElement($('#csKeyD'));
        updateKeyUI();
      }
    }
  }

  function onTestKeyUp(event) {
    if (!run || !rep || run.finished) return;
    if (event.code !== 'KeyA' && event.code !== 'KeyD') return;

    if (event.code === rep.dirCode) {
      if (repPhase === 'holding') {
        failRep('目标出现前松开了方向键。');
        return;
      }
      if ((repPhase === 'armed' || repPhase === 'shot') && rep.oldUp === null) {
        rep.oldUp = now() - rep.cueAt;
      }
    }
    if (event.code === rep.newDirCode) {
      if ((repPhase === 'armed' || repPhase === 'shot') && rep.newUp === null) {
        rep.newUp = now() - rep.cueAt;
      }
    }
    updateKeyUI();
  }

  function onShot(event) {
    if (!run || !rep || run.finished) return;
    if (event.button !== 0) return;
    if (repPhase !== 'armed') {
      if ((repPhase === 'waiting' || repPhase === 'holding')
        && $('#testOverlay').classList.contains('hidden')) {
        failRep('提示出现前开枪。先按住方向键，等目标出现。');
      }
      return;
    }
    if (rep.shotAt !== null) return;
    if (!$('#testOverlay').classList.contains('hidden')) return;

    rep.shotAt = now() - rep.cueAt;
    repPhase = 'shot';
    rep.phase = 'shot';
    flashElement($('#csShotFlash'));
    $('#csTarget').classList.remove('is-live');
    $('#csTarget').classList.add('is-hit');
    $('#csKeyMouse').classList.add('is-active');
    $('#csWindow').classList.add('hidden');
    $('#csHint').textContent = '已记录。等待反键松开...';
    beep(520, 0.04, 0.025, 'square');

    const currentRep = rep;
    activeSession?.setTimeout(() => {
      if (run && rep === currentRep && repPhase === 'shot') finalizeRep();
    }, 450);
  }

  function finalizeRep() {
    if (!run || !rep || rep.finalized) return;
    rep.finalized = true;
    repPhase = 'feedback';

    if (rep.shotAt === null) {
      failRep('未检测到开枪。');
      return;
    }

    const raw = {
      dir: rep.dir,
      oldUp: rep.oldUp,
      newDown: rep.newDown,
      newUp: rep.newUp,
      shotAt: rep.shotAt
    };
    const sample = simulateRep(raw, run.maxSpeed, run.threshold);
    sample.index = activeSamples().length + 1;
    sample.cue = 0;
    sample.pressure = Boolean(rep.pressure);
    sample.timeout = false;
    sample.shotAt = rep.shotAt;
    sample.oldUp = rep.oldUp;
    sample.newDown = rep.newDown;
    sample.newUp = rep.newUp;
    sample.noRelease = rep.oldUp === null;
    if (sample.noRelease) {
      sample.score = 0;
      sample.accurate = false;
    }
    activeSamples().push(sample);
    updateLiveStats();
    markDots();
    drawTrace($('#csTrace'), sample, { compact: true, threshold: run.threshold, maxSpeed: run.maxSpeed });
    $('#csWindow').classList.add('hidden');

    const accurateText = sample.noRelease
      ? '未松开旧键'
      : sample.accurate ? '精度带内' : '精度带外';
    const flash = $('#csFloatResult');
    flash.classList.remove('hidden', 'good', 'bad');
    flash.classList.add(sample.accurate ? 'good' : 'bad');
    flash.innerHTML = `${activePrefix()} ${String(sample.index).padStart(2, '0')} · ${sample.dir}→${rep.newDir}<br>
      <b>${Math.round(sample.shotSpeed)} u/s</b> <span>${accurateText}</span><br>
      <span>停速 ${sample.bandAt !== null ? `${Math.round(sample.bandAt)}ms` : '—'} · 间隔 ${sample.gap !== null ? signed(sample.gap) : '—'} · 过冲 ${Math.round(sample.overshoot)} u/s</span>`;

    $('#csSpeedHud').textContent = String(Math.round(sample.shotSpeed));
    $('#csLastShotHud').textContent = String(Math.round(sample.shotSpeed));
    $('#csLastShotState').textContent = sample.accurate ? '精度带内' : '带外';
    $('#csLastShotState').style.color = sample.accurate ? 'var(--lime)' : 'var(--red)';
    $('#csSpeedFill').style.width = `${clamp(sample.shotSpeed / run.maxSpeed * 100, 0, 100)}%`;
    $('#csKeyMouse').classList.remove('is-active');
    updateKeyUI();
    beep(sample.accurate ? 680 : 260, 0.08, 0.03);

    const currentRun = run;
    const currentRep = rep;
    if (activeSamples().length >= activeReps()) {
      activeSession?.setTimeout(() => {
        if (run !== currentRun || run.finished) return;
        if (run.phase === 'pressure') finishRun();
        else finishMainBlock();
      }, 1250);
    } else {
      activeSession?.setTimeout(() => {
        if (run === currentRun && rep === currentRep && !run.finished) beginRep();
      }, run.phase === 'pressure' ? 1100 : 1500);
    }
  }

  function failRep(reason) {
    if (!run || !rep || rep.finalized || rep.invalid) return;
    rep.invalid = true;
    repPhase = 'invalid';
    run.invalid += 1;
    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#csWindow').classList.add('hidden');
    $('#csDirection').textContent = 'INVALID';
    $('#csDirection').classList.remove('armed');
    $('#csHint').textContent = reason;
    showTestOverlay({
      kicker: 'INVALID REP',
      title: '本次无效',
      text: reason,
      stats: [{ label: '有效样本', value: `${run.samples.length} / ${run.reps}` }],
      actions: [{ label: '重试本组', primary: true, action: 'retry' }]
    });
  }

  function finishMainBlock() {
    if (!run || run.finished) return;
    if (run.phase === 'pressure') {
      finishRun();
      return;
    }
    if (run.phase === 'mainComplete') return;
    if (!run.pressure || run.practice) {
      finishRun();
      return;
    }
    run.phase = 'mainComplete';
    repPhase = 'feedback';
    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#csWindow').classList.add('hidden');
    const metrics = computeMetrics(run.samples, run.threshold);
    showTestOverlay({
      kicker: 'BLOCK 1 COMPLETE',
      title: `基础急停 ${metrics.validCount} 组完成`,
      text: `接下来 8 次比赛模拟：目标出现后 ${run.pressureWindow}ms 内完成急停 + 开枪。目标仍居中，只加时间压力。`,
      stats: [
        { label: '精度带命中', value: pct(metrics.accuracyRate) },
        { label: '中位停速', value: `${Math.round(metrics.stopBandMedian)} ms` },
        { label: '中位开枪', value: `${Math.round(metrics.shotSpeedMedian)} u/s` },
        { label: '松键→反键', value: signed(metrics.gapMedian) }
      ],
      actions: [
        { label: '开始比赛模拟', primary: true, action: 'pressure' },
        { label: '跳过，直接看报告', action: 'skip-pressure' }
      ]
    });
  }

  function startPressureBlock() {
    if (!run || run.finished) return;
    run.phase = 'pressure';
    run.pressureSamples = [];
    run.blockAttempts = 0;
    rep = null;
    repPhase = 'idle';
    $('#testRoleLabel').textContent = 'MATCH SIM';
    $('#csModeLabel').textContent = 'MATCH SIM';
    $('#liveModeLabel').textContent = 'MATCH';
    $('#csStage').classList.add('pressure');
    $('#csWindow').classList.add('hidden');
    $('#csFloatResult').classList.add('hidden');
    $('#csTarget').classList.remove('is-live', 'is-hit');
    $('#csHint').textContent = `比赛模拟：目标出现后 ${run.pressureWindow}ms 内完成急停 + 开枪。`;
    buildDots();
    updateLiveStats();
    beginRep();
  }

  function showTestOverlay({ kicker, title, text, stats, actions }) {
    const overlay = $('#testOverlay');
    if (!overlay) return;
    overlay.classList.remove('hidden');
    $('#testOverlayKicker').textContent = kicker || '';
    $('#testOverlayTitle').textContent = title || '';
    $('#testOverlayText').textContent = text || '';
    const statsEl = $('#testOverlayStats');
    if (stats && stats.length) {
      statsEl.classList.remove('hidden');
      statsEl.innerHTML = stats.map((item) => `
        <div class="overlay-stat"><span>${escapeHtml(item.label)}</span><b>${item.value}</b></div>
      `).join('');
    } else {
      statsEl.classList.add('hidden');
      statsEl.innerHTML = '';
    }
    const actionsEl = $('#testOverlayActions');
    actionsEl.innerHTML = (actions || []).map((item) => (
      `<button class="btn ${item.primary ? 'btn-primary' : 'btn-ghost'}" data-test-action="${item.action}" type="button">${escapeHtml(item.label)}</button>`
    )).join('');
  }

  function hideTestOverlay() {
    $('#testOverlay')?.classList.add('hidden');
  }

  function handleTestOverlayAction(action) {
    if (action === 'retry') {
      hideTestOverlay();
      if (run && !run.finished) {
        run.attempts -= 1;
        run.blockAttempts -= 1;
        beginRep();
      }
    }
    if (action === 'pressure') {
      hideTestOverlay();
      startPressureBlock();
    }
    if (action === 'skip-pressure') {
      hideTestOverlay();
      finishRun();
    }
    if (action === 'report') {
      hideTestOverlay();
      reportRole = run.role === 'ab' ? 'ab' : 'baseline';
      showScreen('report', { force: true });
    }
    if (action === 'again') {
      hideTestOverlay();
      const role = run.role;
      const practice = run.practice;
      abortRun(false);
      startNewRun(role === 'practice' ? pendingRole : role, practice);
    }
    if (action === 'setup') {
      hideTestOverlay();
      abortRun(false);
      showScreen('setup', { force: true });
    }
    if (action === 'finish-practice') {
      const role = pendingRole;
      if (role === 'baseline' && state.baseline
        && !window.confirm('已有基准数据。正式测试会覆盖基准和现有 A/B 对比，继续？')) return;
      hideTestOverlay();
      abortRun(false);
      startNewRun(role, false);
    }
  }

  function finishRun() {
    if (!run || run.finished) return;
    run.finished = true;
    run.finishedAt = Date.now();
    if (activeSession) {
      activeSession.abort();
      activeSession = null;
    }
    $('#csTarget').classList.remove('is-live', 'is-hit');
    updateKeyUI();
    updateProgress();

    const metrics = computeMetrics(run.samples, run.threshold);
    if (run.practice) {
      showTestOverlay({
        kicker: 'PRACTICE COMPLETE',
        title: '练习完成',
        text: '先熟悉节奏，再开始正式测试。正式测试会重新计数，并使用当前武器与模式。',
        stats: [
          { label: '平均开枪速度', value: `${Math.round(metrics.shotSpeedMedian)} u/s` },
          { label: '精度带命中', value: pct(metrics.accuracyRate) },
          { label: '中位停速', value: `${Math.round(metrics.stopBandMedian)} ms` }
        ],
        actions: [
          { label: '开始正式测试', primary: true, action: 'finish-practice' },
          { label: '返回设置', action: 'setup' }
        ]
      });
      beep(660, 0.12, 0.04);
      return;
    }

    const result = {
      role: run.role,
      weapon: run.weaponId,
      weaponName: run.weaponName,
      maxSpeed: run.maxSpeed,
      threshold: run.threshold,
      reps: run.reps,
      mode: run.mode,
      pressure: Boolean(run.pressure),
      pressureReps: run.pressureReps,
      pressureWindow: run.pressureWindow,
      pressureSamples: run.pressureSamples,
      label: run.label,
      hardware: run.hardware,
      samples: run.samples,
      invalid: run.invalid,
      attempts: run.attempts,
      startedAt: run.startedAt,
      finishedAt: run.finishedAt,
      duration: Math.round((run.finishedAt - run.startedAt) / 1000),
      score: metrics.score,
      grade: gradeForScore(metrics.score)
    };

    state[run.role] = result;
    if (run.role === 'baseline') state.ab = null;
    reportRole = run.role;
    saveState();
    renderBaselineStatus();
    updateTopConfig();
    updateProgress();

    const pressure = computePressureMetrics(result);
    const overlayStats = [
      { label: '精度带命中', value: pct(metrics.accuracyRate) },
      { label: '中位开枪速度', value: `${Math.round(metrics.shotSpeedMedian)} u/s` },
      { label: '中位停速', value: `${Math.round(metrics.stopBandMedian)} ms` },
      { label: '中位间隔', value: signed(metrics.gapMedian) }
    ];
    if (pressure) {
      overlayStats.push({ label: '压力精度', value: pct(pressure.accuracyRate) });
      overlayStats.push({ label: '压力超时', value: pct(pressure.timeoutRate) });
    }

    showTestOverlay({
      kicker: run.role === 'ab' ? 'A/B COMPLETE' : 'TEST COMPLETE',
      title: `${metrics.validCount} 组有效 · ${Math.round(metrics.score)} 分`,
      text: run.role === 'ab'
        ? `第二套配置已完成。${pressure ? '报告含基础 vs 比赛模拟对比。' : ''}查看 A/B 时注意单次样本波动。`
        : `基准测试已完成。${pressure ? '报告含基础 vs 比赛模拟对比。' : ''}下一步可以查看报告，或只改一个参数后做 A/B。`,
      stats: overlayStats,
      actions: [
        { label: '查看急停报告', primary: true, action: 'report' },
        { label: run.role === 'ab' ? '再测 B' : '再测一组', action: 'again' },
        { label: '返回设置', action: 'setup' }
      ]
    });
    beep(660, 0.12, 0.04);
  }

  /* ------------------------------------------------------------
     Live telemetry
  ------------------------------------------------------------ */

  function liveTick() {
    if (!run || !rep || run.finished || currentScreen !== 'test') return;
    if (repPhase !== 'waiting' && repPhase !== 'holding' && repPhase !== 'armed') return;
    const t = now();
    const dt = clamp((t - liveLast) / 1000, 0, 0.05);
    liveLast = t;
    if (dt <= 0) return;

    let wish = 0;
    if (pressed.has('KeyD') && !pressed.has('KeyA')) wish = 1;
    if (pressed.has('KeyA') && !pressed.has('KeyD')) wish = -1;
    const steps = Math.max(1, Math.ceil(dt / 0.001));
    const sub = dt / steps;
    for (let i = 0; i < steps; i += 1) {
      liveV = stepVelocity(liveV, wish, run.maxSpeed, sub);
    }
    const speed = Math.abs(liveV);
    $('#csSpeedHud').textContent = run.mode === 'blind' ? '—' : String(Math.round(speed));
    if (run.mode !== 'blind') {
      $('#csSpeedFill').style.width = `${clamp((speed / run.maxSpeed) * 100, 0, 100)}%`;
      liveTrace.push({ t: t - liveTraceStart, v: liveV });
      if (liveTrace.length > 400) liveTrace.shift();
      drawLiveTrace();
    }
  }

  function updateKeyUI() {
    $('#csKeyA')?.classList.toggle('is-active', pressed.has('KeyA'));
    $('#csKeyD')?.classList.toggle('is-active', pressed.has('KeyD'));
  }

  function updateLiveStats() {
    if (!run) return;
    const samples = activeSamples();
    const metrics = computeMetrics(samples, run.threshold);
    const shotSamples = samples.filter((sample) => !sample.timeout);
    $('#liveValid').textContent = `${samples.length} / ${run.blockAttempts}`;
    $('#liveAccuracy').textContent = shotSamples.length ? pct(metrics.accuracyRate) : '—';
    $('#liveStop').textContent = shotSamples.length ? `${Math.round(metrics.stopBandMedian)}ms` : '—';
    $('#liveGap').textContent = shotSamples.length ? signed(metrics.gapMedian) : '—';
    $('#liveOvershoot').textContent = shotSamples.length ? pct(metrics.overshootRate) : '—';
    $('#csEffSide').textContent = shotSamples.length ? `${Math.round(metrics.efficiencyMedian)}%` : '—';
    $('#csValidSide').textContent = `${samples.length} / ${run.blockAttempts}`;
  }

  /* ------------------------------------------------------------
     Metrics
  ------------------------------------------------------------ */

  function computeMetrics(samples, threshold) {
    const valid = (samples || []).filter((sample) => sample && !sample.noRelease);
    const all = samples || [];
    const shotSpeeds = valid.map((sample) => sample.shotSpeed);
    const stopBands = valid.map((sample) => sample.bandAt).filter((value) => value !== null && value !== undefined && value > 0);
    const gaps = valid.map((sample) => sample.gap).filter((value) => value !== null && value !== undefined);
    const taps = valid.map((sample) => sample.tap).filter((value) => value !== null && value !== undefined);
    const overshoots = valid.map((sample) => sample.overshoot).filter((value) => value !== null && value !== undefined);
    const reactions = valid.map((sample) => sample.oldUp).filter((value) => value !== null && value !== undefined);
    const shotReactions = valid.map((sample) => sample.shotAt).filter((value) => value !== null && value !== undefined);
    const shotLates = valid.map((sample) => sample.shotLate).filter((value) => value !== null && value !== undefined);
    const efficiencies = valid.map((sample) => sample.efficiency).filter((value) => Number.isFinite(value));
    const idealBands = valid.map((sample) => sample.idealBand).filter((value) => value !== null && value !== undefined);
    const accurateCount = valid.filter((sample) => sample.accurate).length;
    const fullStopCount = valid.filter((sample) => sample.shotSpeed <= 1).length;
    const overlapCount = gaps.filter((value) => value < -10).length;
    const deadCount = gaps.filter((value) => value > 35).length;
    const overshootCount = overshoots.filter((value) => value > 10).length;
    const underTapCount = valid.filter((sample) => (
      sample.tap !== null && sample.tap !== undefined
      && sample.idealStop !== null && sample.idealStop !== undefined
      && sample.tap < sample.idealStop - 20
    )).length;
    const overTapCount = valid.filter((sample) => sample.overshoot > 10 || (
      sample.tap !== null && sample.idealStop !== null && sample.tap > sample.idealStop + 60
    )).length;
    const inaccurateCount = valid.filter((sample) => !sample.accurate).length;
    const earlyShotCount = valid.filter((sample) => sample.earlyShot).length;
    const accuracyScore = valid.length ? (accurateCount / valid.length) * 100 : 0;
    const speedScore = valid.length
      ? 100 - clamp(((median(shotSpeeds) - threshold) / Math.max(threshold, 1)) * 100, 0, 100)
      : 0;
    const timingScoreAgg = valid.length
      ? 100 - clamp(Math.max(0, median(shotLates) - 40) * 1.1, 0, 100)
      : 0;
    const overshootScoreAgg = valid.length
      ? 100 - clamp((overshootCount / valid.length) * 100, 0, 100)
      : 0;
    const overallScore = valid.length
      ? 0.55 * accuracyScore
        + 0.15 * speedScore
        + 0.15 * median(efficiencies)
        + 0.10 * overshootScoreAgg
        + 0.05 * timingScoreAgg
      : 0;

    return {
      total: all.length,
      validCount: valid.length,
      invalidCount: all.length - valid.length,
      accuracyRate: valid.length ? accurateCount / valid.length : 0,
      accurateCount,
      fullStopRate: valid.length ? fullStopCount / valid.length : 0,
      fullStopCount,
      shotSpeedMedian: median(shotSpeeds),
      shotSpeedP90: percentile(shotSpeeds, 90),
      shotSpeedMin: shotSpeeds.length ? Math.min(...shotSpeeds) : 0,
      stopBandMedian: median(stopBands),
      stopBandP90: percentile(stopBands, 90),
      idealBandMedian: median(idealBands),
      idealStopMedian: median(valid.map((sample) => sample.idealStop).filter((value) => value !== null && value !== undefined)),
      efficiencyMedian: median(efficiencies),
      efficiencyMean: mean(efficiencies),
      gapMedian: median(gaps),
      overlapRate: valid.length ? overlapCount / valid.length : 0,
      deadTimeRate: valid.length ? deadCount / valid.length : 0,
      tapMedian: median(taps),
      underTapRate: valid.length ? underTapCount / valid.length : 0,
      overTapRate: valid.length ? overTapCount / valid.length : 0,
      overshootRate: valid.length ? overshootCount / valid.length : 0,
      overshootMedian: median(overshoots),
      overshootP90: percentile(overshoots, 90),
      reactionMedian: median(reactions),
      shotReactionMedian: median(shotReactions),
      shotLateMedian: median(shotLates),
      inaccurateCount,
      earlyShotCount,
      earlyShotRate: valid.length ? earlyShotCount / valid.length : 0,
      consistencyCv: cv(shotSpeeds),
      stopConsistencyCv: cv(stopBands),
      score: overallScore,
      accuracyScore,
      speedScore,
      timingScoreAgg,
      overshootScoreAgg,
      threshold,
      grade: gradeForScore(overallScore)
    };
  }

  function computePressureMetrics(result) {
    if (!result || !Array.isArray(result.pressureSamples) || !result.pressureSamples.length) return null;
    const samples = result.pressureSamples;
    const shots = samples.filter((sample) => !sample.timeout);
    const metrics = computeMetrics(shots, result.threshold);
    const timeouts = samples.filter((sample) => sample.timeout).length;
    return {
      ...metrics,
      blockCount: samples.length,
      shotCount: shots.length,
      timeoutCount: timeouts,
      timeoutRate: samples.length ? timeouts / samples.length : 0
    };
  }

  function gradeForScore(score) {
    if (!Number.isFinite(score)) return '—';
    if (score >= 90) return 'A+';
    if (score >= 82) return 'A';
    if (score >= 72) return 'B';
    if (score >= 60) return 'C';
    return 'D';
  }

  /* ------------------------------------------------------------
     Chart drawing
  ------------------------------------------------------------ */

  function drawTrace(canvas, sample, options = {}) {
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    const W = canvas.width;
    const H = canvas.height;
    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = 'rgba(0,0,0,0)';
    ctx.fillRect(0, 0, W, H);

    if (!sample || !sample.trace || !sample.trace.length) {
      ctx.fillStyle = 'rgba(139,152,170,0.5)';
      ctx.font = '11px monospace';
      ctx.textAlign = 'center';
      ctx.fillText('等待数据', W / 2, H / 2);
      return;
    }

    const left = options.compact ? 10 : 58;
    const right = options.compact ? 10 : 18;
    const top = options.compact ? 8 : 20;
    const bottom = options.compact ? 8 : 34;
    const plotW = W - left - right;
    const plotH = H - top - bottom;
    const maxY = (options.maxSpeed || run?.maxSpeed || 250) * 1.08;
    const threshold = options.threshold || run?.threshold || 73;
    const eventEnd = Math.max(
      sample.oldUp || 0,
      sample.newDown || 0,
      sample.newUp || 0,
      sample.shotAt || 0
    );
    const maxT = Math.max(600, Math.min(1200, eventEnd + 300));
    const cx = left;
    const cy = top + plotH / 2;
    const x = (t) => left + clamp(t / maxT, 0, 1) * plotW;
    const y = (v) => cy - (v / maxY) * (plotH / 2);

    if (!options.compact) {
      ctx.fillStyle = 'rgba(255,255,255,0.015)';
      ctx.fillRect(left, top, plotW, plotH);
      ctx.strokeStyle = 'rgba(255,255,255,0.06)';
      ctx.lineWidth = 1;
      for (let i = 0; i <= 4; i += 1) {
        const yy = top + (plotH / 4) * i;
        ctx.beginPath();
        ctx.moveTo(left, yy);
        ctx.lineTo(left + plotW, yy);
        ctx.stroke();
      }
      for (let i = 0; i <= 5; i += 1) {
        const xx = left + (plotW / 5) * i;
        ctx.beginPath();
        ctx.moveTo(xx, top);
        ctx.lineTo(xx, top + plotH);
        ctx.stroke();
      }
    }

    // 34% accuracy band around zero
    ctx.fillStyle = 'rgba(215,255,63,0.07)';
    ctx.fillRect(left, y(threshold), plotW, y(-threshold) - y(threshold));
    ctx.strokeStyle = 'rgba(215,255,63,0.25)';
    ctx.setLineDash([3, 4]);
    ctx.beginPath();
    ctx.moveTo(left, y(threshold));
    ctx.lineTo(left + plotW, y(threshold));
    ctx.moveTo(left, y(-threshold));
    ctx.lineTo(left + plotW, y(-threshold));
    ctx.stroke();
    ctx.setLineDash([]);

    // zero line
    ctx.strokeStyle = 'rgba(255,255,255,0.18)';
    ctx.beginPath();
    ctx.moveTo(left, cy);
    ctx.lineTo(left + plotW, cy);
    ctx.stroke();

    // velocity line, split at zero for overshoot color
    const points = sample.trace
      .filter((point) => point.t <= maxT)
      .map((point) => ({ x: x(point.t), y: y(point.v * sample.dirSign) }));
    ctx.lineWidth = options.compact ? 2 : 2.5;
    for (let i = 1; i < points.length; i += 1) {
      const a = points[i - 1];
      const b = points[i];
      ctx.strokeStyle = (a.y > cy + 1 || b.y > cy + 1) ? '#ff5268' : '#d7ff3f';
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }

    // markers
    const markers = [
      ['REL', sample.oldUp, '#48dfff'],
      ['CTR', sample.newDown, '#ffb648'],
      ['SHOT', sample.shotAt, '#d7ff3f'],
      ['UP', sample.newUp, '#8b98aa']
    ];
    markers.forEach(([label, time, color]) => {
      if (time === null || time === undefined) return;
      const xx = x(time);
      ctx.strokeStyle = color;
      ctx.globalAlpha = 0.45;
      ctx.setLineDash([2, 4]);
      ctx.beginPath();
      ctx.moveTo(xx, top);
      ctx.lineTo(xx, top + plotH);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.globalAlpha = 1;
      if (!options.compact) {
        ctx.fillStyle = color;
        ctx.font = '9px monospace';
        ctx.textAlign = 'center';
        ctx.fillText(label, xx, top - 6);
      }
    });

    // shot point
    if (sample.shotAt !== null && sample.shotAt !== undefined) {
      const sx = x(sample.shotAt);
      const closest = points.reduce((best, point) => (
        Math.abs(point.x - sx) < Math.abs(best.x - sx) ? point : best
      ), points[0]);
      ctx.fillStyle = sample.accurate ? '#d7ff3f' : '#ff5268';
      ctx.beginPath();
      ctx.arc(closest.x, closest.y, 4.2, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = 'rgba(0,0,0,0.5)';
      ctx.lineWidth = 1;
      ctx.stroke();
    }

    if (!options.compact) {
      ctx.fillStyle = 'rgba(139,152,170,0.7)';
      ctx.font = '9px monospace';
      ctx.textAlign = 'right';
      ctx.fillText(`+${Math.round(maxY)}`, left - 8, top + 4);
      ctx.fillText('0', left - 8, cy + 3);
      ctx.fillText(`−${Math.round(maxY)}`, left - 8, top + plotH);
      ctx.textAlign = 'center';
      ctx.fillText(`${Math.round(maxT)}ms`, left + plotW, H - 8);
      ctx.textAlign = 'left';
      ctx.fillText('u/s', 4, 12);
    }
  }

  function drawLiveTrace() {
    const canvas = $('#csTrace');
    if (!canvas || !run || !rep) return;
    if (run.mode === 'blind') {
      canvas.getContext('2d').clearRect(0, 0, canvas.width, canvas.height);
      return;
    }
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    const W = canvas.width;
    const H = canvas.height;
    const left = 10;
    const right = 10;
    const top = 8;
    const bottom = 8;
    const plotW = W - left - right;
    const plotH = H - top - bottom;
    const cy = top + plotH / 2;
    const maxY = run.maxSpeed * 1.05;
    const maxT = Math.max(900, liveTrace.length ? liveTrace[liveTrace.length - 1].t : 900);
    const x = (t) => left + clamp(t / maxT, 0, 1) * plotW;
    const y = (v) => cy - (v * rep.dirSign / maxY) * (plotH / 2);

    ctx.fillStyle = 'rgba(215,255,63,0.06)';
    ctx.fillRect(left, y(run.threshold), plotW, y(-run.threshold) - y(run.threshold));
    ctx.strokeStyle = 'rgba(255,255,255,0.12)';
    ctx.beginPath();
    ctx.moveTo(left, cy);
    ctx.lineTo(left + plotW, cy);
    ctx.stroke();
    if (!liveTrace.length) return;
    ctx.strokeStyle = '#48dfff';
    ctx.lineWidth = 2;
    ctx.beginPath();
    const start = Math.max(0, liveTrace.length - 220);
    for (let i = start; i < liveTrace.length; i += 1) {
      const point = liveTrace[i];
      const xx = x(point.t);
      const yy = y(point.v);
      if (i === start) ctx.moveTo(xx, yy);
      else ctx.lineTo(xx, yy);
    }
    ctx.stroke();
  }

  function drawScatter(canvas, result, metrics) {
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    const W = canvas.width;
    const H = canvas.height;
    ctx.clearRect(0, 0, W, H);
    const left = 62;
    const right = 22;
    const top = 20;
    const bottom = 38;
    const plotW = W - left - right;
    const plotH = H - top - bottom;
    const maxStop = Math.max(240, ...result.samples.map((s) => s.bandAt || 0).filter(Boolean), 1);
    const maxSpeed = Math.max(result.threshold * 2.4, ...result.samples.map((s) => s.shotSpeed || 0), 1);
    const x = (value) => left + clamp(value / maxStop, 0, 1) * plotW;
    const y = (value) => top + plotH - clamp(value / maxSpeed, 0, 1) * plotH;

    // grid
    ctx.strokeStyle = 'rgba(255,255,255,0.06)';
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i += 1) {
      const yy = top + (plotH / 4) * i;
      ctx.beginPath();
      ctx.moveTo(left, yy);
      ctx.lineTo(left + plotW, yy);
      ctx.stroke();
    }
    for (let i = 0; i <= 4; i += 1) {
      const xx = left + (plotW / 4) * i;
      ctx.beginPath();
      ctx.moveTo(xx, top);
      ctx.lineTo(xx, top + plotH);
      ctx.stroke();
    }

    // threshold line
    ctx.strokeStyle = 'rgba(215,255,63,0.45)';
    ctx.setLineDash([4, 4]);
    ctx.beginPath();
    ctx.moveTo(left, y(result.threshold));
    ctx.lineTo(left + plotW, y(result.threshold));
    ctx.stroke();
    ctx.setLineDash([]);
    ctx.fillStyle = 'rgba(215,255,63,0.75)';
    ctx.font = '9px monospace';
    ctx.textAlign = 'left';
    ctx.fillText(`34% · ${Math.round(result.threshold)} u/s`, left + 4, y(result.threshold) - 5);

    // ideal stop line
    if (metrics.idealBandMedian) {
      ctx.strokeStyle = 'rgba(72,223,255,0.3)';
      ctx.setLineDash([3, 4]);
      ctx.beginPath();
      ctx.moveTo(x(metrics.idealBandMedian), top);
      ctx.lineTo(x(metrics.idealBandMedian), top + plotH);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = 'rgba(72,223,255,0.7)';
      ctx.fillText(`理论 ${Math.round(metrics.idealBandMedian)}ms`, x(metrics.idealBandMedian) + 4, top + 12);
    }

    result.samples.forEach((sample) => {
      if (sample.bandAt === null || sample.bandAt === undefined) return;
      const xx = x(sample.bandAt);
      const yy = y(sample.shotSpeed);
      ctx.fillStyle = sample.accurate ? 'rgba(215,255,63,0.82)' : 'rgba(255,82,104,0.82)';
      ctx.beginPath();
      ctx.arc(xx, yy, 4.2, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = 'rgba(7,10,15,0.7)';
      ctx.lineWidth = 1;
      ctx.stroke();
    });

    // axes labels
    ctx.fillStyle = 'rgba(139,152,170,0.75)';
    ctx.font = '9px monospace';
    ctx.textAlign = 'center';
    ctx.fillText('停到 34% (ms)', left + plotW / 2, H - 10);
    ctx.save();
    ctx.translate(12, top + plotH / 2);
    ctx.rotate(-Math.PI / 2);
    ctx.fillText('开枪速度 (u/s)', 0, 0);
    ctx.restore();
    ctx.textAlign = 'left';
    ctx.fillText('0', left - 8, top + plotH + 3);
    ctx.textAlign = 'right';
    ctx.fillText(`${Math.round(maxStop)}`, left + plotW, top + plotH + 14);
    ctx.textAlign = 'right';
    ctx.fillText(`${Math.round(maxSpeed)}`, left - 8, top + 4);
  }

  /* ------------------------------------------------------------
     Report
  ------------------------------------------------------------ */

  function renderReport() {
    const result = state[reportRole];
    if (!result) {
      toast('没有可查看的报告');
      showScreen('setup', { force: true });
      return;
    }
    $('#demoBanner')?.classList.toggle('hidden', !demoMode);
    const metrics = computeMetrics(result.samples, result.threshold);
    lastReportMetrics = metrics;
    const modeText = result.mode === 'blind' ? '盲测' : '训练';
    $('#reportTitle').textContent = '急停报告';
    $('#reportMeta').textContent = `${result.weaponName} · ${result.reps} 次 · ${modeText} · ${result.label} · ${dateLabel(result.finishedAt)}`;
    $('#reportScore').textContent = Math.round(metrics.score);
    $('#reportGrade').textContent = `${metrics.grade} · ${metrics.validCount} 有效 / ${result.invalid} 次无效重试${metrics.invalidCount ? ` / ${metrics.invalidCount} 未松键` : ''}`;
    $('#metricSampleCount').textContent = `${metrics.validCount} 有效样本`;

    $('#reportRoleRow')?.classList.remove('hidden');
    $$('#reportRoleRow .role-chip').forEach((button) => {
      button.classList.toggle('is-selected', button.dataset.role === reportRole);
      if (button.dataset.role === 'ab') {
        button.disabled = !state.ab;
        button.classList.toggle('hidden', !state.ab);
      }
    });

    renderMetricGrid(metrics, result);
    renderReportTrace(result, metrics);
    drawScatter($('#reportScatter'), result, metrics);
    renderPressurePanel(result, metrics);
    renderDiagnosis(result, metrics);
    renderRecommendation(result, metrics);
    renderABComparison();
    updateTopConfig();
    updateProgress();
  }

  function renderMetricGrid(metrics, result) {
    const cards = [
      {
        label: '精度带命中率',
        value: pct(metrics.accuracyRate),
        sub: `${metrics.accurateCount} / ${metrics.validCount} 枪 · 阈值 ${Math.round(result.threshold)} u/s`,
        tone: metrics.accuracyRate >= 0.8 ? 'good' : metrics.accuracyRate >= 0.6 ? 'info' : 'warn'
      },
      {
        label: '中位开枪速度',
        value: Math.round(metrics.shotSpeedMedian),
        unit: 'u/s',
        sub: `P90 ${Math.round(metrics.shotSpeedP90)} · 最低 ${Math.round(metrics.shotSpeedMin)}`,
        tone: metrics.shotSpeedMedian <= result.threshold ? 'good' : 'warn'
      },
      {
        label: '停到 34%',
        value: Math.round(metrics.stopBandMedian),
        unit: 'ms',
        sub: `理论 ${Math.round(metrics.idealBandMedian)}ms · P90 ${Math.round(metrics.stopBandP90)}ms`,
        tone: metrics.efficiencyMedian >= 82 ? 'good' : metrics.efficiencyMedian >= 70 ? 'info' : 'warn'
      },
      {
        label: '制动效率',
        value: Math.round(metrics.efficiencyMedian),
        unit: '%',
        sub: '理论停速 / 实际停速',
        tone: metrics.efficiencyMedian >= 82 ? 'good' : metrics.efficiencyMedian >= 70 ? 'info' : 'warn'
      },
      {
        label: '松键→反键',
        value: signed(metrics.gapMedian),
        sub: `重叠 ${pct(metrics.overlapRate)} · 空档 ${pct(metrics.deadTimeRate)}`,
        tone: metrics.overlapRate > 0.25 || metrics.deadTimeRate > 0.25 ? 'warn' : 'good'
      },
      {
        label: '反键按住',
        value: metrics.tapMedian ? Math.round(metrics.tapMedian) : '—',
        unit: metrics.tapMedian ? 'ms' : '',
        sub: `过冲率 ${pct(metrics.overshootRate)} · 中位 ${Math.round(metrics.overshootMedian)} u/s`,
        tone: metrics.overshootRate > 0.3 ? 'warn' : 'good'
      },
      {
        label: '提示→松键',
        value: Math.round(metrics.reactionMedian),
        unit: 'ms',
        sub: `提示→开枪 ${Math.round(metrics.shotReactionMedian)}ms`,
        tone: metrics.reactionMedian < 220 ? 'good' : metrics.reactionMedian < 280 ? 'info' : 'warn'
      },
      {
        label: '开枪速度波动',
        value: Math.round(metrics.consistencyCv * 100),
        unit: '%',
        sub: `停速波动 ${Math.round(metrics.stopConsistencyCv * 100)}% · 完全停止 ${pct(metrics.fullStopRate)}`,
        tone: metrics.consistencyCv < 0.22 ? 'good' : metrics.consistencyCv < 0.35 ? 'info' : 'warn'
      }
    ];
    $('#metricGrid').innerHTML = cards.map((card) => `
      <div class="metric-card ${card.tone || ''}">
        <span>${card.label}</span>
        <b>${card.value}${card.unit ? `<small>${card.unit}</small>` : ''}</b>
        <p>${card.sub}</p>
      </div>
    `).join('');
  }

  function pickTraceSample(result) {
    const samples = [...result.samples].filter((sample) => !sample.noRelease && sample.trace && sample.trace.length);
    if (!samples.length) return null;
    const sorted = [...samples].sort((a, b) => a.score - b.score);
    if (traceSampleMode === 'best') return sorted[sorted.length - 1];
    if (traceSampleMode === 'worst') return sorted[0];
    return sorted[Math.floor(sorted.length / 2)];
  }

  function renderReportTrace(result, metrics) {
    const sample = pickTraceSample(result);
    drawTrace($('#reportTrace'), sample, { compact: false, threshold: result.threshold, maxSpeed: result.maxSpeed });
    if (!sample) {
      $('#traceCaption').textContent = '无有效样本';
      return;
    }
    $('#traceCaption').textContent = `REP ${String(sample.index).padStart(2, '0')} · ${sample.dir}→${sample.dir === 'A' ? 'D' : 'A'} · ${Math.round(sample.shotSpeed)} u/s · 停速 ${sample.bandAt !== null ? `${Math.round(sample.bandAt)}ms` : '—'}`;
  }

  function pressureDeltaItem(label, before, after, valueUnit, deltaUnit, goodUp, digits = 0) {
    const delta = after - before;
    const rendered = Number.isFinite(delta) ? delta.toFixed(digits) : '—';
    const good = goodUp ? delta > 0 : delta < 0;
    const cls = Math.abs(delta) < 0.01 ? '' : good ? 'good' : 'bad';
    const beforeText = Number.isFinite(before) ? `${before.toFixed(digits)}${valueUnit}` : '—';
    const afterText = Number.isFinite(after) ? `${after.toFixed(digits)}${valueUnit}` : '—';
    return `
      <div class="pressure-card">
        <span>${label}</span>
        <div class="pressure-values"><em>${beforeText}</em><b>→ ${afterText}</b></div>
        <div class="pressure-delta ${cls}">${Number.isFinite(delta) && delta > 0 ? '+' : ''}${rendered}${deltaUnit}</div>
      </div>
    `;
  }

  function renderPressurePanel(result, dryMetrics) {
    const panel = $('#pressurePanel');
    if (!panel) return;
    const pressure = computePressureMetrics(result);
    if (!pressure || !result.pressure) {
      panel.classList.add('hidden');
      return;
    }
    panel.classList.remove('hidden');
    $('#pressureNote').textContent = `基础 ${result.reps} 次 vs 比赛模拟 ${pressure.blockCount} 次 · ${result.pressureWindow || 900}ms 窗口`;

    const cards = [
      pressureDeltaItem('精度带命中率', dryMetrics.accuracyRate * 100, pressure.accuracyRate * 100, '%', 'pp', true, 0),
      pressureDeltaItem('中位开枪速度', dryMetrics.shotSpeedMedian, pressure.shotSpeedMedian, 'u/s', 'u/s', false, 0),
      pressureDeltaItem('停到 34%', dryMetrics.stopBandMedian, pressure.stopBandMedian, 'ms', 'ms', false, 0),
      pressureDeltaItem('过冲率', dryMetrics.overshootRate * 100, pressure.overshootRate * 100, '%', 'pp', false, 0)
    ];
    cards.push(`
      <div class="pressure-card">
        <span>窗口超时率</span>
        <div class="pressure-values"><em>—</em><b>${pct(pressure.timeoutRate)}</b></div>
        <div class="pressure-delta ${pressure.timeoutRate > 0.2 ? 'bad' : 'good'}">${pressure.timeoutCount} / ${pressure.blockCount} 次</div>
      </div>
    `);
    cards.push(`
      <div class="pressure-card">
        <span>压力块有效开枪</span>
        <div class="pressure-values"><em>—</em><b>${pressure.shotCount} / ${pressure.blockCount}</b></div>
        <div class="pressure-delta">只统计急停，不含瞄准</div>
      </div>
    `);
    $('#pressureGrid').innerHTML = cards.join('');

    const verdicts = [];
    const accuracyDrop = (dryMetrics.accuracyRate - pressure.accuracyRate) * 100;
    const stopRise = pressure.stopBandMedian - dryMetrics.stopBandMedian;
    const overshootRise = (pressure.overshootRate - dryMetrics.overshootRate) * 100;
    if (accuracyDrop > 10) verdicts.push(`压力下精度带命中下降 ${Math.round(accuracyDrop)}pp`);
    if (stopRise > 15) verdicts.push(`压力下停到 34% 慢了 ${Math.round(stopRise)}ms`);
    if (overshootRise > 15) verdicts.push(`压力下过冲率增加 ${Math.round(overshootRise)}pp`);
    if (pressure.timeoutRate > 0.2) verdicts.push(`窗口超时 ${pct(pressure.timeoutRate)}`);
    if (pressure.accuracyRate < 0.7 || dryMetrics.accuracyRate < 0.7) {
      $('#pressureVerdict').textContent = `两个块的精度带命中都偏低（基础 ${pct(dryMetrics.accuracyRate)} / 压力 ${pct(pressure.accuracyRate)}）。先解决早枪/停速，不建议现在追求更激进的参数；用比赛模拟做 A/B 时，把精度带命中率作为第一指标。`;
    } else if (pressure.overshootRate > 0.3 || dryMetrics.overshootRate > 0.3) {
      $('#pressureVerdict').textContent = `过冲率偏高（基础 ${pct(dryMetrics.overshootRate)} / 压力 ${pct(pressure.overshootRate)}）。先练在速度零点松开反键，再谈更灵敏的 RT；否则压力下会更容易反向移动。`;
    } else if (verdicts.length) {
      $('#pressureVerdict').textContent = `${verdicts.join('；')}。这说明基础块的手感没有完全迁移到比赛压力下；建议用比赛模拟模式做 A/B，并优先稳定性。`;
    } else {
      $('#pressureVerdict').textContent = `压力块与基础块接近：精度带 ${pct(pressure.accuracyRate)}，中位停速 ${Math.round(pressure.stopBandMedian)}ms。动作迁移稳定，可以再考虑更激进的参数 A/B。`;
    }
  }

  function buildDiagnosis(result, metrics) {
    const issues = [];
    const threshold = result.threshold;
    const gap = metrics.gapMedian;
    const overlap = metrics.overlapRate;
    const dead = metrics.deadTimeRate;
    const eff = metrics.efficiencyMedian;
    const overshoot = metrics.overshootRate;
    const accuracy = metrics.accuracyRate;

    if (metrics.earlyShotRate > 0.2) {
      issues.push({
        tag: '开枪顺序',
        tone: 'warn',
        title: `${pct(metrics.earlyShotRate)} 的枪发生在旧键松开之前`,
        text: '这是顺序错误，不是参数问题：先松开旧方向，再点反键，最后开枪。先把这个顺序固定，再看速度。'
      });
    }

    if (accuracy < 0.7) {
      issues.push({
        tag: '开枪时机',
        tone: 'warn',
        title: `${pct(metrics.accuracyRate)} 的枪在 34% 精度带外`,
        text: `中位开枪速度 ${Math.round(metrics.shotSpeedMedian)} u/s，阈值 ${Math.round(threshold)} u/s。先练“看到精度带再点击”，不要用更灵敏的 RT 掩盖早枪。`
      });
    } else if (accuracy >= 0.85) {
      issues.push({
        tag: '开枪时机',
        tone: 'good',
        title: `${pct(metrics.accuracyRate)} 的枪在精度带内`,
        text: `中位开枪速度 ${Math.round(metrics.shotSpeedMedian)} u/s，说明你基本能在速度降下来后开枪。下一步看停速和过冲。`
      });
    }

    if (eff < 75) {
      if (overlap > 0.25) {
        issues.push({
          tag: '松键时序',
          tone: 'warn',
          title: `${pct(overlap)} 的切换出现双键重叠`,
          text: `中位间隔 ${signed(gap)}。重叠期间两键同时按住，移动输入为 0，只剩摩擦，反向加速没有生效。先练先松后按；RT 抬起可以试 0.20–0.25mm（手指稳再往 0.15 探）。`
        });
      } else if (dead > 0.25) {
        issues.push({
          tag: '松键时序',
          tone: 'warn',
          title: `${pct(dead)} 的切换有 >35ms 空档`,
          text: `中位间隔 ${signed(gap)}。旧键松开后反键按得晚，损失了反向加速。先让“松旧键→点反键”成为一个动作；RT 按下 0.20–0.25mm 可让反键更早被读到。`
        });
      } else if (metrics.underTapRate > 0.3) {
        issues.push({
          tag: '反键时长',
          tone: 'warn',
          title: `${pct(metrics.underTapRate)} 的反键 tap 偏短`,
          text: `中位 tap ${metrics.tapMedian ? Math.round(metrics.tapMedian) : '—'}ms，速度还没进入精度带就松开了反键。先把反键按住到速度归零，再考虑更灵敏的参数。`
        });
      } else if (overshoot > 0.3) {
        issues.push({
          tag: '反键过冲',
          tone: 'warn',
          title: `${pct(overshoot)} 的切换出现反向过冲`,
          text: `中位过冲 ${Math.round(metrics.overshootMedian)} u/s。反键松得偏晚，速度已经反向。先练在 0 点松反键；如果动作稳定仍过冲，把 RT 抬起往大调（+0.05–0.10mm），不要往 0.1mm 挤。`
        });
      } else {
        issues.push({
          tag: '制动效率',
          tone: 'warn',
          title: `制动效率 ${Math.round(eff)}%`,
          text: `停到 34% 的中位时间是 ${Math.round(metrics.stopBandMedian)}ms，理论约 ${Math.round(metrics.idealBandMedian)}ms。主要损失不在开枪，而在速度归零过程。`
        });
      }
    } else {
      issues.push({
        tag: '制动效率',
        tone: 'good',
        title: `制动效率 ${Math.round(eff)}%`,
        text: `中位 ${Math.round(metrics.stopBandMedian)}ms 进入精度带，理论约 ${Math.round(metrics.idealBandMedian)}ms。你的松键+反键结构已经比较接近物理最优。`
      });
    }

    if (metrics.shotLateMedian > 80) {
      issues.push({
        tag: '开枪偏晚',
        tone: 'warn',
        title: `进入精度带后等待 ${Math.round(metrics.shotLateMedian)}ms 才开枪`,
        text: '停速本身没问题，但开枪决策偏慢。先练在精度带入口开第一枪；这项不是键盘参数能解决的。'
      });
    } else if (metrics.accuracyRate >= 0.8 && metrics.shotLateMedian > 0 && metrics.shotLateMedian <= 50) {
      issues.push({
        tag: '开枪时机',
        tone: 'good',
        title: `精度带内开枪延迟 ${Math.round(metrics.shotLateMedian)}ms`,
        text: '开枪时机很干净，没有明显早枪或等待。保持这个节奏，再去动参数。'
      });
    }

    if (metrics.consistencyCv > 0.3) {
      issues.push({
        tag: '一致性',
        tone: 'warn',
        title: `开枪速度波动 ${Math.round(metrics.consistencyCv * 100)}%`,
        text: '同一套动作的停速差异较大。先固定握姿、手位和反键 tap 时长；参数 A/B 要在动作稳定后再做。'
      });
    }

    if (metrics.reactionMedian > 270) {
      issues.push({
        tag: '反应',
        tone: 'warn',
        title: `提示→松键 ${Math.round(metrics.reactionMedian)}ms`,
        text: '反应偏慢会吃掉急停窗口。先练看到目标立刻松旧键；这是决策速度，不是触发行程能解决的。'
      });
    }

    if (!issues.length) {
      issues.push({
        tag: '结构',
        tone: 'good',
        title: '没有明显的结构性错误',
        text: '精度带、停速和过冲都在合理范围。下一步只改一个参数做 A/B，不要同时动触发、死区和 RT。'
      });
    }

    return issues.slice(0, 6);
  }

  function blockDrift(result) {
    const samples = (result.samples || []).filter((sample) => !sample.noRelease);
    if (samples.length < 10) return null;
    const first = samples.slice(0, 5);
    const last = samples.slice(-5);
    const firstMetrics = computeMetrics(first, result.threshold);
    const lastMetrics = computeMetrics(last, result.threshold);
    return {
      first: firstMetrics,
      last: lastMetrics,
      accuracyDelta: lastMetrics.accuracyRate - firstMetrics.accuracyRate,
      stopDelta: lastMetrics.stopBandMedian - firstMetrics.stopBandMedian,
      overshootDelta: lastMetrics.overshootRate - firstMetrics.overshootRate
    };
  }

  function renderDiagnosis(result, metrics) {
    const issues = buildDiagnosis(result, metrics);
    const drift = blockDrift(result);
    if (drift && (drift.accuracyDelta < -0.15 || drift.stopDelta > 15 || drift.overshootDelta > 0.15)) {
      issues.push({
        tag: '前后段漂移',
        tone: 'warn',
        title: `后 5 组比前 5 组${drift.accuracyDelta < -0.1 ? `精度下降 ${pct(Math.abs(drift.accuracyDelta))}` : '更不稳定'}`,
        text: `前 5 组精度 ${pct(drift.first.accuracyRate)} / 停速 ${Math.round(drift.first.stopBandMedian)}ms，后 5 组精度 ${pct(drift.last.accuracyRate)} / 停速 ${Math.round(drift.last.stopBandMedian)}ms。可能是热身不足、疲劳或注意力下降；正式对比前先休息/重测。`
      });
    } else if (drift && drift.accuracyDelta > 0.15 && drift.stopDelta < -8) {
      issues.push({
        tag: '热身效应',
        tone: 'good',
        title: '后 5 组明显好于前 5 组',
        text: `精度从 ${pct(drift.first.accuracyRate)} 提升到 ${pct(drift.last.accuracyRate)}，停速下降 ${Math.round(Math.abs(drift.stopDelta))}ms。你的手可能需要更长热身；正式测试前先做 5 次练习。`
      });
    }
    if (issues.length < 4 && result.reps < 20) {
      issues.push({
        tag: '样本量',
        tone: 'info',
        title: `当前 ${result.reps} 次 · 快速模式`,
        text: '12 次足以看动作结构，但不足以稳定比较设置。正式 A/B 建议 20 次，并至少重复两次。'
      });
    }
    if (issues.length < 4 && result.mode === 'blind') {
      issues.push({
        tag: '测试模式',
        tone: 'info',
        title: '盲测：没有实时速度辅助',
        text: `样本来自盲测，开枪判断更接近实战。${result.hardware?.rtOn ? '当前记录为 RT 开启状态。' : '当前记录为 RT 关闭状态。'}`
      });
    }
    if (issues.length < 4) {
      issues.push({
        tag: '下一步',
        tone: 'info',
        title: '先固定动作，再做 A/B',
        text: '只改一个参数，保持相同武器、次数和模式。把“更好”定义为精度带命中率、停速或过冲中的一项，而不是单次手感。'
      });
    }
    $('#diagnosisList').innerHTML = issues.map((item) => `
      <article class="insight-card ${item.tone || ''}">
        <span class="insight-tag">${item.tag}</span>
        <h4>${item.title}</h4>
        <p>${item.text}</p>
      </article>
    `).join('');
  }

  function recommendSettings(result, metrics) {
    const hw = result.hardware || DEFAULT_HARDWARE;
    const selfRisk = norm(Number(hw.misTouch), 1, 5);
    const styleRisk = { light: 0.18, balanced: 0.48, heavy: 0.72 }[hw.pressStyle] || 0.48;
    let risk = 0.42 * selfRisk + 0.18 * (hw.fingersRest ? 1 : 0) + 0.22 * styleRisk;
    if (Number(hw.top) < 0.1) risk += 0.1;
    risk = clamp(risk, 0, 1);

    // 证据基线：WASD 触发 0.4–0.7mm（新手从 0.7 起），RT 释放 0.2–0.5mm。
    // 来源：Wooting CS2 官方基线（0.7 / 0.5，连续 RT 关）与社区共识；
    // 职业快照：ZywOo 1.2/0.2、ropz 1.2/0.1、donk 1.5/0.4、NiKo 1.2/0.3（触发/RT，mm）。
    // 0.1mm 级触发/释放是抖动重灾区（传感器噪声、误触），不要当作目标值。
    let actuation = 0.70;
    actuation += (risk - 0.4) * 0.50;
    if (metrics.gapMedian > 35) actuation -= 0.10;
    if (metrics.overlapRate > 0.25) actuation += 0.10;
    if (metrics.underTapRate > 0.30) actuation -= 0.05;
    if (metrics.accuracyRate < 0.65) actuation += 0.05;
    actuation = clamp(roundTo(actuation, 0.05), 0.40, 1.20);

    const top = clamp(roundTo(0.10 + risk * 0.15, 0.05), 0.10, 0.30);
    const bottom = clamp(roundTo(0.30 + (metrics.overshootRate > 0.25 ? 0.05 : 0), 0.05), 0.25, 0.45);

    let rtPress = 0.25;
    rtPress += (metrics.gapMedian > 35 ? -0.03 : 0);
    rtPress += risk * 0.05;
    rtPress = clamp(roundTo(rtPress, 0.01), 0.15, 0.35);

    // 释放距离：重叠/过冲高 → 加大释放距离防抖（社区经验：停不稳先 +0.1mm），
    // 而不是一味收紧。0.2mm 是多数人的下限，0.1mm 只适合手指极稳的玩家。
    let rtRelease = 0.30;
    rtRelease += (metrics.overlapRate > 0.25 ? 0.05 : 0);
    rtRelease += (metrics.overshootRate > 0.25 ? 0.05 : 0);
    rtRelease += risk * 0.10;
    rtRelease = clamp(roundTo(rtRelease, 0.05), 0.20, 0.50);

    const control = clamp(
      0.45 * metrics.accuracyRate +
      0.25 * clamp(metrics.efficiencyMedian / 100, 0, 1) +
      0.15 * (1 - clamp(metrics.overshootRate, 0, 1)) +
      0.15 * (1 - clamp(metrics.consistencyCv, 0, 1)),
      0, 1
    );
    // 连续 RT 默认不建议开启（Wooting 官方同样不作为默认推荐）：
    // 只有控制分很高且误触风险很低时才开。
    const continuous = control > 0.70 && risk < 0.50 ? '开启' : '关闭';
    return { actuation, top, bottom, rtPress, rtRelease, continuous, risk, control };
  }

  function renderRecommendation(result, metrics) {
    const rec = recommendSettings(result, metrics);
    const dominant = metrics.overlapRate > 0.25
      ? '重叠/旧键释放'
      : metrics.deadTimeRate > 0.25
        ? '松键→反键空档'
        : metrics.overshootRate > 0.3
          ? '反键过冲'
          : metrics.accuracyRate < 0.7
            ? '开枪时机'
            : '制动效率';
    $('#settingsIntro').innerHTML = `这是一套基于当前样本与硬件画像的起点，不是唯一答案。当前主要限制：<b style="color:var(--lime)">${dominant}</b>。建议先改一个与限制直接相关的参数，再做同样 ${result.reps} 次的 A/B。`;

    const cards = [
      { label: '触发行程', value: rec.actuation.toFixed(2), unit: 'mm', sub: '先保证旧键释放/反键触发', primary: true },
      { label: '顶部死区', value: rec.top.toFixed(2), unit: 'mm', sub: `防误触画像风险 ${Math.round(rec.risk * 100)}/100` },
      { label: '底部死区', value: rec.bottom.toFixed(2), unit: 'mm', sub: '到底过冲与稳定性' },
      { label: 'RT 按下', value: rec.rtPress.toFixed(2), unit: 'mm', sub: '反键更早进入游戏' },
      { label: 'RT 抬起', value: rec.rtRelease.toFixed(2), unit: 'mm', sub: '旧键/反键更早复位' },
      { label: '连续 RT', value: rec.continuous, sub: '根据控制与误触画像决定' }
    ];
    $('#recommendGrid').innerHTML = cards.map((card) => `
      <div class="recommend-card ${card.primary ? 'primary' : ''}">
        <span>${card.label}</span>
        <b>${card.value}${card.unit ? `<small>${card.unit}</small>` : ''}</b>
        <p>${card.sub}</p>
      </div>
    `).join('');
    const proRef = $('#proRef');
    if (proRef) {
      proRef.innerHTML = '职业参考（快照，非标准）：ZywOo 1.2 / 0.2 · ropz 1.2 / 0.1 · donk 1.5 / 0.4 · NiKo 1.2 / 0.3（触发 / RT，mm）。'
        + 'Wooting CS2 新手基线：WASD 0.7 / RT 0.5，连续 RT 关，RT 只开 WASD（拆包键 E 必须关 RT）。'
        + '理论锚点（AK-47，1ms 模型）：立即反键约 75ms 进精度带，松键等待约 207ms；真实服务器为 64 tick，急停落在下一个 tick，浏览器会有数毫秒输入抖动。';
    }
  }

  function renderABComparison() {
    const panel = $('#abPanel');
    if (!panel) return;
    if (!state.baseline || !state.ab) {
      panel.classList.add('hidden');
      return;
    }
    panel.classList.remove('hidden');
    const base = computeMetrics(state.baseline.samples, state.baseline.threshold);
    const ab = computeMetrics(state.ab.samples, state.ab.threshold);
    const basePressure = computePressureMetrics(state.baseline);
    const abPressure = computePressureMetrics(state.ab);
    $('#abNote').textContent = `${state.baseline.label} vs ${state.ab.label} · 相同武器 / 次数 / 模式${basePressure && abPressure ? ' / 含比赛模拟' : ''}`;
    const items = [
      {
        label: '精度带命中率',
        before: pct(base.accuracyRate),
        after: pct(ab.accuracyRate),
        delta: (ab.accuracyRate - base.accuracyRate) * 100,
        unit: 'pp',
        goodUp: true
      },
      {
        label: '中位开枪速度',
        before: `${Math.round(base.shotSpeedMedian)} u/s`,
        after: `${Math.round(ab.shotSpeedMedian)} u/s`,
        delta: ab.shotSpeedMedian - base.shotSpeedMedian,
        unit: 'u/s',
        goodUp: false
      },
      {
        label: '停到 34%',
        before: `${Math.round(base.stopBandMedian)} ms`,
        after: `${Math.round(ab.stopBandMedian)} ms`,
        delta: ab.stopBandMedian - base.stopBandMedian,
        unit: 'ms',
        goodUp: false
      },
      {
        label: '制动效率',
        before: `${Math.round(base.efficiencyMedian)}%`,
        after: `${Math.round(ab.efficiencyMedian)}%`,
        delta: ab.efficiencyMedian - base.efficiencyMedian,
        unit: '%',
        goodUp: true
      },
      {
        label: '重叠率',
        before: pct(base.overlapRate),
        after: pct(ab.overlapRate),
        delta: (ab.overlapRate - base.overlapRate) * 100,
        unit: 'pp',
        goodUp: false
      },
      {
        label: '过冲率',
        before: pct(base.overshootRate),
        after: pct(ab.overshootRate),
        delta: (ab.overshootRate - base.overshootRate) * 100,
        unit: 'pp',
        goodUp: false
      }
    ];
    if (basePressure && abPressure) {
      items.push({
        label: '压力精度',
        before: pct(basePressure.accuracyRate),
        after: pct(abPressure.accuracyRate),
        delta: (abPressure.accuracyRate - basePressure.accuracyRate) * 100,
        unit: 'pp',
        goodUp: true
      });
      items.push({
        label: '压力超时率',
        before: pct(basePressure.timeoutRate),
        after: pct(abPressure.timeoutRate),
        delta: (abPressure.timeoutRate - basePressure.timeoutRate) * 100,
        unit: 'pp',
        goodUp: false
      });
    }
    $('#abGrid').innerHTML = items.map((item) => {
      const rounded = Math.round(item.delta);
      const good = item.goodUp ? rounded > 0 : rounded < 0;
      const cls = rounded === 0 ? '' : good ? 'good' : 'bad';
      return `
        <div class="ab-card">
          <span>${item.label}</span>
          <div class="ab-values"><em>${item.before}</em><b>→ ${item.after}</b></div>
          <div class="ab-delta ${cls}">${rounded > 0 ? '+' : ''}${rounded}${item.unit}</div>
        </div>
      `;
    }).join('');
  }

  /* ------------------------------------------------------------
     Export / copy
  ------------------------------------------------------------ */

  function buildReportText(result, metrics) {
    const rec = recommendSettings(result, metrics);
    const lines = [
      'MAGLAB PRO · CS2 急停报告',
      `日期：${dateLabel(result.finishedAt)}`,
      `配置：${result.label}`,
      `武器：${result.weaponName} · 最大速度 ${result.maxSpeed} u/s · 34% 精度带 ≤ ${Math.round(result.threshold)} u/s`,
      `样本：${metrics.validCount} 有效 / ${result.invalid} 次无效重试 / ${metrics.invalidCount} 次未松键 · 模式：${result.mode === 'blind' ? '盲测' : '训练'}`,
      '',
      '【核心指标】',
      `精度带命中率：${pct(metrics.accuracyRate)}`,
      `中位开枪速度：${Math.round(metrics.shotSpeedMedian)} u/s（P90 ${Math.round(metrics.shotSpeedP90)}）`,
      `停到 34% 中位：${Math.round(metrics.stopBandMedian)} ms（理论 ${Math.round(metrics.idealBandMedian)} ms）`,
      `制动效率中位：${Math.round(metrics.efficiencyMedian)}%`,
      `松键→反键中位：${signed(metrics.gapMedian)}（重叠 ${pct(metrics.overlapRate)} / 空档 ${pct(metrics.deadTimeRate)}）`,
      `反键按住中位：${metrics.tapMedian ? Math.round(metrics.tapMedian) : '—'} ms · 过冲率 ${pct(metrics.overshootRate)}`,
      `提示→松键：${Math.round(metrics.reactionMedian)} ms · 提示→开枪：${Math.round(metrics.shotReactionMedian)} ms`,
      `开枪速度波动：${Math.round(metrics.consistencyCv * 100)}%`,
      `内部评分：${Math.round(metrics.score)} / 100（${metrics.grade}）`,
      ...(computePressureMetrics(result) ? [
        '',
        '【比赛模拟】',
        `精度带命中率：${pct(computePressureMetrics(result).accuracyRate)}（基础 ${pct(metrics.accuracyRate)}）`,
        `中位停速：${Math.round(computePressureMetrics(result).stopBandMedian)} ms`,
        `中位开枪速度：${Math.round(computePressureMetrics(result).shotSpeedMedian)} u/s`,
        `窗口超时率：${pct(computePressureMetrics(result).timeoutRate)}（${result.pressureWindow || 900}ms 窗口）`
      ] : []),
      '',
      '【参数起点】',
      `触发行程：${rec.actuation.toFixed(2)} mm`,
      `顶部死区：${rec.top.toFixed(2)} mm`,
      `底部死区：${rec.bottom.toFixed(2)} mm`,
      `RT：${result.hardware.rtOn ? '开启' : '关闭'} · 按下 ${rec.rtPress.toFixed(2)} mm / 抬起 ${rec.rtRelease.toFixed(2)} mm`,
      `连续 RT：${rec.continuous}`,
      '',
      '【技术诊断】',
      ...buildDiagnosis(result, metrics).map((item) => `- ${item.title}：${item.text}`),
      '',
      '【使用说明】',
      '参数只影响输入被游戏读到的时刻，不能替代松键、反键和开枪时机。先固定技术，再用相同条件做 A/B。',
      '本工具是受控物理模型，不是游戏内遥测；最终用 Counter-strafe Master（3667395967）或 aim_botz 复验。'
    ];
    return lines.join('\n');
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (error) {
      try {
        const area = document.createElement('textarea');
        area.value = text;
        area.style.position = 'fixed';
        area.style.opacity = '0';
        document.body.appendChild(area);
        area.select();
        const ok = document.execCommand('copy');
        area.remove();
        return ok;
      } catch (fallbackError) {
        return false;
      }
    }
  }

  function downloadText(filename, text, mime = 'text/plain;charset=utf-8') {
    const blob = new Blob([text], { type: mime });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1500);
  }

  function exportCsv(result) {
    const header = [
      'block', 'rep', 'timeout', 'dir', 'oldUp_ms', 'newDown_ms', 'newUp_ms', 'shotAt_ms', 'gap_ms', 'tap_ms',
      'shotSpeed_u_s', 'accurate', 'stop34_ms', 'efficiency_pct', 'overshoot_u_s', 'shotLate_ms', 'score'
    ];
    const allSamples = [
      ...result.samples.map((sample, index) => ({ ...sample, block: 'dry', index: index + 1 })),
      ...((result.pressureSamples || []).map((sample, index) => ({ ...sample, block: 'pressure', index: index + 1 })))
    ];
    const rows = allSamples.map((sample) => [
      sample.block,
      sample.index,
      sample.timeout ? '1' : '0',
      sample.dir,
      sample.oldUp !== null && sample.oldUp !== undefined ? sample.oldUp.toFixed(1) : '',
      sample.newDown !== null && sample.newDown !== undefined ? sample.newDown.toFixed(1) : '',
      sample.newUp !== null && sample.newUp !== undefined ? sample.newUp.toFixed(1) : '',
      sample.shotAt !== null && sample.shotAt !== undefined ? sample.shotAt.toFixed(1) : '',
      sample.gap !== null && sample.gap !== undefined ? sample.gap.toFixed(1) : '',
      sample.tap !== null && sample.tap !== undefined ? sample.tap.toFixed(1) : '',
      sample.shotSpeed !== null && sample.shotSpeed !== undefined ? sample.shotSpeed.toFixed(1) : '',
      sample.accurate ? '1' : '0',
      sample.bandAt !== null && sample.bandAt !== undefined ? sample.bandAt.toFixed(1) : '',
      Number.isFinite(sample.efficiency) ? sample.efficiency.toFixed(1) : '',
      Number.isFinite(sample.overshoot) ? sample.overshoot.toFixed(1) : '',
      sample.shotLate !== null && sample.shotLate !== undefined ? sample.shotLate.toFixed(1) : '',
      Number.isFinite(sample.score) ? sample.score.toFixed(1) : '0'
    ]);
    const csv = '\uFEFF' + [header, ...rows].map((row) => row.join(',')).join('\r\n');
    const stamp = new Date(result.finishedAt || Date.now()).toISOString().slice(0, 10);
    downloadText(`MAGLAB-PRO-${stamp}.csv`, csv, 'text/csv;charset=utf-8');
  }

  function baselineDateLabel(timestamp) {
    const d = new Date(timestamp || Date.now());
    const y = d.getFullYear();
    const m = String(d.getMonth() + 1).padStart(2, '0');
    const day = String(d.getDate()).padStart(2, '0');
    return `${y}-${m}-${day}`;
  }

  function exportBaseline(result) {
    if (!result) {
      toast('还没有实验室基准');
      return;
    }
    const metrics = computeMetrics(result.samples, result.threshold);
    const payload = {
      kind: 'maglab-counterstrafe-baseline',
      version: 1,
      recordedAt: baselineDateLabel(result.finishedAt),
      shotSpeedMedian: Math.round(metrics.shotSpeedMedian),
      stopBandMs: Math.round(metrics.stopBandMedian)
    };
    downloadText('maglab-baseline.json', JSON.stringify(payload, null, 2), 'application/json;charset=utf-8');
    toast('实验室基准已导出');
  }

  /* ------------------------------------------------------------
     Bindings
  ------------------------------------------------------------ */

  function bindTestControls() {
    $('#abortRunBtn')?.addEventListener('click', () => {
      if (!run) return;
      if (window.confirm('退出本次测试？当前数据不会保存。')) {
        abortRun(false);
        showScreen('setup', { force: true });
      }
    });

    $('#testOverlayActions')?.addEventListener('click', (event) => {
      const button = event.target.closest('[data-test-action]');
      if (!button) return;
      handleTestOverlayAction(button.dataset.testAction);
    });
  }

  function bindReportControls() {
    $('#reportRoleRow')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-role]');
      if (!button || button.disabled) return;
      reportRole = button.dataset.role;
      if (!state[reportRole]) {
        toast('还没有这套配置的报告');
        return;
      }
      renderReport();
    });

    $('#traceSelect')?.addEventListener('click', (event) => {
      const button = event.target.closest('button[data-sample]');
      if (!button) return;
      traceSampleMode = button.dataset.sample;
      $$('#traceSelect button').forEach((item) => item.classList.toggle('is-selected', item === button));
      const result = state[reportRole];
      if (result) {
        const metrics = computeMetrics(result.samples, result.threshold);
        renderReportTrace(result, metrics);
      }
    });

    $('#reportBackBtn')?.addEventListener('click', () => showScreen('setup', { force: true }));

    $('#startABBtn')?.addEventListener('click', prepareAB);
    $('#startABBtnBottom')?.addEventListener('click', prepareAB);
    $('#newBaselineBtn')?.addEventListener('click', () => {
      setPendingRole('baseline');
      showScreen('setup', { force: true });
      toast('下一次测试将作为新基准');
    });

    $('#copyReportBtn')?.addEventListener('click', async () => {
      const result = state[reportRole];
      if (!result) return;
      const metrics = computeMetrics(result.samples, result.threshold);
      const ok = await copyText(buildReportText(result, metrics));
      toast(ok ? '报告摘要已复制' : '复制失败，请手动选择文本');
    });

    $('#downloadCsvBtn')?.addEventListener('click', () => {
      const result = state[reportRole];
      if (!result) return;
      exportCsv(result);
      toast('CSV 已导出');
    });

    $('#exportBaselineBtn')?.addEventListener('click', () => {
      const result = state[reportRole];
      exportBaseline(result);
    });

    $('#printReportBtn')?.addEventListener('click', () => window.print());
  }

  function prepareAB() {
    if (!state.baseline) {
      toast('先完成一次基准测试');
      return;
    }
    setPendingRole('ab');
    showScreen('setup', { force: true });
    toast('已切换到 A/B 第二组：只改一个参数');
  }

  function bindGlobal() {
    document.addEventListener('click', (event) => {
      const target = event.target.closest('[data-screen]');
      if (!target) return;
      event.preventDefault();
      showScreen(target.dataset.screen);
    });

    $('#demoBtn')?.addEventListener('click', loadDemo);
    $('#exitDemo')?.addEventListener('click', exitDemo);

    $('#soundToggle')?.addEventListener('click', () => {
      state.sound = !state.sound;
      $('#soundToggle').textContent = state.sound ? '声音 开' : '声音 关';
      saveState();
      if (state.sound) beep(720, 0.08, 0.03);
      toast(state.sound ? '提示音已开启' : '提示音已关闭');
    });

    $('#resetBtn')?.addEventListener('click', () => {
      if (!window.confirm('清空所有测试数据和报告？此操作不可撤销。')) return;
      try {
        localStorage.removeItem(STORAGE_KEY);
      } catch (error) {
        /* ignore */
      }
      if (activeSession) activeSession.abort();
      activeSession = null;
      run = null;
      rep = null;
      repPhase = 'idle';
      demoMode = false;
      demoBackup = null;
      state = freshState();
      state.settings.label = labelFromHardware(state.settings.hardware);
      pendingRole = 'baseline';
      reportRole = 'baseline';
      preLockSettings = null;
      $('#soundToggle').textContent = '声音 开';
      showScreen('setup', { force: true });
      syncSetupUI();
      toast('已清空数据');
    });

    window.addEventListener('keydown', (event) => {
      if (!event.repeat) pressed.add(event.code);
    });
    window.addEventListener('keyup', (event) => {
      pressed.delete(event.code);
    });
    window.addEventListener('blur', () => pressed.clear());

    document.addEventListener('visibilitychange', () => {
      if (document.hidden && run && !run.finished) {
        abortRun(false);
        showScreen('setup', { force: true });
        toast('检测到切换窗口，本次测试已取消');
      }
    });
  }

  /* ------------------------------------------------------------
     Demo (sample report)
  ------------------------------------------------------------ */

  function buildDemoResult() {
    const weapon = WEAPONS.ak47;
    const threshold = weapon.speed * ACCURACY_BAND;
    const samples = [];
    const gaps = [12, -18, 24, 8, -26, 16, 31, -12, 18, 6, -22, 14, 35, -8, 20, 10, -30, 26, 4, 16];
    const reactions = [188, 201, 176, 214, 195, 182, 223, 190, 205, 178, 198, 216, 184, 207, 192, 174, 220, 199, 186, 203];
    for (let i = 0; i < 20; i += 1) {
      const dir = i % 2 === 0 ? 'A' : 'D';
      const oldUp = reactions[i];
      const gap = gaps[i];
      const newDown = oldUp + gap;
      const tapLength = 150 + (i % 5) * 12 + (i % 3 === 0 ? 18 : 0);
      const newUp = newDown + tapLength;
      const shotAt = oldUp + 138 + (i % 7) * 8 + (i % 4 === 0 ? 42 : 0);
      const sample = simulateRep({ dir, oldUp, newDown, newUp, shotAt }, weapon.speed, threshold);
      sample.index = i + 1;
      samples.push(sample);
    }

    const pressureSamples = [];
    const pGaps = [18, 26, -12, 32, 20, -18, 24];
    for (let i = 0; i < 7; i += 1) {
      const dir = i % 2 === 0 ? 'A' : 'D';
      const oldUp = 196 + (i % 3) * 12 + (i % 4 === 0 ? 18 : 0);
      const newDown = oldUp + pGaps[i];
      const newUp = newDown + 132 + (i % 3) * 14;
      const shotAt = oldUp + 104 + (i % 4) * 10 + (i % 3 === 0 ? 28 : 0);
      const sample = simulateRep({ dir, oldUp, newDown, newUp, shotAt }, weapon.speed, threshold);
      sample.index = i + 1;
      sample.pressure = true;
      pressureSamples.push(sample);
    }
    pressureSamples.push({
      index: 8,
      dir: 'D',
      dirSign: 1,
      pressure: true,
      timeout: true,
      noRelease: true,
      accurate: false,
      shotAt: null,
      oldUp: null,
      newDown: null,
      newUp: null,
      shotSpeed: null,
      bandAt: null,
      stopAt: null,
      idealBand: null,
      idealStop: null,
      efficiency: 0,
      gap: null,
      tap: null,
      overshoot: 0,
      shotLate: null,
      shotEarly: null,
      score: 0,
      trace: []
    });

    return {
      role: 'baseline',
      weapon: weapon.id,
      weaponName: weapon.name,
      maxSpeed: weapon.speed,
      threshold,
      reps: 20,
      mode: 'blind',
      pressure: true,
      pressureReps: 8,
      pressureWindow: 900,
      label: '示例 · 0.70mm / RT 0.30',
      hardware: { ...DEFAULT_HARDWARE },
      samples,
      pressureSamples,
      invalid: 1,
      attempts: 21,
      startedAt: Date.now() - 90000,
      finishedAt: Date.now() - 1000,
      duration: 89,
      score: computeMetrics(samples, threshold).score,
      grade: gradeForScore(computeMetrics(samples, threshold).score)
    };
  }

  function loadDemo() {
    if (demoMode) return;
    demoBackup = state;
    demoMode = true;
    state = freshState();
    state.baseline = buildDemoResult();
    state.ab = null;
    reportRole = 'baseline';
    pendingRole = 'baseline';
    showScreen('report', { force: true });
    toast('已载入示例报告，不会覆盖你的数据');
  }

  function exitDemo() {
    if (!demoMode) return;
    state = demoBackup || freshState();
    demoBackup = null;
    demoMode = false;
    reportRole = 'baseline';
    pendingRole = 'baseline';
    $('#demoBanner')?.classList.add('hidden');
    try {
      if (window.location.hash === '#demo') {
        history.replaceState(null, '', window.location.pathname + window.location.search);
      }
    } catch (error) {
      /* ignore */
    }
    showScreen('setup', { force: true });
    toast('已退出示例');
  }

  /* ------------------------------------------------------------
     Init
  ------------------------------------------------------------ */

  function init() {
    if (!state.settings.label) state.settings.label = labelFromHardware(state.settings.hardware);
    if (state.baseline) {
      state.settings.weapon = state.baseline.weapon;
      state.settings.reps = state.baseline.reps;
      state.settings.mode = state.baseline.mode;
      state.settings.pressure = Boolean(state.baseline.pressure);
      state.settings.label = state.baseline.label;
    }
    $('#soundToggle').textContent = state.sound ? '声音 开' : '声音 关';
    bindSetup();
    bindTestControls();
    bindReportControls();
    bindGlobal();
    syncSetupUI();
    updateTopConfig();
    updateProgress();

    const hash = window.location.hash.replace('#', '');
    if (hash === 'report' && state.baseline) {
      reportRole = 'baseline';
      showScreen('report', { force: true });
    }
    if (hash === 'demo') setTimeout(loadDemo, 0);

    window.MAGLAB_PRO = {
      version: '1.0',
      go: showScreen,
      getState: () => JSON.parse(JSON.stringify(state)),
      getRun: () => ({
        run: run ? { ...run, samples: run.samples.length, pressureSamples: run.pressureSamples.length } : null,
        rep: rep ? { ...rep } : null,
        repPhase,
        pressed: [...pressed],
        hasSession: Boolean(activeSession),
        hidden: document.hidden
      }),
      simulate: (raw, weaponId = 'ak47') => simulateRep(raw, WEAPONS[weaponId].speed, WEAPONS[weaponId].speed * ACCURACY_BAND),
      analyze: computeMetrics,
      demo: loadDemo
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
