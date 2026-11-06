/* ============================================================
   MAGLAB SENSOR ANGLE · 鼠标传感器旋转角度检测
   算法：PCA / 总体最小二乘（TLS）拟合轨迹主轴
        对水平与垂直滑动均有效，不再依赖 y-on-x 回归
   ============================================================ */

(function () {
  'use strict';

  // -------------------- 算法常量 --------------------
  const SEG_MIN_LEN = 80;     // 有效描边：首尾距离 ≥ 80px
  const SEG_MIN_POINTS = 15;  // 有效描边：采样点 ≥ 15
  const MIN_SEGMENTS = 10;    // 给推荐值所需的最少有效描边
  const MIN_R2 = 0.9;         // 可信度阈值

  // -------------------- 纯算法（可在 Node 中单测） --------------------

  function clamp01(v) {
    if (v === null || v === undefined || Number.isNaN(v)) return 0;
    return Math.min(1, Math.max(0, v));
  }

  // 把“相对理想方向的偏移”归一化到 (-90, 90]
  function normalizeOffset(deg) {
    while (deg > 90) deg -= 180;
    while (deg <= -90) deg += 180;
    return deg;
  }

  // PCA / 总体最小二乘直线拟合
  // 返回 {
  //   offsetDeg: 相对理想方向（水平 0° / 垂直 90°）的偏移角度
  //   r2:        主特征值占比，等价可信度（越接近 1 越直）
  //   mainDir:   'h' | 'v'
  //   angleDeg, angleRad, cx, cy
  // }
  function fitLine(pts) {
    if (!pts || pts.length < 2) return null;
    const n = pts.length;

    let sx = 0, sy = 0;
    for (let i = 0; i < n; i++) {
      sx += pts[i].x;
      sy += pts[i].y;
    }
    const cx = sx / n;
    const cy = sy / n;

    // 协方差矩阵分量
    let sxx = 0, syy = 0, sxy = 0;
    for (let i = 0; i < n; i++) {
      const dx = pts[i].x - cx;
      const dy = pts[i].y - cy;
      sxx += dx * dx;
      syy += dy * dy;
      sxy += dx * dy;
    }

    // 主特征向量方向：水平、垂直、斜线都适用
    const theta = 0.5 * Math.atan2(2 * sxy, sxx - syy);
    const angleDegRaw = (theta * 180) / Math.PI; // (-90, 90]

    // 主/次特征值 → 直线可信度（等价 R²）
    const tr = sxx + syy;
    const disc = Math.sqrt((sxx - syy) * (sxx - syy) + 4 * sxy * sxy);
    const l1 = (tr + disc) / 2;
    const l2 = (tr - disc) / 2;
    const r2 = l1 > 1e-12 ? clamp01(l1 / (l1 + l2)) : 0;

    // 主方向：主轴更接近水平还是垂直
    const mainDir = Math.abs(Math.cos(theta)) >= Math.abs(Math.sin(theta)) ? 'h' : 'v';

    // 相对理想方向的偏移
    const idealDeg = mainDir === 'h' ? 0 : 90;
    const offsetDeg = normalizeOffset(angleDegRaw - idealDeg);

    return {
      offsetDeg,
      r2,
      mainDir,
      angleDeg: angleDegRaw,
      angleRad: theta,
      cx,
      cy
    };
  }

  // 建议填入驱动的角度 = 偏移的反方向
  function computeRecommend(offsetDeg) {
    return -offsetDeg;
  }

  // 一段（一次按住-松开）是否有效
  function isSegmentValid(pts) {
    if (!pts || pts.length < SEG_MIN_POINTS) return false;
    const a = pts[0];
    const b = pts[pts.length - 1];
    return Math.hypot(b.x - a.x, b.y - a.y) >= SEG_MIN_LEN;
  }

  // 单段统计：偏移、可信度、长度、左右方向
  function segmentStats(pts) {
    if (!isSegmentValid(pts)) return null;
    const fit = fitLine(pts);
    if (!fit) return null;
    const a = pts[0];
    const b = pts[pts.length - 1];
    const dx = b.x - a.x;
    const dy = b.y - a.y;
    return {
      offsetDeg: fit.offsetDeg,
      r2: fit.r2,
      mainDir: fit.mainDir,
      len: Math.hypot(dx, dy),
      dir: dx >= 0 ? 'ltr' : 'rtl',
      cx: fit.cx,
      cy: fit.cy
    };
  }

  // 汇总多段：按长度加权，分别统计左→右 / 右→左
  function analyzeSegments(segments) {
    const list = segments || [];
    const stats = [];
    list.forEach((seg) => {
      const pts = Array.isArray(seg) ? seg : (seg && seg.points);
      const s = segmentStats(pts);
      if (s) stats.push(s);
    });

    if (!stats.length) {
      return {
        totalCount: list.length,
        validCount: 0,
        offsetDeg: 0,
        r2: 0,
        mainDir: null,
        cx: null,
        cy: null,
        ltr: null,
        rtl: null,
        diff: null
      };
    }

    let w = 0, off = 0, r2 = 0, cx = 0, cy = 0;
    let hLen = 0, vLen = 0;
    let ltrW = 0, ltrOff = 0, ltrN = 0;
    let rtlW = 0, rtlOff = 0, rtlN = 0;

    stats.forEach((s) => {
      w += s.len;
      off += s.offsetDeg * s.len;
      r2 += s.r2 * s.len;
      cx += s.cx * s.len;
      cy += s.cy * s.len;
      if (s.mainDir === 'h') {
        hLen += s.len;
        if (s.dir === 'ltr') {
          ltrW += s.len;
          ltrOff += s.offsetDeg * s.len;
          ltrN++;
        } else {
          rtlW += s.len;
          rtlOff += s.offsetDeg * s.len;
          rtlN++;
        }
      } else {
        vLen += s.len;
      }
    });

    const ltr = ltrN ? { count: ltrN, offsetDeg: ltrOff / ltrW } : null;
    const rtl = rtlN ? { count: rtlN, offsetDeg: rtlOff / rtlW } : null;

    return {
      totalCount: list.length,
      validCount: stats.length,
      offsetDeg: off / w,
      r2: r2 / w,
      mainDir: hLen >= vLen ? 'h' : 'v',
      cx: cx / w,
      cy: cy / w,
      ltr,
      rtl,
      diff: ltr && rtl ? ltr.offsetDeg - rtl.offsetDeg : null
    };
  }

  // 多轮平均：每轮 = 一次“停止并计算”
  function averageRounds(rounds) {
    const list = (rounds || []).filter((r) => r && Number.isFinite(r.offsetDeg));
    if (!list.length) return null;
    const offsetDeg = list.reduce((sum, r) => sum + r.offsetDeg, 0) / list.length;
    return {
      count: list.length,
      offsetDeg,
      recommendDeg: computeRecommend(offsetDeg)
    };
  }

  // Node 单测出口（浏览器里 module 不存在，跳过）
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
      fitLine,
      computeRecommend,
      isSegmentValid,
      segmentStats,
      analyzeSegments,
      averageRounds,
      SEG_MIN_LEN,
      SEG_MIN_POINTS,
      MIN_SEGMENTS,
      MIN_R2
    };
  }

  // 没有 DOM 的环境（Node 测试）到此为止，只导出纯算法
  if (typeof document === 'undefined') return;

  // -------------------- DOM 与交互 --------------------
  const $ = (selector, root = document) => root.querySelector(selector);
  const canvas = $('#sensorCanvas');
  const wrap = $('.canvas-wrap');
  if (!canvas || !wrap) return;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;

  const btnStart = $('#btnStart');
  const btnStop = $('#btnStop');
  const btnReset = $('#btnReset');
  const btnCopy = $('#btnCopy');
  const btnExport = $('#btnExport');
  const statusBadge = $('#statusBadge');
  const statePill = $('#statePill');
  const pointCount = $('#pointCount');
  const directionPill = $('#directionPill');
  const detectAngle = $('#detectAngle');
  const detectUnit = $('#detectUnit');
  const fitR2 = $('#fitR2');
  const resultRow = $('#sensorResultRow');
  const recommendAngle = $('#recommendAngle');
  const recommendSign = $('#recommendSign');
  const angleValue = $('#angleValue');
  const angleSignText = $('#angleSignText');
  const toastEl = $('#toast');

  // 新增 UI（每个都判空）
  const validSegCount = $('#validSegCount');
  const validSegHint = $('#validSegHint');
  const avgAngle = $('#avgAngle');
  const roundCount = $('#roundCount');
  const lrStats = $('#lrStats');
  const verdict = $('#verdict');

  const setText = (el, text) => {
    if (el) el.textContent = text;
  };

  const setDisplay = (el, value) => {
    if (el) el.style.display = value;
  };

  // -------------------- 状态 --------------------
  let recording = false;
  let points = [];          // 所有采样点，仅用于计数/导出
  let segments = [];        // [{points:[...]}]，一段 = 一次按住-松开
  let currentSegment = [];
  let rounds = [];          // [{offsetDeg, r2, validCount, mainDir, at}]
  let pendingResume = false; // 有效段不足时，允许“继续记录”而不清空
  let width = 0;
  let height = 0;

  function fmtSigned(v) {
    return (v >= 0 ? '+' : '') + v.toFixed(1) + '°';
  }

  // -------------------- 绘制 --------------------
  function resize() {
    const rect = wrap.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    width = Math.round(rect.width);
    height = Math.round(rect.height);
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    canvas.style.width = width + 'px';
    canvas.style.height = height + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    draw();
  }

  function draw() {
    ctx.clearRect(0, 0, width, height);

    // 网格
    ctx.strokeStyle = 'rgba(255,255,255,0.04)';
    ctx.lineWidth = 1;
    const step = 40;
    for (let x = step; x < width; x += step) {
      ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, height); ctx.stroke();
    }
    for (let y = step; y < height; y += step) {
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(width, y); ctx.stroke();
    }

    // 中心十字参考线
    ctx.strokeStyle = 'rgba(255,255,255,0.08)';
    ctx.setLineDash([6, 6]);
    ctx.beginPath(); ctx.moveTo(width / 2, 0); ctx.lineTo(width / 2, height); ctx.stroke();
    ctx.beginPath(); ctx.moveTo(0, height / 2); ctx.lineTo(width, height / 2); ctx.stroke();
    ctx.setLineDash([]);

    // 已完成轨迹段：绿色=有效，红色=无效
    segments.forEach((seg) => {
      if (seg.points.length < 2) return;
      const good = isSegmentValid(seg.points);
      ctx.strokeStyle = good ? 'rgba(215,255,63,0.45)' : 'rgba(255,82,104,0.3)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(seg.points[0].x, seg.points[0].y);
      for (let i = 1; i < seg.points.length; i++) ctx.lineTo(seg.points[i].x, seg.points[i].y);
      ctx.stroke();
      seg.points.forEach((p) => {
        ctx.fillStyle = good ? '#d7ff3f' : '#ff5268';
        ctx.beginPath(); ctx.arc(p.x, p.y, 2.2, 0, Math.PI * 2); ctx.fill();
      });
    });

    // 当前正在记录的段
    if (currentSegment.length > 0) {
      ctx.strokeStyle = '#48dfff';
      ctx.lineWidth = 2.5;
      ctx.beginPath();
      ctx.moveTo(currentSegment[0].x, currentSegment[0].y);
      for (let i = 1; i < currentSegment.length; i++) ctx.lineTo(currentSegment[i].x, currentSegment[i].y);
      ctx.stroke();
      currentSegment.forEach((p) => {
        ctx.fillStyle = '#48dfff';
        ctx.beginPath(); ctx.arc(p.x, p.y, 2.2, 0, Math.PI * 2); ctx.fill();
      });
    }

    // 平均轴 + 理想方向参考线
    const agg = analyzeSegments(segments);
    if (agg.validCount > 0 && agg.cx !== null) {
      const base = agg.mainDir === 'h' ? 0 : 90;
      const theta = ((base + agg.offsetDeg) * Math.PI) / 180;
      const len = Math.sqrt(width * width + height * height);
      const dx = Math.cos(theta) * len;
      const dy = Math.sin(theta) * len;

      ctx.strokeStyle = '#d7ff3f';
      ctx.lineWidth = 3;
      ctx.shadowColor = 'rgba(215,255,63,0.5)';
      ctx.shadowBlur = 12;
      ctx.beginPath();
      ctx.moveTo(agg.cx - dx, agg.cy - dy);
      ctx.lineTo(agg.cx + dx, agg.cy + dy);
      ctx.stroke();
      ctx.shadowBlur = 0;

      // 理想方向（水平或垂直）
      const iLen = Math.min(width, height) * 0.85;
      const idx = Math.cos(base * Math.PI / 180) * iLen;
      const idy = Math.sin(base * Math.PI / 180) * iLen;
      ctx.strokeStyle = 'rgba(255,255,255,0.12)';
      ctx.setLineDash([7, 7]);
      ctx.beginPath();
      ctx.moveTo(agg.cx - idx, agg.cy - idy);
      ctx.lineTo(agg.cx + idx, agg.cy + idy);
      ctx.stroke();
      ctx.setLineDash([]);

      // 角度标注
      ctx.fillStyle = '#d7ff3f';
      ctx.font = '600 12px ui-monospace, SFMono-Regular, Menlo, monospace';
      ctx.fillText(fmtSigned(agg.offsetDeg), agg.cx + 12, agg.cy - 12);
    }
  }

  function toast(message, ms = 2500) {
    if (!toastEl) return;
    toastEl.textContent = message;
    toastEl.classList.add('show');
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => toastEl.classList.remove('show'), ms);
  }

  // -------------------- 统计显示 --------------------
  function updateStats() {
    const agg = analyzeSegments(segments);
    setText(pointCount, String(points.length));
    setText(validSegCount, String(agg.validCount));

    const need = Math.max(0, MIN_SEGMENTS - agg.validCount);
    if (recording) {
      setText(validSegHint, need > 0
        ? '还需 ' + need + ' 段有效描边'
        : '段数已够，点击“停止并计算”');
    } else {
      setText(validSegHint, need > 0
        ? '至少 ' + MIN_SEGMENTS + ' 段才给推荐值'
        : '段数已够，可点击“停止并计算”');
    }

    updateDirection(agg);
    return agg;
  }

  function updateDirection(agg) {
    if (!agg.validCount) {
      setText(directionPill, '—');
      setText(lrStats, '—');
      return;
    }

    setText(directionPill, agg.mainDir === 'h' ? '水平滑动' : '垂直滑动');
    if (directionPill) {
      directionPill.style.color = agg.mainDir === 'h' ? 'var(--lime)' : 'var(--cyan)';
    }

    if (!lrStats) return;
    if (agg.ltr && agg.rtl) {
      lrStats.textContent =
        '左→右 ' + fmtSigned(agg.ltr.offsetDeg) + '（' + agg.ltr.count + ' 段） · ' +
        '右→左 ' + fmtSigned(agg.rtl.offsetDeg) + '（' + agg.rtl.count + ' 段） · ' +
        '差 ' + Math.abs(agg.diff).toFixed(1) + '°';
    } else if (agg.ltr || agg.rtl) {
      const one = agg.ltr || agg.rtl;
      lrStats.textContent =
        (agg.ltr ? '左→右 ' : '右→左 ') + fmtSigned(one.offsetDeg) +
        '（' + one.count + ' 段） · 缺少反向描边，无法对比';
    } else {
      lrStats.textContent = '垂直滑动为主，左右差异不适用';
    }
  }

  function updateRoundDisplay(avg) {
    if (!avg) {
      setText(avgAngle, '—');
      setText(roundCount, '已完成 0 轮');
      return;
    }
    setText(avgAngle, fmtSigned(avg.offsetDeg) + ' → ' + fmtSigned(avg.recommendDeg));
    setText(roundCount, '已完成 ' + avg.count + ' 轮');
  }

  // 数据不足：不给推荐值
  function showInsufficient(agg, reason) {
    setDisplay(resultRow, 'grid');
    setText(recommendAngle, '—');
    setText(recommendSign, '数据不足，请重测');
    setText(detectAngle, agg.validCount ? fmtSigned(agg.offsetDeg) : '—');
    setText(detectUnit, agg.validCount
      ? (agg.mainDir === 'h' ? '本轮相对水平方向偏移' : '本轮相对垂直方向偏移')
      : '—');
    setText(fitR2, agg.validCount ? agg.r2.toFixed(3) : '—');
    setText(angleValue, '—');
    setText(angleSignText, '—');
    if (verdict) {
      verdict.textContent = reason;
      verdict.classList.add('warn');
      verdict.classList.remove('ok');
    }
  }

  // 可信结果：推荐值取多轮平均
  function showResult(agg, avg) {
    setDisplay(resultRow, 'grid');
    setText(recommendAngle, fmtSigned(avg.recommendDeg));
    setText(detectAngle, fmtSigned(agg.offsetDeg));
    setText(detectUnit, agg.mainDir === 'h' ? '本轮相对水平方向偏移' : '本轮相对垂直方向偏移');
    setText(fitR2, agg.r2.toFixed(3));
    setText(angleValue, Math.abs(avg.recommendDeg).toFixed(1));
    setText(angleSignText, avg.recommendDeg >= 0 ? '正' : '负');

    if (Math.abs(avg.recommendDeg) < 0.5) {
      setText(recommendSign, '角度极小，近似无需补偿');
    } else {
      setText(recommendSign, avg.recommendDeg >= 0 ? '正值 = 顺时针' : '负值 = 逆时针');
    }

    if (verdict) {
      verdict.textContent = '可信：' + agg.validCount + ' 段有效描边 · R² ' + agg.r2.toFixed(2) + ' · 可填入驱动';
      verdict.classList.add('ok');
      verdict.classList.remove('warn');
    }
  }

  function recordRound(agg) {
    rounds.push({
      offsetDeg: agg.offsetDeg,
      r2: agg.r2,
      validCount: agg.validCount,
      mainDir: agg.mainDir,
      at: Date.now()
    });
    const avg = averageRounds(rounds);
    updateRoundDisplay(avg);
    return avg;
  }

  // -------------------- 记录流程 --------------------
  function startRecording() {
    const resumed = pendingResume;
    if (!resumed) {
      points = [];
      segments = [];
      updateRoundDisplay(averageRounds(rounds));
    }
    pendingResume = false;
    currentSegment = [];
    recording = true;

    if (btnStart) btnStart.disabled = true;
    if (btnStop) btnStop.disabled = false;
    setText(btnStart, '开始记录');
    setText(statusBadge, '记录中');
    if (statusBadge) statusBadge.classList.add('rec');
    setText(statePill, resumed ? '记录中 · 继续滑动' : '记录中 · 滑动鼠标');
    setDisplay(resultRow, 'none');

    const agg = updateStats();
    draw();
    toast(resumed
      ? '继续记录：再滑 ' + Math.max(0, MIN_SEGMENTS - agg.validCount) + ' 段有效描边'
      : '开始记录：按住左键，闭眼水平来回滑');
  }

  function finalizeCurrentSegment() {
    if (currentSegment.length >= 2) {
      segments.push({ points: currentSegment.slice() });
    }
    currentSegment = [];
    updateStats();
  }

  function stopRecording() {
    if (!recording) return;
    recording = false;
    finalizeCurrentSegment();

    if (btnStart) btnStart.disabled = false;
    if (btnStop) btnStop.disabled = true;
    setText(statusBadge, '已停止');
    if (statusBadge) statusBadge.classList.remove('rec');

    const agg = updateStats();

    // 有效段不足：不入轮次、不给推荐值，允许继续滑
    if (agg.validCount < MIN_SEGMENTS) {
      pendingResume = true;
      setText(btnStart, '继续记录');
      setText(validSegHint, '还需 ' + (MIN_SEGMENTS - agg.validCount) + ' 段有效描边');
      setText(statePill, '需继续滑动（' + agg.validCount + '/' + MIN_SEGMENTS + ' 段）');
      showInsufficient(agg, '数据不足，请重测：还需 ' + (MIN_SEGMENTS - agg.validCount) + ' 段有效描边');
      toast('有效描边不足（' + agg.validCount + '/' + MIN_SEGMENTS + '），请继续滑动', 3200);
      draw();
      return;
    }

    // 拟合太散：不给推荐值
    if (agg.r2 < MIN_R2) {
      pendingResume = false;
      setText(btnStart, '重测');
      setText(statePill, '轨迹不够直');
      showInsufficient(agg, '数据不足，请重测：轨迹不够直，保持更直的滑动');
      toast('拟合可信度不足（R² < 0.9），请重测', 3200);
      draw();
      return;
    }

    pendingResume = false;
    const avg = recordRound(agg);
    setText(btnStart, '开始记录');
    setText(validSegHint, '本轮已记录，可开始下一轮');
    setText(statePill, '本轮完成 · 共 ' + avg.count + ' 轮');
    showResult(agg, avg);
    draw();
    toast('本轮偏移 ' + fmtSigned(agg.offsetDeg) + '，多轮平均推荐 ' + fmtSigned(avg.recommendDeg));
  }

  function resetAll() {
    recording = false;
    pendingResume = false;
    points = [];
    segments = [];
    currentSegment = [];
    rounds = [];

    if (btnStart) btnStart.disabled = false;
    if (btnStop) btnStop.disabled = true;
    setText(btnStart, '开始记录');
    setText(statusBadge, '就绪');
    if (statusBadge) statusBadge.classList.remove('rec');
    setText(statePill, '等待开始');
    setDisplay(resultRow, 'none');
    setText(pointCount, '0');
    setText(validSegCount, '0');
    setText(validSegHint, '至少 ' + MIN_SEGMENTS + ' 段才给推荐值');
    setText(directionPill, '—');
    if (directionPill) directionPill.style.color = '';
    setText(lrStats, '—');
    setText(detectAngle, '0°');
    setText(fitR2, '—');
    setText(recommendAngle, '0°');
    setText(recommendSign, '正 = 顺时针 / 负 = 逆时针');
    setText(angleValue, '0');
    setText(angleSignText, '—');
    updateRoundDisplay(null);
    if (verdict) {
      verdict.textContent = '—';
      verdict.classList.remove('ok', 'warn');
    }
    draw();
    toast('已重置');
  }

  function copyAngle() {
    const v = recommendAngle ? recommendAngle.textContent : '';
    if (!v || v === '—') {
      toast('暂无推荐值，请先完成一轮有效测量');
      return;
    }
    navigator.clipboard.writeText(v).then(
      () => toast('已复制：' + v),
      () => toast('复制失败，请手动选中数值')
    );
  }

  function exportText() {
    const agg = analyzeSegments(segments);
    const avg = averageRounds(rounds);
    const lines = [];
    lines.push('MAGLAB 鼠标传感器旋转角度检测记录');
    lines.push('时间：' + new Date().toLocaleString('zh-CN'));
    lines.push('有效描边：' + agg.validCount + ' / ' + agg.totalCount + ' 段（≥80px 且 ≥15 点）');
    lines.push('本轮偏移角度：' + (agg.validCount ? agg.offsetDeg.toFixed(2) + '°' : '—'));
    lines.push('本轮拟合 R²：' + (agg.validCount ? agg.r2.toFixed(4) : '—'));
    lines.push('本轮主方向：' + (agg.validCount ? (agg.mainDir === 'h' ? '水平' : '垂直') : '—'));
    if (agg.ltr) lines.push('左→右平均偏移：' + agg.ltr.offsetDeg.toFixed(2) + '°（' + agg.ltr.count + ' 段）');
    if (agg.rtl) lines.push('右→左平均偏移：' + agg.rtl.offsetDeg.toFixed(2) + '°（' + agg.rtl.count + ' 段）');
    if (agg.diff !== null) lines.push('左右差异：' + agg.diff.toFixed(2) + '°');
    lines.push('多轮平均偏移：' + (avg ? avg.offsetDeg.toFixed(2) + '°（' + avg.count + ' 轮）' : '—'));
    lines.push('建议填入驱动角度：' + (avg ? avg.recommendDeg.toFixed(2) + '°' : '—'));
    lines.push('说明：各品牌驱动对正负号定义可能相反，如指针仍偏移请反转正负号。');

    const blob = new Blob([lines.join('\n')], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'maglab_sensor_angle.txt';
    a.click();
    URL.revokeObjectURL(url);
    toast('已导出记录文本');
  }

  // -------------------- 鼠标事件 --------------------
  function onPointerDown(e) {
    if (!recording) return;
    const rect = canvas.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    currentSegment = [{ x, y, t: performance.now() }];
    points.push(currentSegment[0]);
    setText(pointCount, String(points.length));
    draw();
  }

  function onPointerMove(e) {
    if (!recording || currentSegment.length === 0) return;
    const rect = canvas.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    // 采样降频：距离上一点超过 1.5px 才记录，避免过密
    const last = currentSegment[currentSegment.length - 1];
    if (Math.hypot(x - last.x, y - last.y) < 1.5) return;
    const pt = { x, y, t: performance.now() };
    currentSegment.push(pt);
    points.push(pt);
    setText(pointCount, String(points.length));
    draw();
  }

  function onPointerUp() {
    if (!recording) return;
    finalizeCurrentSegment();
    draw();
  }

  function init() {
    window.addEventListener('resize', resize);
    resize();

    canvas.addEventListener('mousedown', onPointerDown);
    canvas.addEventListener('mousemove', onPointerMove);
    window.addEventListener('mouseup', onPointerUp);
    // 离开画布也算松开，避免轨迹断裂未结束
    canvas.addEventListener('mouseleave', onPointerUp);

    if (btnStart) btnStart.addEventListener('click', startRecording);
    if (btnStop) btnStop.addEventListener('click', stopRecording);
    if (btnReset) btnReset.addEventListener('click', resetAll);
    if (btnCopy) btnCopy.addEventListener('click', copyAngle);
    if (btnExport) btnExport.addEventListener('click', exportText);

    const resetBtn = $('#resetBtn');
    if (resetBtn) {
      resetBtn.addEventListener('click', () => {
        if (confirm('重置会清空当前所有轨迹与结果，继续？')) resetAll();
      });
    }

    updateStats();
    updateRoundDisplay(null);
    draw();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
