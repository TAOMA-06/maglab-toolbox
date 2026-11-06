# ============================================================================
# MAGLAB 磁轴工具箱 · CS2 辅助桌面程序 (Windows)
# 技术栈：Python 3 + tkinter（标准库，无需第三方依赖）
# 功能：
#   1. 鼠标传感器旋转角度检测（显示偏向左/右/上/下，给出推荐驱动角度）
#   2. CS2 电脑运算延迟优化（生成优化脚本 + 回退脚本，支持备份与还原）
#   3. 磁轴急停实验室入口（打开已有网页版）
# ============================================================================

import tkinter as tk
from tkinter import ttk, messagebox, filedialog, scrolledtext
import math
import os
import sys
import json
import subprocess
import webbrowser
import ctypes
import winreg
from datetime import datetime

APP_TITLE = "MAGLAB 磁轴工具箱 · CS2 辅助桌面程序"
APP_VERSION = "v1.0.0"

# ---------------------------------------------------------------- 颜色主题
BG       = "#06090e"
PANEL    = "#0c121a"
PANEL_2  = "#0f1722"
LINE_HEX = "#1f2a3a"
TEXT     = "#e9eff7"
MUTED    = "#8b98aa"
LIME     = "#d7ff3f"
CYAN     = "#48dfff"
RED      = "#ff5268"
AMBER    = "#ffb648"

# ---------------------------------------------------------------- 工具函数
def is_admin():
    """检查是否以管理员身份运行"""
    try:
        return ctypes.windll.shell32.IsUserAnAdmin() != 0
    except Exception:
        return False

def run_admin_exe(path):
    """以管理员身份运行可执行文件/bat"""
    try:
        ctypes.windll.shell32.ShellExecuteW(None, "runas", path, None, None, 1)
        return True
    except Exception as e:
        messagebox.showerror("权限错误", f"无法以管理员身份运行：{e}")
        return False

