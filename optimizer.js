/* ============================================================
   MAGLAB CS2 OPTIMIZER · 电脑运算延迟优化（含笔记本专用）
   生成 Windows .bat 一键优化脚本（用户需以管理员身份运行）
   ============================================================ */

(function () {
  'use strict';

  const $ = (selector, root = document) => root.querySelector(selector);
  const toastEl = $('#toast');

  // 元素
  const sysCores = $('#sysCores');
  const sysMem = $('#sysMem');
  const sysScreen = $('#sysScreen');
  const sysUA = $('#sysUA');
  const scriptPreview = $('#scriptPreview');
  const btnGen = $('#btnGenScript');
  const btnDownload = $('#btnDownload');
  const btnCopyScript = $('#btnCopyScript');
  const btnReset = $('#resetBtn');

  const swPower = $('#swPower');
  const swGameMode = $('#swGameMode');
  const swGpuSched = $('#swGpuSched');
  const swCs2Launch = $('#swCs2Launch');
  const swCs2Priority = $('#swCs2Priority');
  const swTemp = $('#swTemp');

  // 笔记本专用
  const nbPanel = $('#nbContent');
  const nbToggle = $('#nbToggle');
  const swNbDgpu = $('#swNbDgpu');
  const swNbCpuMin = $('#swNbCpuMin');
  const swNbUsb = $('#swNbUsb');
  const swNbPcie = $('#swNbPcie');
  const swNbFastboot = $('#swNbFastboot');
  const swNbSysmain = $('#swNbSysmain');
  const swNbWsearch = $('#swNbWsearch');

  const launchParams = '-freq 144 -novid +fps_max 0 +cl_forcepreload 1 +mat_queue_mode 2 -high';

  let nbExpanded = false;

  // 系统信息
  function detectSys() {
    if (sysCores) sysCores.textContent = navigator.hardwareConcurrency || '—';
    if (sysMem) sysMem.textContent = (navigator.deviceMemory || '—') + (navigator.deviceMemory ? ' GB' : '');
    if (sysScreen) {
      const r = screen.refreshRate ? ` @ ${screen.refreshRate}Hz` : '';
      sysScreen.textContent = `${screen.width}×${screen.height}${r}`;
    }
    if (sysUA) sysUA.textContent = navigator.platform.includes('Win') ? 'Windows' : navigator.platform;
  }

  function toast(message, ms = 2500) {
    if (!toastEl) return;
    toastEl.textContent = message;
    toastEl.classList.add('show');
    clearTimeout(toastEl._t);
    toastEl._t = setTimeout(() => toastEl.classList.remove('show'), ms);
  }

  // -------------------- 笔记本专用面板展开/收起 --------------------
  window.optApp = window.optApp || {};
  window.optApp.toggleNB = function () {
    nbExpanded = !nbExpanded;
    if (nbPanel) {
      nbPanel.style.display = nbExpanded ? 'block' : 'none';
    }
    if (nbToggle) {
      nbToggle.textContent = nbExpanded ? '收起' : '展开';
    }
  };

  // NVIDIA 独显直连提示
  window.optApp.copyNbDgpuTip = function () {
    const tip = [
      'NVIDIA 独显直连设置方法：',
      '1. 右键桌面 → NVIDIA 控制面板',
      '2. “3D 设置” → “管理 3D 设置” → “程序设置”',
      '3. 添加 cs2.exe，选择“高性能 NVIDIA 处理器”',
      '4. 刷新率设为显示器最高，低延迟模式开启“超”或“开启”',
      '5. 应用后重启 CS2'
    ].join('\r\n');
    navigator.clipboard.writeText(tip).then(() => toast('已复制 NVIDIA 独显直连设置方法'));
  };

  // -------------------- .bat 脚本生成 --------------------
  function genBat() {
    const lines = [];
    lines.push('@echo off');
    lines.push('chcp 65001 >nul');
    lines.push('title MAGLAB CS2 电脑运算延迟优化（含笔记本专用）');
    lines.push('echo ============================================');
    lines.push('echo MAGLAB CS2 电脑运算延迟一键优化脚本');
    lines.push('echo 请务必以【管理员身份】运行此脚本');
    lines.push('echo ============================================');
    lines.push('echo.');
    lines.push('setlocal enabledelayedexpansion');

    let step = 0;

    // --- 通用优化 ---
    if (swPower.checked) {
      step++;
      lines.push(`:: [${step}] 电源计划 -> 高性能/终极性能`);
      lines.push('echo [1] 设置电源计划为高性能...');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      lines.push('powercfg /setactive 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] 电源计划已设为高性能) else (echo [!] 电源计划设置失败)');
      lines.push('echo.');
    }

    if (swGameMode.checked) {
      step++;
      lines.push(`:: [${step}] Windows 游戏模式 -> 开启`);
      lines.push('echo [2] 开启 Windows 游戏模式...');
      lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AutoGameModeEnabled /t REG_DWORD /d 1 /f >nul 2>&1');
      lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AllowAutoGameMode /t REG_DWORD /d 1 /f >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] 游戏模式已开启) else (echo [!] 游戏模式设置失败)');
      lines.push('echo.');
    }

    if (swGpuSched.checked) {
      step++;
      lines.push(`:: [${step}] 硬件加速 GPU 调度 -> 开启`);
      lines.push('echo [3] 开启硬件加速 GPU 调度...');
      lines.push('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\GraphicsDrivers" /v HwSchMode /t REG_DWORD /d 1 /f >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] 硬件加速 GPU 调度已开启) else (echo [!] GPU 调度设置失败)');
      lines.push('echo.');
    }

    if (swTemp.checked) {
      step++;
      lines.push(`:: [${step}] 清理系统临时文件`);
      lines.push('echo [4] 清理临时文件...');
      lines.push('del /q /s /f "%TEMP%\\*" >nul 2>&1');
      lines.push('echo [OK] 临时文件已清理');
      lines.push('echo.');
    }

    if (swCs2Launch.checked) {
      step++;
      lines.push(`:: [${step}] CS2 启动参数提示`);
      lines.push('echo [5] 请手动在 Steam 中设置 CS2 启动选项：');
      lines.push(`echo    右键 CS2 -> 属性 -> 启动选项 -> 填入：${launchParams}`);
      lines.push('echo.');
    }

    if (swCs2Priority.checked) {
      step++;
      lines.push(`:: [${step}] CS2 进程优先级提示`);
      lines.push('echo [6] CS2 运行时：任务管理器 -> 详细信息 -> cs2.exe -> 右键 -> 设置优先级 -> 高');
      lines.push('echo.');
    }

    // --- 笔记本专用优化 ---
    if (swNbCpuMin && swNbCpuMin.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：插电时最小处理器状态 100%`);
      lines.push('echo [NB] 设置处理器最小状态为 100%...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 54533251-82be-4824-96c1-47b60b740d00 bc5038f7-23e0-4960-96da-33abaf5935ec 100 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] 最小处理器状态已设为 100%%) else (echo [!] 设置失败，请手动在高级电源选项中调整)');
      lines.push('echo.');
    }

    if (swNbUsb && swNbUsb.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：禁用 USB 选择性暂停`);
      lines.push('echo [NB] 禁用 USB 选择性暂停...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 2a737441-1930-4402-8d77-b2bebba308a3 48e6b7a6-50f5-4782-a5d4-53bb8f07e226 0 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] USB 选择性暂停已禁用) else (echo [!] 设置失败，请手动在高级电源选项中调整)');
      lines.push('echo.');
    }

    if (swNbPcie && swNbPcie.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：禁用 PCI Express 链路状态电源管理`);
      lines.push('echo [NB] 禁用 PCIe ASPM...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 501a4d13-42af-4429-9fd1-a8218c268e20 ee12f906-d277-404b-b6da-e5fa1a576df5 0 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] PCIe 链路状态电源管理已禁用) else (echo [!] 设置失败，请手动在高级电源选项中调整)');
      lines.push('echo.');
    }

    if (swNbFastboot && swNbFastboot.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：关闭 Windows 快速启动`);
      lines.push('echo [NB] 关闭快速启动...');
      lines.push('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Power" /v HiberbootEnabled /t REG_DWORD /d 0 /f >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] 快速启动已关闭) else (echo [!] 设置失败)');
      lines.push('echo.');
    }

    if (swNbSysmain && swNbSysmain.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：禁用 SysMain (Superfetch)`);
      lines.push('echo [NB] 禁用 SysMain...');
      lines.push('sc config SysMain start= disabled >nul 2>&1');
      lines.push('net stop SysMain >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] SysMain 已禁用) else (echo [!] SysMain 禁用失败)');
      lines.push('echo.');
    }

    if (swNbWsearch && swNbWsearch.checked) {
      step++;
      lines.push(`:: [${step}] 笔记本：禁用 Windows 搜索索引`);
      lines.push('echo [NB] 禁用 Windows 搜索...');
      lines.push('sc config WSearch start= disabled >nul 2>&1');
      lines.push('net stop WSearch >nul 2>&1');
      lines.push('if %errorlevel% equ 0 (echo [OK] Windows 搜索索引已禁用) else (echo [!] 禁用失败)');
      lines.push('echo.');
    }

    lines.push('echo ============================================');
    lines.push('echo 优化脚本执行完毕。');
    lines.push('echo 建议：重启电脑后启动 CS2，在控制台输入 cl_showfps 2');
    lines.push('echo       观察右上角帧数左侧【最高 ~ 毫秒】数值变化。');
    lines.push('echo.');
    lines.push('pause');

    return lines.join('\r\n');
  }

  function renderPreview() {
    const bat = genBat();
    scriptPreview.textContent = bat;
    btnDownload.style.display = 'inline-block';
  }

  function copyScript() {
    const bat = genBat();
    navigator.clipboard.writeText(bat).then(() => {
      toast('脚本已复制到剪贴板');
    }).catch(() => {
      const ta = document.createElement('textarea');
      ta.value = bat;
      ta.style.position = 'fixed';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      toast('脚本已复制到剪贴板');
    });
  }

  function downloadBat() {
    const bat = genBat();
    const blob = new Blob([bat], { type: 'application/x-msdos-batch; charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'MAGLAB_CS2_Latency_Optimize.bat';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    toast('已下载 MAGLAB_CS2_Latency_Optimize.bat');
  }

  function copyLaunch() {
    navigator.clipboard.writeText(launchParams).then(() => {
      toast('CS2 启动参数已复制：' + launchParams);
    });
  }

  // 单项应用：生成仅含该项的脚本并下载
  window.optApp.oneClick = function (key) {
    const saved = {
      power: swPower.checked,
      gamemode: swGameMode.checked,
      gpusched: swGpuSched.checked,
      cs2launch: swCs2Launch.checked,
      cs2priority: swCs2Priority.checked,
      temp: swTemp.checked,
      nbDgpu: swNbDgpu ? swNbDgpu.checked : false,
      nbCpuMin: swNbCpuMin ? swNbCpuMin.checked : false,
      nbUsb: swNbUsb ? swNbUsb.checked : false,
      nbPcie: swNbPcie ? swNbPcie.checked : false,
      nbFastboot: swNbFastboot ? swNbFastboot.checked : false,
      nbSysmain: swNbSysmain ? swNbSysmain.checked : false,
      nbWsearch: swNbWsearch ? swNbWsearch.checked : false
    };

    // 只开启当前项
    swPower.checked = false; swGameMode.checked = false; swGpuSched.checked = false;
    swCs2Launch.checked = false; swCs2Priority.checked = false; swTemp.checked = false;
    if (swNbDgpu) swNbDgpu.checked = false;
    if (swNbCpuMin) swNbCpuMin.checked = false;
    if (swNbUsb) swNbUsb.checked = false;
    if (swNbPcie) swNbPcie.checked = false;
    if (swNbFastboot) swNbFastboot.checked = false;
    if (swNbSysmain) swNbSysmain.checked = false;
    if (swNbWsearch) swNbWsearch.checked = false;

    if (key === 'power') swPower.checked = true;
    if (key === 'gamemode') swGameMode.checked = true;
    if (key === 'gpusched') swGpuSched.checked = true;
    if (key === 'cs2launch') swCs2Launch.checked = true;
    if (key === 'cs2priority') swCs2Priority.checked = true;
    if (key === 'temp') swTemp.checked = true;
    if (key === 'nbcpumin') swNbCpuMin.checked = true;
    if (key === 'nbusb') swNbUsb.checked = true;
    if (key === 'nbpcie') swNbPcie.checked = true;
    if (key === 'nbfastboot') swNbFastboot.checked = true;
    if (key === 'nbsysmain') swNbSysmain.checked = true;
    if (key === 'nbwsearch') swNbWsearch.checked = true;

    renderPreview();

    const bat = genBat();
    const blob = new Blob([bat], { type: 'application/x-msdos-batch; charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    const names = {
      power: 'MAGLAB_CS2_PowerPlan.bat',
      gamemode: 'MAGLAB_CS2_GameMode.bat',
      gpusched: 'MAGLAB_CS2_GPUSched.bat',
      cs2launch: 'MAGLAB_CS2_LaunchParams.txt',
      cs2priority: 'MAGLAB_CS2_Priority.txt',
      temp: 'MAGLAB_CS2_CleanTemp.bat',
      nbcpumin: 'MAGLAB_NB_CpuMin.bat',
      nbusb: 'MAGLAB_NB_Usb.bat',
      nbpcie: 'MAGLAB_NB_Pcie.bat',
      nbfastboot: 'MAGLAB_NB_Fastboot.bat',
      nbsysmain: 'MAGLAB_NB_SysMain.bat',
      nbwsearch: 'MAGLAB_NB_WSearch.bat'
    };
    a.download = names[key] || 'MAGLAB_CS2_Item.bat';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);

    // 恢复勾选
    swPower.checked = saved.power; swGameMode.checked = saved.gamemode; swGpuSched.checked = saved.gpusched;
    swCs2Launch.checked = saved.cs2launch; swCs2Priority.checked = saved.cs2priority; swTemp.checked = saved.temp;
    if (swNbDgpu) swNbDgpu.checked = saved.nbDgpu;
    if (swNbCpuMin) swNbCpuMin.checked = saved.nbCpuMin;
    if (swNbUsb) swNbUsb.checked = saved.nbUsb;
    if (swNbPcie) swNbPcie.checked = saved.nbPcie;
    if (swNbFastboot) swNbFastboot.checked = saved.nbFastboot;
    if (swNbSysmain) swNbSysmain.checked = saved.nbSysmain;
    if (swNbWsearch) swNbWsearch.checked = saved.nbWsearch;
    renderPreview();
    toast('单项脚本已下载');
  };

  window.optApp.copyLaunch = copyLaunch;

  function resetAll() {
    swPower.checked = true;
    swGameMode.checked = true;
    swGpuSched.checked = false;
    swCs2Launch.checked = true;
    swCs2Priority.checked = false;
    swTemp.checked = true;
    if (swNbDgpu) swNbDgpu.checked = false;
    if (swNbCpuMin) swNbCpuMin.checked = true;
    if (swNbUsb) swNbUsb.checked = true;
    if (swNbPcie) swNbPcie.checked = false;
    if (swNbFastboot) swNbFastboot.checked = false;
    if (swNbSysmain) swNbSysmain.checked = true;
    if (swNbWsearch) swNbWsearch.checked = false;
    renderPreview();
    toast('已重置为默认勾选');
  }

  // -------------------- 勾选状态记忆（localStorage） --------------------
  const SAVE_KEY = 'maglab_optimizer_selections_v1';
  const ALL_SWITCHES = [
    ['power', () => swPower], ['gamemode', () => swGameMode], ['gpusched', () => swGpuSched],
    ['cs2launch', () => swCs2Launch], ['cs2priority', () => swCs2Priority], ['temp', () => swTemp],
    ['nbDgpu', () => swNbDgpu], ['nbCpuMin', () => swNbCpuMin], ['nbUsb', () => swNbUsb],
    ['nbPcie', () => swNbPcie], ['nbFastboot', () => swNbFastboot], ['nbSysmain', () => swNbSysmain],
    ['nbWsearch', () => swNbWsearch]
  ];

  function saveSelections() {
    try {
      const data = {};
      ALL_SWITCHES.forEach(([key, get]) => {
        const el = get();
        if (el) data[key] = el.checked;
      });
      localStorage.setItem(SAVE_KEY, JSON.stringify(data));
    } catch (e) { /* ignore */ }
  }

  function loadSelections() {
    try {
      const raw = localStorage.getItem(SAVE_KEY);
      if (!raw) return;
      const data = JSON.parse(raw);
      ALL_SWITCHES.forEach(([key, get]) => {
        const el = get();
        if (el && typeof data[key] === 'boolean') el.checked = data[key];
      });
    } catch (e) { /* ignore */ }
  }

  function init() {
    detectSys();
    loadSelections();

    [swPower, swGameMode, swGpuSched, swCs2Launch, swCs2Priority, swTemp].forEach((sw) => {
      if (sw) sw.addEventListener('change', () => { saveSelections(); renderPreview(); });
    });
    [swNbDgpu, swNbCpuMin, swNbUsb, swNbPcie, swNbFastboot, swNbSysmain, swNbWsearch].forEach((sw) => {
      if (sw) sw.addEventListener('change', () => { saveSelections(); renderPreview(); });
    });

    btnGen.addEventListener('click', renderPreview);
    btnDownload.addEventListener('click', downloadBat);
    btnCopyScript.addEventListener('click', copyScript);
    btnReset.addEventListener('click', () => { resetAll(); saveSelections(); });

    renderPreview();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
