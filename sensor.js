/* ============================================================
   MAGLAB SENSOR ANGLE · 鼠标传感器旋转角度检测
   算法：最小二乘法拟合轨迹直线，计算相对理想方向的偏移角度
   ============================================================ */

(function () {
  'use strict';

  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

  const canvas = $('#sensorCanvas');
  const ctx = canvas.getContext('2d');
  const wrap = $('.canvas-wrap');

  // DOM 元素
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

  // 状态
  let recording = false;
  let points = []; // {x, y, t}
  let segments = []; // 每次按住-松开的一段轨迹
  let currentSegment = [];
  let width = 0;
  let height = 0;

  function resize() {
    const rect = wrap.getBoundingClientRect();
    // 保持逻辑分辨率与显示一致，避免模糊
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

  function toast(message, ms = 2500) {
    if (!toastEl) return;
    toastEl.textContent = message;
    toastEl.classList.add('show');
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => toastEl.classList.remove('show'), ms);
  }

  // -------------------- 绘制 --------------------
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

    // 已完成轨迹段
    segments.forEach((seg) => {
      if (seg.points.length < 2) return;
      ctx.strokeStyle = seg.good ? 'rgba(215,255,63,0.45)' : 'rgba(255,82,104,0.35)';
      ctx.lineWidth = 2;
      ctx.beginPath();
      ctx.moveTo(seg.points[0].x, seg.points[0].y);
      for (let i = 1; i < seg.points.length; i++) ctx.lineTo(seg.points[i].x, seg.points[i].y);
      ctx.stroke();
      seg.points.forEach((p) => {
        ctx.fillStyle = seg.good ? '#d7ff3f' : '#ff5268';
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

    // 拟合直线 + 角度标注
    if (points.length >= 10) {
      const fit = fitLine(points);
      if (fit) {
        // 拟合直线（两端延伸）
        const len = Math.sqrt(width * width + height * height);
        const dx = Math.cos(fit.angleRad) * len;
        const dy = Math.sin(fit.angleRad) * len;
        const mx = fit.cx;
        const my = fit.cy;
        ctx.strokeStyle = '#d7ff3f';
        ctx.lineWidth = 3;
        ctx.shadowColor = 'rgba(215,255,63,0.5)';
        ctx.shadowBlur = 12;
        ctx.beginPath();
        ctx.moveTo(mx - dx, my - dy);
        ctx.lineTo(mx + dx, my + dy);
        ctx.stroke();
        ctx.shadowBlur = 0;

        // 理想参考线（水平或垂直，取决于主方向）
        const idealAngle = fit.mainDir === 'h' ? 0 : Math.PI / 2;
        const idealLen = Math.min(width, height) * 0.85;
        const idx = Math.cos(idealAngle) * idealLen;
        const idy = Math.sin(idealAngle) * idealLen;
        ctx.strokeStyle = 'rgba(255,255,255,0.12)';
        ctx.setLineDash([7, 7]);
        ctx.beginPath();
        ctx.moveTo(mx - idx, my - idy);
        ctx.lineTo(mx + idx, my + idy);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
  }

  // -------------------- 算法：最小二乘法直线拟合 --------------------
  // 返回 { angleRad, angleDeg, cx, cy, r2, mainDir:'h'|'v', slope }
  function fitLine(pts) {
    const n = pts.length;
    if (n < 2) return null;
    let sx = 0, sy = 0, sxx = 0, sxy = 0, syy = 0;
    pts.forEach((p) => {
      sx += p.x; sy += p.y;
      sxx += p.x * p.x;
      sxy += p.x * p.y;
      syy += p.y * p.y;
    });
    const denom = n * sxx - sx * sx;
    if (Math.abs(denom) < 1e-9) return null; // 垂直直线，x 不变
    const a = (n * sxy - sx * sy) / denom; // 斜率
    const b = (sy - a * sx) / n;

    // 角度（弧度），相对 +x 轴
    let angleRad = Math.atan(a);
    let angleDeg = (angleRad * 180) / Math.PI;
    // 归一化到 [-90, 90]
    if (angleDeg > 90) angleDeg -= 180;
    if (angleDeg < -90) angleDeg += 180;

    // 主方向：判断轨迹整体更偏向水平还是垂直
    const dx = pts[n - 1].x - pts[0].x;
    const dy = pts[n - 1].y - pts[0].y;
    const mainDir = Math.abs(dx) >= Math.abs(dy) ? 'h' : 'v';

    // 相对理想方向的偏移
    const idealRad = mainDir === 'h' ? 0 : Math.PI / 2;
    let offsetRad = angleRad - idealRad;
    // 归一化偏移到 [-90, 90]
    let offsetDeg = (offsetRad * 180) / Math.PI;
    while (offsetDeg > 90) offsetDeg -= 180;
    while (offsetDeg < -90) offsetDeg += 180;

    // R^2
    const yMean = sy / n;
    let ssRes = 0, ssTot = 0;
    pts.forEach((p) => {
      const yPred = a * p.x + b;
      ssRes += (p.y - yPred) ** 2;
      ssTot += (p.y - yMean) ** 2;
    });
    const r2 = ssTot === 0 ? 1 : 1 - ssRes / ssTot;

    const cx = sx / n;
    const cy = sy / n;

    return {
      angleRad, angleDeg, offsetDeg, mainDir, slope: a, intercept: b,
      cx, cy, r2: clamp01(r2)
    };
  }

  function clamp01(v) {
    if (v === null || v === undefined || Number.isNaN(v)) return 0;
    return Math.min(1, Math.max(0, v));
  }

  // 将角度按“顺时针为正”给出驱动建议（各品牌定义可能相反，界面会提示）
  // 若实际偏移为 +θ，则建议填入 -θ 抵消；这里统一展示“偏移角度”与“建议填入值”
  function computeRecommend(offsetDeg) {
    // 建议填入 = -offsetDeg，用于抵消偏移
    return -offsetDeg;
  }

  // -------------------- 交互 --------------------
  function startRecording() {
    recording = true;
    currentSegment = [];
    points = [];
    segments = [];
    btnStart.disabled = true;
    btnStop.disabled = false;
    statusBadge.textContent = '记录中';
    statusBadge.classList.add('rec');
    statePill.textContent = '记录中 · 滑动鼠标';
    resultRow.style.display = 'none';
    pointCount.textContent = '0';
    directionPill.textContent = '—';
    detectAngle.textContent = '0°';
    fitR2.textContent = '—';
    recommendAngle.textContent = '0°';
    angleValue.textContent = '0';
    angleSignText.textContent = '—';
    draw();
    toast('已开始记录：请在画布上按住左键来回滑动');
  }

  function stopRecording() {
    if (!recording) return;
    recording = false;
    if (currentSegment.length > 0) {
      segments.push({ points: [...currentSegment], good: currentSegment.length >= 15 });
      points.push(...currentSegment);
      currentSegment = [];
    }
    btnStart.disabled = false;
    btnStop.disabled = true;
    statusBadge.textContent = '已完成';
    statusBadge.classList.remove('rec');
    statePill.textContent = '已停止 · 可计算';
    pointCount.textContent = points.length;

    if (points.length < 10) {
      toast('轨迹点过少（需 ≥ 10），请多滑动几次后再计算', 3500);
      draw();
      return;
    }

    calcAndShow();
    draw();
    toast('计算完成：查看右侧推荐角度');
  }

  function calcAndShow() {
    const fit = fitLine(points);
    if (!fit) {
      toast('无法拟合直线，请尝试更直线的滑动', 3500);
      return;
    }

    // 方向判断
    const dirText = fit.mainDir === 'h' ? '水平滑动' : '垂直滑动';
    directionPill.textContent = dirText;
    directionPill.style.color = fit.mainDir === 'h' ? 'var(--lime)' : 'var(--cyan)';

    // 检测到的偏移角度（相对理想方向）
    const offsetDeg = fit.offsetDeg;
    detectAngle.textContent = offsetDeg.toFixed(1) + '°';
    detectUnit.textContent = fit.mainDir === 'h'
      ? '拟合直线相对水平方向的偏移'
      : '拟合直线相对垂直方向的偏移';
    fitR2.textContent = fit.r2.toFixed(3);

    // 推荐写入驱动的角度（抵消偏移）
    const rec = computeRecommend(offsetDeg);
    recommendAngle.textContent = rec.toFixed(1) + '°';
    angleValue.textContent = Math.abs(rec).toFixed(1);
    angleSignText.textContent = rec >= 0 ? '正（顺时针）' : '负（逆时针）';
    recommendSign.textContent = rec >= 0 ? '建议填正值（顺时针）' : '建议填负值（逆时针）';
    if (Math.abs(rec) < 0.5) recommendSign.textContent = '角度极小，近似无需补偿';

    resultRow.style.display = 'grid';

    // 标记轨迹段品质
    const minLen = 80;
    segments.forEach((seg) => {
      const p0 = seg.points[0];
      const p1 = seg.points[seg.points.length - 1];
      const d = Math.hypot(p1.x - p0.x, p1.y - p0.y);
      seg.good = seg.points.length >= 15 && d >= minLen;
    });
    draw();
  }

  function resetAll() {
    recording = false;
    points = [];
    segments = [];
    currentSegment = [];
    btnStart.disabled = false;
    btnStop.disabled = true;
    statusBadge.textContent = '就绪';
    statusBadge.classList.remove('rec');
    statePill.textContent = '等待开始';
    resultRow.style.display = 'none';
    pointCount.textContent = '0';
    directionPill.textContent = '—';
    detectAngle.textContent = '0°';
    fitR2.textContent = '—';
    recommendAngle.textContent = '0°';
    angleValue.textContent = '0';
    angleSignText.textContent = '—';
    draw();
    toast('已重置');
  }

  function copyAngle() {
    const v = recommendAngle.textContent;
    navigator.clipboard.writeText(v).then(() => toast('已复制：' + v));
  }

  function exportText() {
    const fit = fitLine(points);
    const lines = [];
    lines.push('MAGLAB 鼠标传感器旋转角度检测记录');
    lines.push('时间：' + new Date().toLocaleString('zh-CN'));
    lines.push('轨迹点数：' + points.length);
    lines.push('主方向：' + (fit ? (fit.mainDir === 'h' ? '水平' : '垂直') : '—'));
    lines.push('检测偏移角度：' + (fit ? fit.offsetDeg.toFixed(2) + '°' : '—'));
    lines.push('拟合优度 R²：' + (fit ? fit.r2.toFixed(4) : '—'));
    lines.push('建议填入驱动角度：' + (fit ? computeRecommend(fit.offsetDeg).toFixed(2) + '°' : '—'));
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

  // 鼠标事件
  function onPointerDown(e) {
    if (!recording) return;
    const rect = canvas.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    currentSegment = [{ x, y, t: performance.now() }];
    points.push(currentSegment[0]);
    pointCount.textContent = points.length;
    draw();
  }

  function onPointerMove(e) {
    if (!recording || currentSegment.length === 0) return;
    const rect = canvas.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    // 采样降频：点距离上一个点超过 2px 才记录，避免过密
    const last = currentSegment[currentSegment.length - 1];
    if (Math.hypot(x - last.x, y - last.y) < 1.5) return;
    const pt = { x, y, t: performance.now() };
    currentSegment.push(pt);
    points.push(pt);
    pointCount.textContent = points.length;
    draw();
  }

  function onPointerUp() {
    if (!recording || currentSegment.length === 0) return;
    if (currentSegment.length >= 5) {
      segments.push({ points: [...currentSegment], good: false });
    }
    currentSegment = [];
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

    btnStart.addEventListener('click', startRecording);
    btnStop.addEventListener('click', stopRecording);
    btnReset.addEventListener('click', resetAll);
    btnCopy.addEventListener('click', copyAngle);
    btnExport.addEventListener('click', exportText);

    $('#resetBtn')?.addEventListener('click', () => {
      if (confirm('重置会清空当前所有轨迹与结果，继续？')) resetAll();
    });

    draw();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
