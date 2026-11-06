/* ============================================================
   MAGLAB TOOLBOX · 工具中心页脚本
   ============================================================ */

(function () {
  'use strict';

  const $ = (selector, root = document) => root.querySelector(selector);
  const toastEl = $('#toast');

  function toast(message, ms = 2500) {
    if (!toastEl) return;
    toastEl.textContent = message;
    toastEl.classList.add('show');
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => toastEl.classList.remove('show'), ms);
  }

  function bindAbout() {
    const abouts = {
      aboutKb: '模块一 · 磁轴设置 / 急停实验室\n基于 Source 移动物理的 CS2 急停测试。重建速度曲线、34% 精度带、开枪时机、反键过冲与 A/B 参数对比。所有数据仅保存在本机浏览器 localStorage。',
      aboutSensor: '模块二 · 鼠标传感器旋转角度\n在平板上以固定方向（建议水平）来回滑动鼠标 10 次以上，工具拟合轨迹并给出推荐旋转角度。将角度填入 Razer Synapse / Cooler Master 等驱动，即可抵消传感器安装偏移，使屏幕指针移动与物理滑动方向一致。',
      aboutOpt: '模块三 · CS2 电脑运算延迟优化\n针对 CS2 右上角帧数左侧【最高 ~ 毫秒】（CPU 帧耗时）。生成 Windows 一键优化脚本：电源计划、游戏模式、硬件加速 GPU 调度、CS2 启动参数等。生成后请以管理员身份运行 .bat 脚本，再启动 CS2 观察毫秒数变化。',
      aboutProSettings: '模块四 · Pro 外设 / 灵敏度（ProSettings.net）\n仅在本仓库的 Windows 桌面程序 main.py 中提供：打开后切到「Pro 外设/设置」标签，输入选手页 slug（如 s1mple）或粘贴完整 ProSettings 选手链接即可拉取外设、灵敏度与多数游戏设置。数据来源于第三方网站，可能滞后；解析依赖页面 HTML，原站改版后或需更新。浏览器工具页无法直接请求该站（跨域限制），故须用桌面版。'
    };
    Object.entries(abouts).forEach(([id, msg]) => {
      const el = $(`#${id}`);
      if (el) el.addEventListener('click', () => toast(msg, 6000));
    });
  }

  function init() {
    bindAbout();
    const btn = $('#heroStartBtn');
    if (btn) btn.addEventListener('click', () => {
      const card = document.querySelector('.tool-card');
      if (card) card.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