# ---------------------------------------------------------------- 模块：传感器角度检测
class SensorTab(ttk.Frame):
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self.recording = False
        self.points = []          # (x, y, t)
        self.segments = []        # 每段轨迹
        self.current_segment = []
        self.canvas_w = 900
        self.canvas_h = 520
        self._build_ui()
        self._draw_grid()

    def _build_ui(self):
        self.configure(style="Dark.TFrame")

        # 左右分栏
        self.columnconfigure(0, weight=3)
        self.columnconfigure(1, weight=1)
        self.rowconfigure(0, weight=1)

        # 左：画布
        left = ttk.Frame(self, style="Dark.TFrame")
        left.grid(row=0, column=0, sticky="nsew", padx=(0, 10))
        left.rowconfigure(1, weight=1)

        hdr = ttk.Frame(left, style="Panel.TFrame")
        hdr.grid(row=0, column=0, sticky="ew", pady=(0, 8))
        ttk.Label(hdr, text="轨迹记录画布", style="PanelH.TLabel").pack(side=tk.LEFT)
        self.status_lbl = ttk.Label(hdr, text="就绪", style="Status.TLabel")
        self.status_lbl.pack(side=tk.RIGHT)

        self.canvas = tk.Canvas(left, width=self.canvas_w, height=self.canvas_h,
                                bg="#080d14", highlightthickness=1,
                                highlightbackground=LINE_HEX, cursor="crosshair")
        self.canvas.grid(row=1, column=0, sticky="nsew")
        self.canvas.bind("<Button-1>", self._on_press)
        self.canvas.bind("<B1-Motion>", self._on_drag)
        self.canvas.bind("<ButtonRelease-1>", self._on_release)
        self.canvas.bind("<Leave>", self._on_release)

        hint = ttk.Frame(left, style="Dark.TFrame")
        hint.grid(row=2, column=0, sticky="ew", pady=(8, 0))
        hints = [
            "● 按住鼠标左键在画布上滑动",
            "● 建议方向：水平左右来回（或垂直上下），保持直线",
            "● 滑动 10 次以上后松开，点击“停止并计算”",
            "● 避免急停、打圈或大幅变向，否则拟合角度不准",
        ]
        for h in hints:
            ttk.Label(hint, text=h, style="Hint.TLabel").pack(anchor="w")

        # 右：控制面板
        right = ttk.Frame(self, style="Dark.TFrame")
        right.grid(row=0, column=1, sticky="nsew")
        right.rowconfigure(6, weight=1)

        sec = ttk.Frame(right, style="Panel.TFrame")
        sec.grid(row=0, column=0, sticky="nsew", pady=(0, 10))
        sec.columnconfigure(0, weight=1)

        ttk.Label(sec, text="控制与结果", style="PanelH.TLabel").grid(row=0, column=0, pady=(12, 10), sticky="w")

        # 状态
        self._kv(sec, 1, "记录状态", "等待开始", key="state")
        # 点数
        self._kv(sec, 2, "有效轨迹点", "0", key="points", big=True)
        # 方向
        self._kv(sec, 3, "滑动方向判断", "—", key="direction")
        # 检测角度
        self._kv(sec, 4, "检测偏移角度", "0°", key="detect")
        # 拟合优度
        self._kv(sec, 5, "拟合优度 R²", "—", key="r2")
        # 推荐角度
        self._kv(sec, 6, "推荐填入驱动角度", "0°", key="recommend", big=True)
        # 正负号说明
        self.sign_lbl = ttk.Label(sec, text="—", style="Sign.TLabel")
        self.sign_lbl.grid(row=7, column=0, sticky="w", padx=16, pady=(0, 10))

        # 按钮
        btnf = ttk.Frame(sec, style="Panel.TFrame")
        btnf.grid(row=8, column=0, sticky="ew", padx=16, pady=(0, 12))
        btnf.columnconfigure(0, weight=1)
        btnf.columnconfigure(1, weight=1)
        self.btn_start = ttk.Button(btnf, text="开始记录", command=self._start, style="Accent.TButton")
        self.btn_start.grid(row=0, column=0, sticky="ew", padx=(0, 5), pady=2)
        self.btn_stop = ttk.Button(btnf, text="停止并计算", command=self._stop, state="disabled", style="Accent.TButton")
        self.btn_stop.grid(row=0, column=1, sticky="ew", padx=(5, 0), pady=2)
        ttk.Button(btnf, text="重置", command=self._reset, style="Ghost.TButton").grid(row=1, column=0, columnspan=2, sticky="ew", pady=(6, 0))

        # 底部操作
        bot = ttk.Frame(sec, style="Panel.TFrame")
        bot.grid(row=9, column=0, sticky="ew", padx=16, pady=(0, 12))
        ttk.Button(bot, text="复制角度值", command=self._copy_angle, style="Ghost.TButton").pack(side=tk.LEFT, padx=(0, 6))
        ttk.Button(bot, text="导出记录", command=self._export, style="Ghost.TButton").pack(side=tk.LEFT)

        # 说明
        note = ttk.Frame(self, style="Dark.TFrame")
        note.grid(row=1, column=0, columnspan=2, sticky="ew", pady=(10, 0))
        ttk.Label(note,
                  text="本工具仅在本机运行，不上传数据。各品牌驱动对正负号定义可能相反；若填入后指针仍偏移，请将正负号反转再试。",
                  style="Note.TLabel").pack(anchor="w")

    def _kv(self, parent, row, label, value, key=None, big=False):
        fr = ttk.Frame(parent, style="Panel.TFrame")
        fr.grid(row=row, column=0, sticky="ew", padx=16, pady=3)
        fr.columnconfigure(1, weight=1)
        ttk.Label(fr, text=label, style="Meta.TLabel").grid(row=0, column=0, sticky="w")
        if big:
            lbl = ttk.Label(fr, text=value, style="Big.TLabel")
        else:
            lbl = ttk.Label(fr, text=value, style="Val.TLabel")
        lbl.grid(row=0, column=1, sticky="e")
        if key:
            setattr(self, f"lbl_{key}", lbl)

    # ----------------- 绘制与交互 -----------------
    def _draw_grid(self):
        self.canvas.delete("all")
        cw, ch = self.canvas_w, self.canvas_h
        step = 40
        self.canvas.create_rectangle(0, 0, cw, ch, fill="#080d14", outline="")
        for x in range(step, cw, step):
            self.canvas.create_line(x, 0, x, ch, fill="#121a26", width=1)
        for y in range(step, ch, step):
            self.canvas.create_line(0, y, cw, y, fill="#121a26", width=1)
        # 中心参考线
        self.canvas.create_line(cw // 2, 0, cw // 2, ch, fill="#1f2a3a", width=1, dash=(6, 6))
        self.canvas.create_line(0, ch // 2, cw, ch // 2, fill="#1f2a3a", width=1, dash=(6, 6))

    def _draw(self):
        self._draw_grid()
        # 已完成段
        for seg in self.segments:
            pts = seg["points"]
            color = LIME if seg.get("good") else RED
            if len(pts) >= 2:
                self.canvas.create_line([p[:2] for p in pts], fill=color, width=2, capstyle=tk.ROUND, joinstyle=tk.ROUND)
            for p in pts:
                self.canvas.create_oval(p[0]-2, p[1]-2, p[0]+2, p[1]+2, fill=color, outline="")
        # 当前段
        if len(self.current_segment) >= 2:
            self.canvas.create_line([p[:2] for p in self.current_segment], fill=CYAN, width=2.5, capstyle=tk.ROUND, joinstyle=tk.ROUND)
        for p in self.current_segment:
            self.canvas.create_oval(p[0]-2, p[1]-2, p[0]+2, p[1]+2, fill=CYAN, outline="")
        # 拟合直线
        if len(self.points) >= 10:
            fit = self._fit_line(self.points)
            if fit:
                cx, cy = fit["cx"], fit["cy"]
                ang = fit["angle_rad"]
                length = math.hypot(self.canvas_w, self.canvas_h)
                dx = math.cos(ang) * length
                dy = math.sin(ang) * length
                self.canvas.create_line(cx - dx, cy - dy, cx + dx, cy + dy,
                                        fill=LIME, width=3, capstyle=tk.ROUND)
                # 理想参考线
                ideal = 0 if fit["main"] == "h" else math.pi / 2
                ilen = min(self.canvas_w, self.canvas_h) * 0.85
                idx = math.cos(ideal) * ilen
                idy = math.sin(ideal) * ilen
                self.canvas.create_line(cx - idx, cy - idy, cx + idx, cy + idy,
                                        fill="#ffffff", width=1, dash=(7, 7))

    def _to_canvas(self, event):
        return event.x, event.y

    def _on_press(self, event):
        if not self.recording:
            return
        x, y = self._to_canvas(event)
        self.current_segment = [(x, y, self._now())]
        self.points.append(self.current_segment[0])
        self.lbl_points.config(text=str(len(self.points)))
        self._draw()

    def _on_drag(self, event):
        if not self.recording or not self.current_segment:
            return
        x, y = self._to_canvas(event)
        last = self.current_segment[-1]
        if math.hypot(x - last[0], y - last[1]) < 1.5:
            return
        pt = (x, y, self._now())
        self.current_segment.append(pt)
        self.points.append(pt)
        self.lbl_points.config(text=str(len(self.points)))
        self._draw()

    def _on_release(self, event):
        if not self.recording or not self.current_segment:
            return
        if len(self.current_segment) >= 5:
            self.segments.append({"points": list(self.current_segment), "good": False})
        self.current_segment = []
        self._draw()

    def _now(self):
        import time
        return time.time() * 1000

    # ----------------- 算法 -----------------
    def _fit_line(self, pts):
        """最小二乘法拟合直线，返回主方向、角度、R²、中心点"""
        n = len(pts)
        if n < 2:
            return None
        xs = [p[0] for p in pts]
        ys = [p[1] for p in pts]
        sx = sum(xs); sy = sum(ys)
        sxx = sum(x*x for x in xs)
        sxy = sum(xs[i]*ys[i] for i in range(n))
        syy = sum(y*y for y in ys)
        denom = n * sxx - sx * sx
        main = "h" if abs(xs[-1] - xs[0]) >= abs(ys[-1] - ys[0]) else "v"

        if abs(denom) < 1e-9:
            # 垂直直线，x 基本不变
            angle_rad = math.pi / 2
            angle_deg = 90.0
            offset = 90.0 if main == "h" else 0.0
            r2 = 1.0
        else:
            a = (n * sxy - sx * sy) / denom
            b = (sy - a * sx) / n
            angle_rad = math.atan(a)
            angle_deg = math.degrees(angle_rad)
            if angle_deg > 90: angle_deg -= 180
            if angle_deg < -90: angle_deg += 180
            ideal_rad = 0 if main == "h" else math.pi / 2
            offset = math.degrees(angle_rad - ideal_rad)
            while offset > 90: offset -= 180
            while offset < -90: offset += 180
            ymean = sy / n
            ss_res = sum((ys[i] - (a*xs[i]+b))**2 for i in range(n))
            ss_tot = sum((y - ymean)**2 for y in ys)
            r2 = 1.0 if ss_tot == 0 else 1 - ss_res / ss_tot
            r2 = max(0.0, min(1.0, r2))

        cx = sx / n
        cy = sy / n
        return {"angle_rad": angle_rad, "angle_deg": angle_deg,
                "offset": offset, "main": main, "cx": cx, "cy": cy, "r2": r2}

    def _direction_text(self, fit):
        """返回人类可读方向：偏左/右/上/下"""
        off = fit["offset"]
        main = fit["main"]
        if abs(off) < 1.0:
            return "近似无偏移", 0
        if main == "h":
            # 屏幕坐标系 y 向下；angle 为正表示随 x 增大 y 增大 -> 偏下
            return ("偏下" if off > 0 else "偏上"), off
        else:
            # 垂直滑动，x 随 y 增大而增加 -> 偏右；减少 -> 偏左
            return ("偏右" if off > 0 else "偏左"), off

    # ----------------- 按钮动作 -----------------
    def _start(self):
        self.recording = True
        self.points = []
        self.segments = []
        self.current_segment = []
        self.btn_start.config(state="disabled")
        self.btn_stop.config(state="normal")
        self.status_lbl.config(text="记录中")
        self.lbl_state.config(text="记录中 · 滑动鼠标")
        self.lbl_points.config(text="0")
        self.lbl_direction.config(text="—")
        self.lbl_detect.config(text="0°")
        self.lbl_r2.config(text="—")
        self.lbl_recommend.config(text="0°")
        self.sign_lbl.config(text="—")
        self._draw_grid()

    def _stop(self):
        if not self.recording:
            return
        self.recording = False
        if self.current_segment:
            self.segments.append({"points": list(self.current_segment), "good": False})
            self.current_segment = []
        self.btn_start.config(state="normal")
        self.btn_stop.config(state="disabled")
        self.status_lbl.config(text="已完成")
        self.lbl_state.config(text="已停止 · 可计算")
        self.lbl_points.config(text=str(len(self.points)))

        if len(self.points) < 10:
            self.app.toast("轨迹点过少（需 ≥ 10），请多滑动几次后再计算")
            self._draw()
            return

        fit = self._fit_line(self.points)
        if not fit:
            self.app.toast("无法拟合直线，请尝试更直线的滑动")
            return

        direction, off = self._direction_text(fit)
        self.lbl_direction.config(text=direction)
        self.lbl_detect.config(text=f"{off:.1f}°")
        self.lbl_r2.config(text=f"{fit['r2']:.3f}")

        rec = -off  # 抵消偏移
        self.lbl_recommend.config(text=f"{rec:.1f}°")
        sign_txt = "正（顺时针）" if rec >= 0 else "负（逆时针）"
        if abs(rec) < 0.5:
            sign_txt = "角度极小，近似无需补偿"
        self.sign_lbl.config(text=f"建议填入：{sign_txt}")
        # 颜色提示
        self.sign_lbl.config(foreground=LIME if abs(rec) < 0.5 else CYAN)

        # 标记段品质
        for seg in self.segments:
            p0 = seg["points"][0]; p1 = seg["points"][-1]
            d = math.hypot(p1[0]-p0[0], p1[1]-p0[1])
            seg["good"] = len(seg["points"]) >= 15 and d >= 80
        self._draw()
        self.app.toast("计算完成：查看右侧推荐角度")

    def _reset(self):
        if messagebox.askyesno("确认", "重置会清空当前所有轨迹与结果，继续？"):
            self.recording = False
            self.points = []
            self.segments = []
            self.current_segment = []
            self.btn_start.config(state="normal")
            self.btn_stop.config(state="disabled")
            self.status_lbl.config(text="就绪")
            self.lbl_state.config(text="等待开始")
            self.lbl_points.config(text="0")
            self.lbl_direction.config(text="—")
            self.lbl_detect.config(text="0°")
            self.lbl_r2.config(text="—")
            self.lbl_recommend.config(text="0°")
            self.sign_lbl.config(text="—")
            self._draw_grid()

    def _copy_angle(self):
        val = self.lbl_recommend.cget("text")
        self.app.clipboard_clear()
        self.app.clipboard_append(val)
        self.app.toast(f"已复制：{val}")

    def _export(self):
        fit = self._fit_line(self.points) if self.points else None
        lines = []
        lines.append("MAGLAB 鼠标传感器旋转角度检测记录")
        lines.append("时间：" + datetime.now().strftime("%Y-%m-%d %H:%M:%S"))
        lines.append("轨迹点数：" + str(len(self.points)))
        if fit:
            direction, off = self._direction_text(fit)
            lines.append("主方向：" + ("水平" if fit["main"] == "h" else "垂直"))
            lines.append("检测偏移：" + direction + f" ({off:.2f}°)")
            lines.append("拟合优度 R²：" + f"{fit['r2']:.4f}")
            rec = -off
            lines.append("建议填入驱动角度：" + f"{rec:.2f}°")
        lines.append("说明：各品牌驱动对正负号定义可能相反，如指针仍偏移请反转正负号。")
        path = filedialog.asksaveasfilename(defaultextension=".txt",
                                            filetypes=[("文本文件", "*.txt")],
                                            initialfile="maglab_sensor_angle.txt")
        if path:
            with open(path, "w", encoding="utf-8") as f:
                f.write("\n".join(lines))
            self.app.toast("已导出记录文本")

# ---------------------------------------------------------------- 模块：系统优化
class OptimizerTab(ttk.Frame):
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self.backup_file = os.path.join(os.path.dirname(sys.argv[0] or "."), "maglab_opt_backup.json")
        self._build_ui()
        self._refresh_sysinfo()

    def _build_ui(self):
        self.configure(style="Dark.TFrame")
        self.columnconfigure(0, weight=2)
        self.columnconfigure(1, weight=1)
        self.rowconfigure(0, weight=1)

        # 左：优化项
        left = ttk.Frame(self, style="Dark.TFrame")
        left.grid(row=0, column=0, sticky="nsew", padx=(0, 8))

        hdr = ttk.Frame(left, style="Panel.TFrame")
        hdr.grid(row=0, column=0, sticky="ew", pady=(0, 10))
        ttk.Label(hdr, text="优化项目", style="PanelH.TLabel").pack(side=tk.LEFT)

        self.vars = {}
        items = [
            ("power",  "电源计划 → 高性能 / 终极性能",
             "防止 CPU 频率下调，保持 CS2 单核运算频率，降低帧耗时。", "高"),
            ("gamemode", "Windows 游戏模式 → 开启",
             "让 Windows 在游戏时优先分配 CPU/GPU 资源，减少后台抢占。", "中"),
            ("gpusched", "硬件加速 GPU 调度 → 开启",
             "将部分 GPU 调度交给硬件，降低部分场景下的帧提交延迟（Win10 2004+/Win11）。", "中"),
            ("cs2launch", "CS2 启动参数优化",
             "在 Steam 属性中设置启动选项，如 -freq 144 -novid +fps_max 0。", "高"),
            ("cs2prio",  "CS2 进程优先级 → 高",
             "游戏运行时将 cs2.exe 优先级设为“高”。", "中"),
            ("temp",     "清理系统临时文件",
             "清空 %TEMP% 与预取文件，轻微改善系统响应。", "低"),
        ]
        row = 1
        for key, title, desc, impact in items:
            self._item(left, row, key, title, desc, impact)
            row += 1

        # 笔记本专用面板
        nb_panel = ttk.Frame(left, style="Panel.TFrame")
        nb_panel.grid(row=row, column=0, sticky="ew", pady=(10, 0))
        nb_panel.columnconfigure(0, weight=1)

        nb_header = ttk.Frame(nb_panel, style="Panel.TFrame")
        nb_header.grid(row=0, column=0, sticky="ew", padx=12, pady=(10, 6))
        nb_header.columnconfigure(1, weight=1)
        ttk.Label(nb_header, text="💻 笔记本专用优化", style="PanelH.TLabel").grid(row=0, column=0, sticky="w")
        self.nb_expanded = tk.BooleanVar(value=False)
        self.nb_toggle_btn = ttk.Button(nb_header, text="展开", width=8, command=self._toggle_nb, style="Ghost.TButton")
        self.nb_toggle_btn.grid(row=0, column=1, sticky="e")

        self.nb_content = ttk.Frame(nb_panel, style="Panel.TFrame")
        self.nb_content.grid(row=1, column=0, sticky="ew", padx=12, pady=(0, 10))
        self.nb_content.grid_remove()

        nb_items = [
            ("nb_dgpu", "独显直连 / 强制独显输出",
             "笔记本混合显卡（NVIDIA Optimus / AMD 混合显卡）强制独显直输，绕过核显，降低帧提交延迟。需在 NVIDIA 控制面板 / AMD 驱动中设置 CS2 为“高性能 NVIDIA 处理器”。", "高", False),
            ("nb_cpu_min", "插电时最小处理器状态 100%",
             "防止笔记本 CPU 在游戏中降频节能，保持单核运算频率，降低 CS2 帧耗时。", "高", True),
            ("nb_usb", "禁用 USB 选择性暂停",
             "防止笔记本 USB 接口进入低功耗导致鼠标轮询延迟或掉线，保持鼠标稳定输出。", "中", True),
            ("nb_pcie", "禁用 PCI Express 链路状态电源管理",
             "关闭 PCIe ASPM 节能，避免独显/硬件在低负载时降频，提升帧稳定性。", "中", False),
            ("nb_fastboot", "关闭 Windows 快速启动",
             "快速启动可能导致驱动未完全初始化，关闭后可保证每次开机驱动完整加载，减少延迟波动。", "中", False),
            ("nb_sysmain", "禁用 SysMain（Superfetch）",
             "笔记本机械硬盘或资源紧张时，禁用该服务可释放内存和磁盘I/O，减少卡顿。", "低", True),
            ("nb_wsearch", "禁用 Windows 搜索索引",
             "减少后台磁盘占用，适合笔记本游戏时降低系统背景负载。", "低", False),
        ]
        self.nb_vars = {}
        for i, (key, title, desc, impact, default) in enumerate(nb_items):
            var = tk.BooleanVar(value=default)
            self.nb_vars[key] = var
            fr = ttk.Frame(self.nb_content, style="Item.TFrame")
            fr.grid(row=i, column=0, sticky="ew", pady=3, padx=4)
            fr.columnconfigure(1, weight=1)
            cb = ttk.Checkbutton(fr, variable=var, style="Switch.TCheckbutton")
            cb.grid(row=0, column=0, sticky="w", padx=(10, 8), pady=6)
            txt = ttk.Frame(fr, style="Item.TFrame")
            txt.grid(row=0, column=1, sticky="ew", padx=(0, 10), pady=6)
            ttk.Label(txt, text=title, style="ItemTitle.TLabel").pack(anchor="w")
            ttk.Label(txt, text=desc, style="ItemDesc.TLabel").pack(anchor="w")
            imp = ttk.Label(fr, text=f"影响：{impact}", style="Impact.TLabel")
            imp.grid(row=0, column=2, sticky="e", padx=10)

        row += 1

        btnf = ttk.Frame(left, style="Panel.TFrame")
        btnf.grid(row=row, column=0, sticky="ew", pady=(12, 0))
        ttk.Button(btnf, text="生成优化脚本 (.bat)", command=self._gen, style="Accent.TButton").pack(fill=tk.X, pady=2)
        ttk.Button(btnf, text="复制脚本", command=self._copy_script, style="Ghost.TButton").pack(fill=tk.X, pady=2)
        ttk.Button(btnf, text="下载脚本", command=self._download, style="Ghost.TButton").pack(fill=tk.X, pady=2)

        note = ttk.Frame(left, style="Dark.TFrame")
        note.grid(row=row+1, column=0, sticky="ew", pady=(12, 0))
        ttk.Label(note,
                  text="重要：生成的 .bat 脚本请下载后【右键 → 以管理员身份运行】。\n执行完毕后重启电脑，再在 Steam → CS2 属性 → 启动选项填入建议参数。",
                  style="Note.TLabel").pack(anchor="w", justify="left")

        # 右：系统信息 + 备份/回退 + 预览
        right = ttk.Frame(self, style="Dark.TFrame")
        right.grid(row=0, column=1, sticky="nsew")
        right.rowconfigure(3, weight=1)

        sysf = ttk.Frame(right, style="Panel.TFrame")
        sysf.grid(row=0, column=0, sticky="ew", pady=(0, 10))
        ttk.Label(sysf, text="系统信息", style="PanelH.TLabel").pack(anchor="w", padx=12, pady=(10, 6))
        self.sys_lbl = ttk.Label(sysf, text="检测中...", style="Meta.TLabel")
        self.sys_lbl.pack(anchor="w", padx=12, pady=(0, 10))

        actf = ttk.Frame(right, style="Panel.TFrame")
        actf.grid(row=1, column=0, sticky="ew", pady=(0, 10))
        ttk.Label(actf, text="备份与回退", style="PanelH.TLabel").pack(anchor="w", padx=12, pady=(10, 6))
        ttk.Button(actf, text="备份当前系统状态", command=self._backup, style="Ghost.TButton").pack(fill=tk.X, padx=12, pady=2)
        self.btn_restore = ttk.Button(actf, text="一键回退（恢复备份）", command=self._restore, state="disabled", style="Ghost.TButton")
        self.btn_restore.pack(fill=tk.X, padx=12, pady=2)
        self.admin_lbl = ttk.Label(actf, text="", style="Warn.TLabel")
        self.admin_lbl.pack(anchor="w", padx=12, pady=(6, 10))
        self._update_admin_label()

        prevf = ttk.Frame(right, style="Panel.TFrame")
        prevf.grid(row=2, column=0, sticky="nsew", pady=(0, 10))
        prevf.rowconfigure(1, weight=1)
        prevf.columnconfigure(0, weight=1)
        ttk.Label(prevf, text="脚本预览", style="PanelH.TLabel").pack(anchor="w", padx=12, pady=(10, 6))
        self.preview = scrolledtext.ScrolledText(prevf, width=40, height=15, bg="#06090e", fg=LIME,
                                                 insertbackground=LIME, highlightthickness=1,
                                                 highlightbackground=LINE_HEX, font=("Consolas", 10))
        self.preview.pack(fill=tk.BOTH, expand=True, padx=12, pady=(0, 10))

        expf = ttk.Frame(right, style="Panel.TFrame")
        expf.grid(row=3, column=0, sticky="ew")
        ttk.Label(expf, text="预期改善", style="PanelH.TLabel").pack(anchor="w", padx=12, pady=(10, 6))
        for t in ["帧耗时下降：CPU 每帧运算更稳，右上角【最高 ~ 毫秒】数值变小",
                  "1% Low 更稳：减少突发卡顿与掉帧",
                  "输入到显示延迟降低：鼠标反馈更迅速"]:
            ttk.Label(expf, text="● " + t, style="Hint.TLabel").pack(anchor="w", padx=12, pady=1)

    def _item(self, parent, row, key, title, desc, impact):
        fr = ttk.Frame(parent, style="Item.TFrame")
        fr.grid(row=row, column=0, sticky="ew", pady=4, padx=4)
        fr.columnconfigure(1, weight=1)
        var = tk.BooleanVar(value=(key in ("power", "gamemode", "cs2launch", "temp")))
        self.vars[key] = var
        cb = ttk.Checkbutton(fr, variable=var, style="Switch.TCheckbutton")
        cb.grid(row=0, column=0, sticky="w", padx=(10, 8), pady=8)
        txt = ttk.Frame(fr, style="Item.TFrame")
        txt.grid(row=0, column=1, sticky="ew", padx=(0, 10), pady=8)
        ttk.Label(txt, text=title, style="ItemTitle.TLabel").pack(anchor="w")
        ttk.Label(txt, text=desc, style="ItemDesc.TLabel").pack(anchor="w")
        imp = ttk.Label(fr, text=f"影响：{impact}", style="Impact.TLabel")
        imp.grid(row=0, column=2, sticky="e", padx=10)

    def _refresh_sysinfo(self):
        try:
            import platform
            cores = os.cpu_count() or "—"
            mem = "—"
            try:
                import ctypes
                class MEMORYSTATUSEX(ctypes.Structure):
                    _fields_ = [("dwLength", ctypes.c_uint32), ("dwMemoryLoad", ctypes.c_uint32),
                                ("ullTotalPhys", ctypes.c_uint64), ("ullAvailPhys", ctypes.c_uint64),
                                ("ullTotalPageFile", ctypes.c_uint64), ("ullAvailPageFile", ctypes.c_uint64),
                                ("ullTotalVirtual", ctypes.c_uint64), ("ullAvailVirtual", ctypes.c_uint64),
                                ("ullAvailExtendedVirtual", ctypes.c_uint64)]
                ms = MEMORYSTATUSEX(); ms.dwLength = ctypes.sizeof(MEMORYSTATUSEX)
                ctypes.windll.kernel32.GlobalMemoryStatusEx(ctypes.byref(ms))
                mem = f"{ms.ullTotalPhys // (1024**3)} GB"
            except Exception:
                pass
            scr = f"{self.app.winfo_screenwidth()}×{self.app.winfo_screenheight()}"
            plat = platform.platform()
            self.sys_lbl.config(text=f"CPU 核心：{cores}   内存：{mem}   屏幕：{scr}\n平台：{plat}")
        except Exception as e:
            self.sys_lbl.config(text=f"系统信息读取失败：{e}")

    def _toggle_nb(self):
        if self.nb_expanded.get():
            self.nb_content.grid_remove()
            self.nb_expanded.set(False)
            self.nb_toggle_btn.config(text="展开")
        else:
            self.nb_content.grid()
            self.nb_expanded.set(True)
            self.nb_toggle_btn.config(text="收起")

    def _update_admin_label(self):
        if is_admin():
            self.admin_lbl.config(text="当前已以管理员身份运行", foreground=LIME)
        else:
            self.admin_lbl.config(text="当前非管理员身份，优化脚本需右键以管理员运行", foreground=AMBER)
        if os.path.exists(self.backup_file):
            self.btn_restore.config(state="normal")
        else:
            self.btn_restore.config(state="disabled")

    # ----------------- 脚本生成 -----------------
    def _gen(self):
        lines = self._bat_lines()
        self.preview.delete("1.0", tk.END)
        self.preview.insert("1.0", "\n".join(lines))
        self.app.toast("脚本预览已生成")

    def _bat_lines(self):
        lines = []
        lines.append("@echo off")
        lines.append("chcp 65001 >nul")
        lines.append("title MAGLAB CS2 电脑运算延迟优化")
        lines.append("echo ============================================")
        lines.append("echo MAGLAB CS2 电脑运算延迟一键优化脚本")
        lines.append("echo 请务必以【管理员身份】运行此脚本")
        lines.append("echo ============================================")
        lines.append("echo.")
        lines.append("setlocal enabledelayedexpansion")

        step = 0
        if self.vars["power"].get():
            step += 1
            lines.append(f":: [{step}] 电源计划 -> 高性能/终极性能")
            lines.append("echo [1] 设置电源计划为高性能...")
            lines.append("powercfg /setactive SCHEME_MAX >nul 2>&1")
            lines.append("powercfg /setactive 8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c >nul 2>&1")
            lines.append("if %errorlevel% equ 0 (echo [OK] 电源计划已设为高性能) else (echo [!] 电源计划设置失败)")
            lines.append("echo.")

        if self.vars["gamemode"].get():
            step += 1
            lines.append(f":: [{step}] Windows 游戏模式 -> 开启")
            lines.append('echo [2] 开启 Windows 游戏模式...')
            lines.append('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AutoGameModeEnabled /t REG_DWORD /d 1 /f >nul 2>&1')
            lines.append('reg add "HKCU\\Software\\Microsoft\\GameBar" /v AllowAutoGameMode /t REG_DWORD /d 1 /f >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] 游戏模式已开启) else (echo [!] 游戏模式设置失败)")
            lines.append("echo.")

        if self.vars["gpusched"].get():
            step += 1
            lines.append(f":: [{step}] 硬件加速 GPU 调度 -> 开启")
            lines.append('echo [3] 开启硬件加速 GPU 调度...')
            lines.append('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\GraphicsDrivers" /v HwSchMode /t REG_DWORD /d 1 /f >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] 硬件加速 GPU 调度已开启) else (echo [!] GPU 调度设置失败)")
            lines.append("echo.")

        if self.vars["temp"].get():
            step += 1
            lines.append(f":: [{step}] 清理系统临时文件")
            lines.append("echo [4] 清理临时文件...")
            lines.append('del /q /s /f "%TEMP%\\*" >nul 2>&1')
            lines.append("echo [OK] 临时文件已清理")
            lines.append("echo.")

        if self.vars["cs2launch"].get():
            step += 1
            lines.append(f":: [{step}] CS2 启动参数提示")
            lines.append('echo [5] 请手动在 Steam 中设置 CS2 启动选项：')
            lines.append('echo    右键 CS2 -> 属性 -> 启动选项 -> 填入：-freq 144 -novid +fps_max 0 +cl_forcepreload 1 +mat_queue_mode 2 -high')
            lines.append("echo.")

        if self.vars["cs2prio"].get():
            step += 1
            lines.append(f":: [{step}] CS2 进程优先级提示")
            lines.append('echo [6] CS2 运行时：任务管理器 -> 详细信息 -> cs2.exe -> 右键 -> 设置优先级 -> 高')
            lines.append("echo.")

        # 笔记本专用优化
        if self.nb_vars.get("nb_cpu_min") and self.nb_vars["nb_cpu_min"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：插电时最小处理器状态 100%")
            lines.append("echo [NB] 设置处理器最小状态为 100 percent...")
            lines.append('powercfg /setacvalueindex SCHEME_MAX 54533251-82be-4824-96c1-47b60b740d00 bc5038f7-23e0-4960-96da-33abaf5935ec 100 >nul 2>&1')
            lines.append('powercfg /setactive SCHEME_MAX >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] 最小处理器状态已设为 100 百分之百) else (echo [!] 设置失败，请手动在高级电源选项中调整)")
            lines.append("echo.")

        if self.nb_vars.get("nb_usb") and self.nb_vars["nb_usb"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：禁用 USB 选择性暂停")
            lines.append("echo [NB] 禁用 USB 选择性暂停...")
            lines.append('powercfg /setacvalueindex SCHEME_MAX 2a737441-1930-4402-8d77-b2bebba308a3 48e6b7a6-50f5-4782-a5d4-53bb8f07e226 0 >nul 2>&1')
            lines.append('powercfg /setactive SCHEME_MAX >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] USB 选择性暂停已禁用) else (echo [!] 设置失败，请手动在高级电源选项中调整)")
            lines.append("echo.")

        if self.nb_vars.get("nb_pcie") and self.nb_vars["nb_pcie"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：禁用 PCI Express 链路状态电源管理")
            lines.append("echo [NB] 禁用 PCIe ASPM...")
            lines.append('powercfg /setacvalueindex SCHEME_MAX 501a4d13-42af-4429-9fd1-a8218c268e20 ee12f906-d277-404b-b6da-e5fa1a576df5 0 >nul 2>&1')
            lines.append('powercfg /setactive SCHEME_MAX >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] PCIe 链路状态电源管理已禁用) else (echo [!] 设置失败，请手动在高级电源选项中调整)")
            lines.append("echo.")

        if self.nb_vars.get("nb_fastboot") and self.nb_vars["nb_fastboot"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：关闭 Windows 快速启动")
            lines.append("echo [NB] 关闭快速启动...")
            lines.append('reg add "HKLM\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Power" /v HiberbootEnabled /t REG_DWORD /d 0 /f >nul 2>&1')
            lines.append("if %errorlevel% equ 0 (echo [OK] 快速启动已关闭) else (echo [!] 设置失败)")
            lines.append("echo.")

        if self.nb_vars.get("nb_sysmain") and self.nb_vars["nb_sysmain"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：禁用 SysMain (Superfetch)")
            lines.append("echo [NB] 禁用 SysMain...")
            lines.append("sc config SysMain start= disabled >nul 2>&1")
            lines.append("net stop SysMain >nul 2>&1")
            lines.append("if %errorlevel% equ 0 (echo [OK] SysMain 已禁用) else (echo [!] SysMain 禁用失败)")
            lines.append("echo.")

        if self.nb_vars.get("nb_wsearch") and self.nb_vars["nb_wsearch"].get():
            step += 1
            lines.append(f":: [{step}] 笔记本：禁用 Windows 搜索索引")
            lines.append("echo [NB] 禁用 Windows 搜索...")
            lines.append("sc config WSearch start= disabled >nul 2>&1")
            lines.append("net stop WSearch >nul 2>&1")
            lines.append("if %errorlevel% equ 0 (echo [OK] Windows 搜索索引已禁用) else (echo [!] 禁用失败)")
            lines.append("echo.")

        lines.append("echo ============================================")
        lines.append("echo 优化脚本执行完毕。")
        lines.append("echo 建议：重启电脑后启动 CS2，在控制台输入 cl_showfps 2")
        lines.append("echo       观察右上角帧数左侧【最高 ~ 毫秒】数值变化。")
        lines.append("echo.")
        lines.append("pause")
        return lines

    def _copy_script(self):
        txt = self.preview.get("1.0", tk.END).strip()
        if not txt:
            return
        self.app.clipboard_clear()
        self.app.clipboard_append(txt)
        self.app.toast("脚本已复制到剪贴板")

    def _download(self):
        txt = self.preview.get("1.0", tk.END).strip()
        if not txt:
            self._gen()
            txt = self.preview.get("1.0", tk.END).strip()
        path = filedialog.asksaveasfilename(defaultextension=".bat",
                                            filetypes=[("批处理文件", "*.bat")],
                                            initialfile="MAGLAB_CS2_Latency_Optimize.bat")
        if path:
            with open(path, "w", encoding="utf-8") as f:
                f.write(txt + "\r\n")
            self.app.toast(f"已下载：{os.path.basename(path)}")

    # ----------------- 备份与回退 -----------------
    def _backup(self):
        data = {}
        # 电源计划
        try:
            out = subprocess.check_output("powercfg /getactivescheme", shell=True, stderr=subprocess.STDOUT, text=True)
            data["power"] = out.strip()
        except Exception as e:
            data["power"] = f"ERROR: {e}"
        # 注册表项
        for hive, path, val in [
            (winreg.HKEY_CURRENT_USER, r"Software\Microsoft\GameBar", "AutoGameModeEnabled"),
            (winreg.HKEY_LOCAL_MACHINE, r"SYSTEM\CurrentControlSet\Control\GraphicsDrivers", "HwSchMode"),
        ]:
            hive_name = "HKEY_CURRENT_USER" if hive == winreg.HKEY_CURRENT_USER else "HKEY_LOCAL_MACHINE"
            try:
                with winreg.OpenKey(hive, path, 0, winreg.KEY_READ) as k:
                    data[f"reg_{hive_name}_{path}_{val}"] = winreg.QueryValueEx(k, val)[0]
            except Exception as e:
                data[f"reg_{hive_name}_{path}_{val}"] = f"ERROR: {e}"
        data["time"] = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        try:
            with open(self.backup_file, "w", encoding="utf-8") as f:
                json.dump(data, f, ensure_ascii=False, indent=2)
            self.btn_restore.config(state="normal")
            self.app.toast("已备份当前系统状态")
        except Exception as e:
            messagebox.showerror("备份失败", str(e))

    def _restore(self):
        if not os.path.exists(self.backup_file):
            messagebox.showwarning("无备份", "未找到备份文件，无法回退。")
            return
        if not messagebox.askyesno("确认回退", "这会将电源计划和注册表恢复到备份时的状态，继续？"):
            return
        try:
            with open(self.backup_file, "r", encoding="utf-8") as f:
                data = json.load(f)
            # 电源计划回退：尝试解析 GUID
            import re
            m = re.search(r"([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})", data.get("power", ""))
            if m:
                subprocess.run(f'powercfg /setactive {m.group(1)}', shell=True, check=False)
            # 注册表回退（仅恢复已有值，ERROR 忽略）
            for key, val in data.items():
                if key.startswith("reg_") and not str(val).startswith("ERROR"):
                    # 格式：reg_HKEY_CURRENT_USER_Software\Microsoft\GameBar_AutoGameModeEnabled
                    # split("_", 3) 得到 ['reg', 'HKEY_CURRENT_USER', 'Software\Microsoft\GameBar', 'AutoGameModeEnabled']
                    parts = key.split("_", 3)
                    if len(parts) < 4:
                        continue
                    hive_name = parts[1]
                    reg_path = parts[2]
                    val_name = parts[3]
                    hive = winreg.HKEY_CURRENT_USER if hive_name == "HKEY_CURRENT_USER" else winreg.HKEY_LOCAL_MACHINE
                    try:
                        with winreg.OpenKey(hive, reg_path, 0, winreg.KEY_SET_VALUE) as k:
                            winreg.SetValueEx(k, val_name, 0, winreg.REG_DWORD, int(val))
                    except Exception:
                        pass
            self.app.toast("已尝试恢复备份状态，建议重启电脑")
        except Exception as e:
            messagebox.showerror("回退失败", str(e))

# ---------------------------------------------------------------- 模块：磁轴入口
class KeyboardTab(ttk.Frame):
    def __init__(self, parent, app):
        super().__init__(parent)
        self.app = app
        self._build_ui()

    def _build_ui(self):
        self.configure(style="Dark.TFrame")
        box = ttk.Frame(self, style="Panel.TFrame")
        box.pack(expand=True, fill=tk.BOTH, padx=40, pady=40)
        ttk.Label(box, text="模块一 · 磁轴设置 / 急停实验室", style="BigH.TLabel").pack(pady=(0, 8))
        ttk.Label(box, text="基于 Source 移动物理的 CS2 急停测试，用于磁轴键盘参数调校与 A/B 对比。",
                  style="Sub.TLabel").pack(pady=(0, 24))
        ttk.Button(box, text="打开急停实验室 (index.html)", command=self._open_kb, style="Accent.TButton").pack(pady=6, ipady=6)
        ttk.Button(box, text="返回工具箱首页", command=self._open_tools, style="Ghost.TButton").pack(pady=6, ipady=6)
        ttk.Label(box, text="本模块调用已有网页版 MAGLAB PRO，需浏览器打开。", style="Note.TLabel").pack(pady=(24, 0))

    def _open_kb(self):
        path = os.path.join(os.path.dirname(sys.argv[0] or "."), "index.html")
        if os.path.exists(path):
            webbrowser.open("file://" + os.path.abspath(path))
        else:
            messagebox.showerror("未找到", "index.html 不在当前目录，请确认与本程序同目录。")

    def _open_tools(self):
        path = os.path.join(os.path.dirname(sys.argv[0] or "."), "tools.html")
        if os.path.exists(path):
            webbrowser.open("file://" + os.path.abspath(path))
        else:
            messagebox.showerror("未找到", "tools.html 不在当前目录。")

# ---------------------------------------------------------------- 主程序
class MAGLabApp(tk.Tk):
    def __init__(self):
        super().__init__()
        self.title(APP_TITLE)
        self.geometry("1150x720")
        self.configure(bg=BG)
        self.minsize(900, 600)
        self._setup_styles()
        self._build_menu()
        self._build_notebook()
        self.status = tk.StringVar(value="就绪")
        self._build_statusbar()

    def _setup_styles(self):
        st = ttk.Style(self)
        st.theme_use("clam")
        st.configure("Dark.TFrame", background=BG)
        st.configure("Panel.TFrame", background=PANEL, borderwidth=0)
        st.configure("Item.TFrame", background=PANEL_2, borderwidth=0)
        st.configure("PanelH.TLabel", background=PANEL, foreground=TEXT,
                     font=("Microsoft YaHei UI", 11, "bold"))
        st.configure("Meta.TLabel", background=PANEL, foreground=MUTED,
                     font=("Microsoft YaHei UI", 9))
        st.configure("Val.TLabel", background=PANEL, foreground=TEXT,
                     font=("Consolas", 11))
        st.configure("Big.TLabel", background=PANEL, foreground=LIME,
                     font=("Consolas", 16, "bold"))
        st.configure("Sign.TLabel", background=PANEL, foreground=CYAN,
                     font=("Microsoft YaHei UI", 9))
        st.configure("Status.TLabel", background=PANEL, foreground=LIME,
                     font=("Consolas", 10, "bold"))
        st.configure("Hint.TLabel", background=BG, foreground=MUTED,
                     font=("Microsoft YaHei UI", 9))
        st.configure("Note.TLabel", background=BG, foreground=MUTED,
                     font=("Microsoft YaHei UI", 9))
        st.configure("Warn.TLabel", background=PANEL, foreground=AMBER,
                     font=("Microsoft YaHei UI", 9))
        st.configure("BigH.TLabel", background=PANEL, foreground=TEXT,
                     font=("Microsoft YaHei UI", 16, "bold"))
        st.configure("Sub.TLabel", background=PANEL, foreground=MUTED,
                     font=("Microsoft YaHei UI", 10))
        st.configure("ItemTitle.TLabel", background=PANEL_2, foreground=TEXT,
                     font=("Microsoft YaHei UI", 10, "bold"))
        st.configure("ItemDesc.TLabel", background=PANEL_2, foreground=MUTED,
                     font=("Microsoft YaHei UI", 9))
        st.configure("Impact.TLabel", background=PANEL_2, foreground=CYAN,
                     font=("Consolas", 9))
        st.configure("Accent.TButton", font=("Microsoft YaHei UI", 10, "bold"))
        st.configure("Ghost.TButton", font=("Microsoft YaHei UI", 9))
        st.configure("Switch.TCheckbutton", background=PANEL_2, foreground=TEXT,
                     font=("Microsoft YaHei UI", 9))

    def _build_menu(self):
        m = tk.Menu(self)
        fm = tk.Menu(m, tearoff=0)
        fm.add_command(label="关于", command=self._about)
        fm.add_separator()
        fm.add_command(label="退出", command=self.quit)
        m.add_cascade(label="文件", menu=fm)
        hm = tk.Menu(m, tearoff=0)
        hm.add_command(label="Python 下载页", command=lambda: webbrowser.open("https://www.python.org/downloads/windows/"))
        m.add_cascade(label="帮助", menu=hm)
        self.config(menu=m)

    def _build_notebook(self):
        self.nb = ttk.Notebook(self)
        self.nb.pack(fill=tk.BOTH, expand=True, padx=10, pady=10)
        self.tab_sensor = SensorTab(self.nb, self)
        self.tab_opt = OptimizerTab(self.nb, self)
        self.tab_kb = KeyboardTab(self.nb, self)
        self.nb.add(self.tab_sensor, text="鼠标传感器角度")
        self.nb.add(self.tab_opt, text="CS2 运算延迟优化")
        self.nb.add(self.tab_kb, text="磁轴急停实验室")

    def _build_statusbar(self):
        bar = ttk.Frame(self, style="Panel.TFrame")
        bar.pack(side=tk.BOTTOM, fill=tk.X)
        ttk.Label(bar, textvariable=self.status, style="Meta.TLabel").pack(side=tk.LEFT, padx=10, pady=4)
        ttk.Label(bar, text=APP_VERSION, style="Meta.TLabel").pack(side=tk.RIGHT, padx=10, pady=4)

    def toast(self, msg):
        self.status.set(msg)
        self.after(3000, lambda: self.status.set("就绪"))

    def _about(self):
        messagebox.showinfo("关于", f"{APP_TITLE}\n{APP_VERSION}\n\n本地运行 · 不读取游戏内存 · 不上传数据\n最终请以游戏内 cl_showfps 表现为准。")

# ---------------------------------------------------------------- 入口
if __name__ == "__main__":
    app = MAGLabApp()
    app.mainloop()
