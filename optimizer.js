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
  const swPointer = $('#swPointer');
  const swReflex = $('#swReflex');
  const swFSO = $('#swFSO');
  const swVBS = $('#swVBS');
  const gpuGen = $('#gpuGen');

  // 启动参数：不写死 -freq（用当前屏幕刷新率），去掉 CS2 已废弃的 mat_queue_mode
  // 与多余的 -high（游戏模式已覆盖提权，且 -high 在部分反作弊环境多余）。
  const screenHz = (() => {
    try {
      const r = window.screen && window.screen.refreshRate;
      const n = Number(r);
      if (Number.isFinite(n) && n >= 60 && n <= 500) return Math.round(n);
    } catch (e) { /* ignore */ }
    return 144;
  })();
  const launchParams = `-freq ${screenHz} -novid -nojoy +fps_max 0`;

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
  // 序号全部用 step 变量，保证勾选增减时连续；说明类项目只输出 echo，不改系统。
  function gpuLabel(v) {
    if (v === 'gtx10') return 'GTX 10 / RTX 20 系';
    if (v === 'rtx30') return 'RTX 30 系';
    return 'RTX 40 / 50 系';
  }

  function genBat() {
    const lines = [];
    lines.push('@echo off');
    lines.push('chcp 65001 >nul');
    lines.push('title MAGLAB CS2 电脑运算延迟优化');
    lines.push('echo ============================================');
    lines.push('echo MAGLAB CS2 电脑运算延迟一键优化脚本');
    lines.push('echo 请务必以【管理员身份】运行此脚本');
    lines.push('echo HAGS / 电源计划改动需重启电脑才生效');
    lines.push('echo ============================================');
    lines.push('echo.');
    lines.push('setlocal enabledelayedexpansion');

    let step = 0;
    const head = (title) => {
      step += 1;
      lines.push(`:: [${step}] ${title}`);
      lines.push(`echo [${step}] ${title}...`);
    };
    const ok = (msg) => {
      lines.push(`if %errorlevel% equ 0 (echo [OK] ${msg}) else (echo [!] ${msg}失败，请确认以管理员身份运行)`);
    };
    const note = (msg) => lines.push(`echo     注：${msg}`);

    // --- 通用优化 ---
    if (swPower.checked) {
      head('电源计划 -> 高性能');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      lines.push('powercfg /setactive 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c >nul 2>&1');
      ok('电源计划已设为高性能');
      note('AMD 锐龙建议用 AMD Ryzen High Performance / Balanced，微软 Ultimate 可能干扰加速频率。');
      lines.push('echo.');
    }

    if (swGameMode.checked) {
      head('Windows 游戏模式 -> 开启');
      lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AutoGameModeEnabled /t REG_DWORD /d 1 /f >nul 2>&1');
      lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AllowAutoGameMode /t REG_DWORD /d 1 /f >nul 2>&1');
      ok('游戏模式已开启');
      lines.push('echo.');
    }

    if (swGpuSched.checked) {
      // HAGS 按显卡分代：10/20 系关（帧时间更稳），40/50 系开（吃 Reflex），30 系两边都测。
      const gen = gpuGen ? gpuGen.value : 'rtx40';
      const enable = gen !== 'gtx10';
      head(`硬件加速 GPU 调度 -> ${enable ? '开启' : '关闭'}（${gpuLabel(gen)}）`);
      lines.push(`reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\GraphicsDrivers" /v HwSchMode /t REG_DWORD /d ${enable ? 1 : 0} /f >nul 2>&1`);
      ok(`GPU 调度已设为${enable ? '开启' : '关闭'}`);
      note('此项需重启电脑生效。30 系两边都测，用 CapFrameX 看帧时间再定。');
      lines.push('echo.');
    }

    if (swTemp.checked) {
      head('清理系统临时文件');
      lines.push('del /q /s /f "%TEMP%\\*" >nul 2>&1');
      ok('临时文件已清理');
      note('主要释放磁盘空间，对帧耗时基本无直接影响。');
      lines.push('echo.');
    }

    if (swPointer && swPointer.checked) {
      head('鼠标增强指针精确度 -> 关闭（游戏内 1:1）');
      lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseSpeed /t REG_SZ /d 0 /f >nul 2>&1');
      lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseThreshold1 /t REG_SZ /d 0 /f >nul 2>&1');
      lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseThreshold2 /t REG_SZ /d 0 /f >nul 2>&1');
      ok('指针精确度已关闭');
      note('需注销或重启生效；Windows 鼠标速度保持 6/11。FPS 必关项。');
      lines.push('echo.');
    }

    if (swCs2Launch.checked) {
      head('CS2 启动参数（手动填 Steam）');
      lines.push('echo    右键 CS2 -> 属性 -> 启动选项 -> 填入：');
      lines.push(`echo    ${launchParams}`);
      note(`-freq 后数字已按本机 ${screenHz}Hz 生成，请与显示器刷新率一致。`);
      lines.push('echo.');
    }

    if (swCs2Priority.checked) {
      head('CS2 进程优先级 -> 高（手动）');
      lines.push('echo    每次开游戏后：任务管理器 -> 详细信息 -> cs2.exe -> 右键 -> 设置优先级 -> 高');
      note('开着游戏模式时收益很小，可跳过；FACEIT 等平台请确认不冲突。');
      lines.push('echo.');
    }

    if (swReflex && swReflex.checked) {
      head('NVIDIA Reflex（游戏内设置）');
      lines.push('echo    CS2 设置 -^> 视频 -^> 高级 -^> NVIDIA Reflex 低延迟 -^> 开启+增强');
      note('需 GTX 10 系以上 + 新驱动；此前驱动里低延迟模式保持默认（Reflex 接管）。40/50 系请同时保持 HAGS 开启。');
      lines.push('echo.');
    }

    if (swFSO && swFSO.checked) {
      head('禁用 CS2 全屏优化（手动）');
      lines.push('echo    找到 cs2.exe（一般在 Steam 库 steamapps\\common\\Counter-Strike Global Offensive\\game\\bin\\win64\\cs2.exe）');
      lines.push('echo    右键 -^> 属性 -^> 兼容性 -^> 勾选“禁用全屏优化” -^> 确定');
      note('独占全屏下延迟更稳；已用全屏模式的玩家优先做这一项。');
      lines.push('echo.');
    }

    if (swVBS && swVBS.checked) {
      head('内存完整性 / VBS（手动，需重启）');
      lines.push('echo    Windows 安全中心 -^> 设备安全性 -^> 内核隔离 -^> 内存完整性 -^> 关');
      note('这会降低虚拟化安全隔离；个人游戏机可接受，介意者跳过本项。');
      lines.push('echo.');
    }

    // --- 笔记本专用优化 ---
    if (swNbDgpu && swNbDgpu.checked) {
      head('独显直连 / 强制独显输出（手动设置，脚本无法代设）');
      lines.push('echo    NVIDIA 控制面板 -^> 管理 3D 设置 -^> 程序设置 -^> 添加 cs2.exe');
      lines.push('echo    首选图形处理器 -^> 高性能 NVIDIA 处理器；刷新率设为最高');
      lines.push('echo    AMD 显卡：在 AMD Software 游戏 -^> CS2 中选高性能；另看 BIOS 有无独显直连/MUX 开关');
      note('混合显卡绕过核显是笔记本帧延迟大头之一，值得手动做。');
      lines.push('echo.');
    }

    if (swNbCpuMin && swNbCpuMin.checked) {
      head('笔记本：插电时最小处理器状态 100%');
      lines.push('echo [NB] 设置处理器最小状态为 100%...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 54533251-82be-4824-96c1-47b60b740d00 bc5038f7-23e0-4960-96da-33abaf5935ec 100 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      ok('最小处理器状态已设为 100%%');
      lines.push('echo.');
    }

    if (swNbUsb && swNbUsb.checked) {
      head('笔记本：禁用 USB 选择性暂停');
      lines.push('echo [NB] 禁用 USB 选择性暂停...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 2a737441-1930-4402-8d77-b2bebba308a3 48e6b7a6-50f5-4782-a5d4-53bb8f07e226 0 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      ok('USB 选择性暂停已禁用');
      note('主要保鼠标轮询稳定、防掉线；对帧耗时是间接增益。');
      lines.push('echo.');
    }

    if (swNbPcie && swNbPcie.checked) {
      head('笔记本：禁用 PCI Express 链路状态电源管理');
      lines.push('echo [NB] 禁用 PCIe ASPM...');
      lines.push('powercfg /setacvalueindex SCHEME_MAX 501a4d13-42af-4429-9fd1-a8218c268e20 ee12f906-d277-404b-b6da-e5fa1a576df5 0 >nul 2>&1');
      lines.push('powercfg /setactive SCHEME_MAX >nul 2>&1');
      ok('PCIe 链路状态电源管理已禁用');
      note('边际增益；帧不稳且独显疑似降频时再开这一项。');
      lines.push('echo.');
    }

    if (swNbFastboot && swNbFastboot.checked) {
      head('笔记本：关闭 Windows 快速启动');
      lines.push('echo [NB] 关闭快速启动...');
      lines.push('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Power" /v HiberbootEnabled /t REG_DWORD /d 0 /f >nul 2>&1');
      ok('快速启动已关闭');
      note('只影响开机与驱动初始化，不影响游戏内帧耗时；开关机慢半拍属正常。');
      lines.push('echo.');
    }

    if (swNbSysmain && swNbSysmain.checked) {
      head('笔记本：禁用 SysMain (Superfetch)');
      lines.push('echo [NB] 禁用 SysMain...');
      lines.push('sc config SysMain start= disabled >nul 2>&1');
      lines.push('net stop SysMain >nul 2>&1');
      ok('SysMain 已禁用');
      note('SSD 上基本无增益；只有机械盘或内存常年吃满时有用。');
      lines.push('echo.');
    }

    if (swNbWsearch && swNbWsearch.checked) {
      head('笔记本：禁用 Windows 搜索索引');
      lines.push('echo [NB] 禁用 Windows 搜索...');
      lines.push('sc config WSearch start= disabled >nul 2>&1');
      lines.push('net stop WSearch >nul 2>&1');
      ok('Windows 搜索索引已禁用');
      note('开始菜单搜索会变慢；只为压后台负载，介意者跳过。');
      lines.push('echo.');
    }

    if (step === 0) {
      lines.push('echo [提示] 你一个优化项都没勾，生成的脚本什么都不会做。');
      lines.push('echo.');
    }

    lines.push('echo ============================================');
    lines.push('echo 优化脚本执行完毕。');
    lines.push('echo 建议：重启电脑后启动 CS2（启动选项已填），控制台输入 cl_showfps 2');
    lines.push('echo       观察右上角帧数左侧【最高 ~ 毫秒】数值变化。');
    lines.push('echo 如需还原：运行同一目录下的 MAGLAB_Rollback.bat（点页面“下载回退脚本”）。');
    lines.push('echo.');
    lines.push('pause');

    return lines.join('\r\n');
  }

  // -------------------- 回退脚本 --------------------
  function genRollback() {
    const lines = [];
    lines.push('@echo off');
    lines.push('chcp 65001 >nul');
    lines.push('title MAGLAB 回退脚本');
    lines.push('echo ============================================');
    lines.push('echo MAGLAB 一键回退：把本工具改过的系统设置恢复成 Windows 默认');
    lines.push('echo HAGS 按原样保留（开/关都有依据，请按显卡分代手动确认）');
    lines.push('echo 电源计划重置会删掉你的自定义电源计划！介意请直接关掉窗口。');
    lines.push('echo ============================================');
    lines.push('pause');
    lines.push('echo.');
    lines.push('powercfg /setactive SCHEME_BALANCED >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] 电源计划已回到平衡) else (echo [!] 电源计划回退失败)');
    lines.push('powercfg /restoredefaultschemes >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] 电源计划已重置为 Windows 默认) else (echo [!] 重置失败)');
    lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AutoGameModeEnabled /t REG_DWORD /d 0 /f >nul 2>&1');
    lines.push('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AllowAutoGameMode /t REG_DWORD /d 0 /f >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] 游戏模式已关闭) else (echo [!] 游戏模式回退失败)');
    lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseSpeed /t REG_SZ /d 1 /f >nul 2>&1');
    lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseThreshold1 /t REG_SZ /d 6 /f >nul 2>&1');
    lines.push('reg add "HKCU\\Control Panel\\Mouse" /v MouseThreshold2 /t REG_SZ /d 10 /f >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] 指针精确度已恢复默认) else (echo [!] 指针回退失败)');
    lines.push('sc config SysMain start= auto >nul 2>&1');
    lines.push('sc config WSearch start= delayed-auto >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] SysMain / 搜索索引已恢复自动) else (echo [!] 服务回退失败)');
    lines.push('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Power" /v HiberbootEnabled /t REG_DWORD /d 1 /f >nul 2>&1');
    lines.push('if %errorlevel% equ 0 (echo [OK] 快速启动已恢复开启) else (echo [!] 快速启动回退失败)');
    lines.push('echo.');
    lines.push('echo 回退完毕，请重启电脑。CS2 启动参数 / 游戏内 Reflex / 全屏优化请手动改回。');
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
    downloadText(bat, 'MAGLAB_CS2_Latency_Optimize.bat', 'application/x-msdos-batch; charset=utf-8');
    toast('已下载 MAGLAB_CS2_Latency_Optimize.bat');
  }

  function downloadRollback() {
    const bat = genRollback();
    downloadText(bat, 'MAGLAB_Rollback.bat', 'application/x-msdos-batch; charset=utf-8');
    toast('已下载 MAGLAB_Rollback.bat（出问题一键还原）');
  }

  function downloadText(content, filename, mime) {
    const blob = new Blob([content], { type: mime || 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }

  function copyLaunch() {
    navigator.clipboard.writeText(launchParams).then(() => {
      toast('CS2 启动参数已复制：' + launchParams);
    });
  }

  // 单项：系统类 -> 仅该项的 .bat；说明类 -> .txt 实文（以前这里下错成 bat 回显，已修）。
  window.optApp.oneClick = function (key) {
    // 说明类：直接下载真实文本，不经过开关
    const TEXT_ITEMS = {
      cs2launch: {
        name: 'MAGLAB_CS2_LaunchParams.txt',
        body: () => `CS2 启动选项（Steam 右键 CS2 -> 属性 -> 启动选项，填入整行）：\r\n\r\n${launchParams}\r\n\r\n-freq 后数字已按本机 ${screenHz}Hz 生成，请与显示器刷新率一致。\r\n`
      },
      cs2priority: {
        name: 'MAGLAB_CS2_Priority.txt',
        body: () => 'CS2 进程优先级 -> 高（每次开游戏后手动设一次）：\r\n\r\n任务管理器 -> 详细信息 -> cs2.exe -> 右键 -> 设置优先级 -> 高\r\n\r\n注：开着 Windows 游戏模式时收益很小，可跳过；FACEIT 等平台请确认不冲突。\r\n'
      },
      reflex: {
        name: 'MAGLAB_CS2_Reflex.txt',
        body: () => 'NVIDIA Reflex（游戏内设置）：\r\n\r\nCS2 设置 -> 视频 -> 高级 -> NVIDIA Reflex 低延迟 -> 开启+增强\r\n\r\n需 GTX 10 系以上 + 新驱动；此前驱动里低延迟模式保持默认（Reflex 接管）。\r\n40/50 系请同时保持 HAGS 开启。\r\n'
      },
      fso: {
        name: 'MAGLAB_CS2_FSO.txt',
        body: () => '禁用 CS2 全屏优化（手动）：\r\n\r\n找到 cs2.exe（一般在 Steam 库 steamapps\\common\\Counter-Strike Global Offensive\\game\\bin\\win64\\cs2.exe）\r\n右键 -> 属性 -> 兼容性 -> 勾选“禁用全屏优化” -> 确定\r\n\r\n独占全屏下延迟更稳。\r\n'
      },
      vbs: {
        name: 'MAGLAB_CS2_VBS.txt',
        body: () => '内存完整性 / VBS（手动，需重启）：\r\n\r\nWindows 安全中心 -> 设备安全性 -> 内核隔离 -> 内存完整性 -> 关\r\n\r\n注：这会降低虚拟化安全隔离；个人游戏机可接受，介意者跳过。\r\n'
      },
      nbDgpu: {
        name: 'MAGLAB_NB_Dgpu.txt',
        body: () => '独显直连 / 强制独显输出（手动设置，脚本无法代设）：\r\n\r\nNVIDIA 控制面板 -> 管理 3D 设置 -> 程序设置 -> 添加 cs2.exe\r\n首选图形处理器 -> 高性能 NVIDIA 处理器；刷新率设为最高\r\nAMD 显卡：在 AMD Software 游戏 -> CS2 中选高性能；另看 BIOS 有无独显直连/MUX 开关\r\n'
      }
    };
    if (TEXT_ITEMS[key]) {
      const item = TEXT_ITEMS[key];
      downloadText(item.body(), item.name, 'text/plain;charset=utf-8');
      toast('单项说明已下载');
      return;
    }

    const saved = {};
    ALL_SWITCHES.forEach(([k, get]) => {
      const el = get();
      if (el) saved[k] = el.type === 'checkbox' ? el.checked : el.value;
    });

    // 只开启当前项
    ALL_SWITCHES.forEach(([, get]) => {
      const el = get();
      if (el && el.type === 'checkbox') el.checked = false;
    });

    const byKey = {};
    ALL_SWITCHES.forEach(([k, get]) => { byKey[k] = get(); });
    if (byKey[key] && byKey[key].type === 'checkbox') byKey[key].checked = true;

    renderPreview();

    const bat = genBat();
    downloadText(bat, {
      power: 'MAGLAB_CS2_PowerPlan.bat',
      gamemode: 'MAGLAB_CS2_GameMode.bat',
      gpusched: 'MAGLAB_CS2_GPUSched.bat',
      pointer: 'MAGLAB_CS2_Pointer.bat',
      temp: 'MAGLAB_CS2_CleanTemp.bat',
      nbcpumin: 'MAGLAB_NB_CpuMin.bat',
      nbusb: 'MAGLAB_NB_Usb.bat',
      nbpcie: 'MAGLAB_NB_Pcie.bat',
      nbfastboot: 'MAGLAB_NB_Fastboot.bat',
      nbsysmain: 'MAGLAB_NB_SysMain.bat',
      nbwsearch: 'MAGLAB_NB_WSearch.bat'
    }[key] || 'MAGLAB_CS2_Item.bat', 'application/x-msdos-batch; charset=utf-8');

    // 恢复勾选
    ALL_SWITCHES.forEach(([k, get]) => {
      const el = get();
      if (!el) return;
      if (el.type === 'checkbox') el.checked = Boolean(saved[k]);
      else el.value = saved[k];
    });
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
    if (swPointer) swPointer.checked = true;
    if (swReflex) swReflex.checked = true;
    if (swFSO) swFSO.checked = true;
    if (swVBS) swVBS.checked = false;
    if (gpuGen) gpuGen.value = 'rtx40';
    if (swNbDgpu) swNbDgpu.checked = false;
    if (swNbCpuMin) swNbCpuMin.checked = true;
    if (swNbUsb) swNbUsb.checked = true;
    if (swNbPcie) swNbPcie.checked = false;
    if (swNbFastboot) swNbFastboot.checked = false;
    if (swNbSysmain) swNbSysmain.checked = false;
    if (swNbWsearch) swNbWsearch.checked = false;
    renderPreview();
    toast('已重置为默认勾选');
  }

  // -------------------- 勾选状态记忆（localStorage） --------------------
  const SAVE_KEY = 'maglab_optimizer_selections_v1';
  const ALL_SWITCHES = [
    ['power', () => swPower], ['gamemode', () => swGameMode], ['gpusched', () => swGpuSched],
    ['cs2launch', () => swCs2Launch], ['cs2priority', () => swCs2Priority], ['temp', () => swTemp],
    ['pointer', () => swPointer], ['reflex', () => swReflex], ['fso', () => swFSO], ['vbs', () => swVBS],
    ['gpugen', () => gpuGen],
    ['nbDgpu', () => swNbDgpu], ['nbCpuMin', () => swNbCpuMin], ['nbUsb', () => swNbUsb],
    ['nbPcie', () => swNbPcie], ['nbFastboot', () => swNbFastboot], ['nbSysmain', () => swNbSysmain],
    ['nbWsearch', () => swNbWsearch]
  ];

  function saveSelections() {
    try {
      const data = {};
      ALL_SWITCHES.forEach(([key, get]) => {
        const el = get();
        if (!el) return;
        data[key] = el.type === 'checkbox' ? el.checked : el.value;
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
        if (!el) return;
        if (el.type === 'checkbox' && typeof data[key] === 'boolean') el.checked = data[key];
        else if (el.type !== 'checkbox' && typeof data[key] === 'string') el.value = data[key];
      });
    } catch (e) { /* ignore */ }
  }

  function init() {
    detectSys();
    loadSelections();

    [swPower, swGameMode, swGpuSched, swCs2Launch, swCs2Priority, swTemp,
     swPointer, swReflex, swFSO, swVBS].forEach((sw) => {
      if (sw) sw.addEventListener('change', () => { saveSelections(); renderPreview(); });
    });
    [swNbDgpu, swNbCpuMin, swNbUsb, swNbPcie, swNbFastboot, swNbSysmain, swNbWsearch].forEach((sw) => {
      if (sw) sw.addEventListener('change', () => { saveSelections(); renderPreview(); });
    });
    if (gpuGen) gpuGen.addEventListener('change', () => { saveSelections(); renderPreview(); });

    btnGen.addEventListener('click', renderPreview);
    btnDownload.addEventListener('click', downloadBat);
    btnCopyScript.addEventListener('click', copyScript);
    const btnRollback = $('#btnDownloadRollback');
    if (btnRollback) btnRollback.addEventListener('click', downloadRollback);
    btnReset.addEventListener('click', () => { resetAll(); saveSelections(); });

    renderPreview();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
