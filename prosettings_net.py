# -*- coding: utf-8 -*-
"""
从 prosettings.net 拉取职业选手页面并解析外设、灵敏度与游戏内设置。
仅使用 Python 标准库（urllib），供桌面端 tkinter 使用。

注意：网站结构与 CSS 类名可能变更；解析失败时会抛出带上下文的 ValueError。
版权声明：页面内容由 ProSettings.net 提供，请在界面中保留来源链接。
"""
from __future__ import annotations

import html as html_mod
import re
import urllib.error
import urllib.request
from typing import Any, Dict, List, Optional, Tuple

BASE = "https://prosettings.net/players/"
# 站点对过长或非浏览器样式的 UA 会返回 403
USER_AGENT = "MAGLAB-Toolbox/1.0"


def _clean_text(s: str) -> str:
    s = html_mod.unescape(s)
    s = re.sub(r"<[^>]+>", "", s)
    s = re.sub(r"\s+", " ", s).strip()
    return s


def _fetch(url: str, timeout: float = 25.0) -> str:
    req = urllib.request.Request(
        url,
        headers={"User-Agent": USER_AGENT, "Accept-Language": "en-US,en;q=0.9"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        charset = "utf-8"
        ct = resp.headers.get_content_charset()
        if ct:
            charset = ct
        return resp.read().decode(charset, errors="replace")


def fetch_player_html(slug: str) -> str:
    slug = slug.strip().strip("/")
    if not slug or "/" in slug or ".." in slug:
        raise ValueError("选手 ID 无效：只能包含 URL 中的英文名部分，例如 s1mple")
    url = BASE + slug + "/"
    try:
        return _fetch(url)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            raise ValueError(f"未找到该选手页面：{slug}（404）") from e
        raise ValueError(f"请求失败：HTTP {e.code}") from e
    except urllib.error.URLError as e:
        raise ValueError(f"网络错误：{e.reason}") from e


def _top_level_settings_sections(html: str) -> List[str]:
    pat = re.compile(
        r'<section\s+id="([^"]+)"[^>]*class="[^"]*\bsettings-group\b',
        re.I,
    )
    return [m.group(1) for m in pat.finditer(html)]


def _slice_between_sections(html: str, section_id: str, ordered_ids: List[str]) -> str:
    try:
        idx = ordered_ids.index(section_id)
    except ValueError:
        return ""
    start_m = re.search(
        rf'<section\s+id="{re.escape(section_id)}"', html, re.I
    )
    if not start_m:
        return ""
    start = start_m.start()
    if idx + 1 >= len(ordered_ids):
        return html[start:]
    next_id = ordered_ids[idx + 1]
    end_m = re.search(rf'<section\s+id="{re.escape(next_id)}"', html[start + 1 :], re.I)
    if not end_m:
        return html[start:]
    end = start + 1 + end_m.start()
    return html[start:end]


def _parse_settings_table(table_html: str) -> List[Tuple[str, str]]:
    rows: List[Tuple[str, str]] = []
    for tr_m in re.finditer(r"<tr[^>]*>([\s\S]*?)</tr>", table_html, re.I):
        tr = tr_m.group(1)
        th_m = re.search(r"<th[^>]*>([\s\S]*?)</th>", tr, re.I)
        td_m = re.search(r"<td[^>]*>([\s\S]*?)</td>", tr, re.I)
        if not th_m or not td_m:
            continue
        key = _clean_text(th_m.group(1))
        val = _clean_text(td_m.group(1))
        if key:
            rows.append((key, val))
    return rows


def _parse_gear_block(chunk: str) -> List[Dict[str, str]]:
    out: List[Dict[str, str]] = []
    for box_m in re.finditer(
        r'<div class="cta-box promo[\s\S]*?(?=<div class="cta-box promo|$)',
        chunk,
    ):
        box = box_m.group(0)
        tag_m = re.search(
            r'<div class="cta-box__tag[^"]*">([^<]+)</div>', box, re.I
        )
        name_m = re.search(r"<h4><a[^>]*>([^<]+)</a></h4>", box, re.I)
        if tag_m and name_m:
            out.append(
                {
                    "category": _clean_text(tag_m.group(1)),
                    "name": _clean_text(name_m.group(1)),
                }
            )
    return out


def _parse_promo_device_name(chunk: str) -> Optional[str]:
    m = re.search(
        r'<div class="promo[^"]*"[\s\S]*?<h4><a[^>]*>([^<]+)</a></h4>',
        chunk,
        re.I,
    )
    return _clean_text(m.group(1)) if m else None


def _parse_section_tables(
    chunk: str, skip_crosshair_gallery: bool = True
) -> List[Dict[str, Any]]:
    """
    解析含 table.settings 的区块；每个 h3/h4 标题后跟若干表格。
    """
    parts: List[Dict[str, Any]] = []
    if skip_crosshair_gallery and "section--crosshair" in chunk:
        return parts
    combined = list(
        re.finditer(
            r"<h([34])[^>]*>([^<]+)</h[34]>[\s\S]*?<table\s+class=\"settings\"",
            chunk,
            re.I,
        )
    )
    for i, m in enumerate(combined):
        level = m.group(1)
        title = _clean_text(m.group(2))
        start = m.end()
        tbl_start = chunk.rfind("<table", m.start(), m.end() + 400)
        if tbl_start < 0:
            continue
        # 从第一个 table.settings 收集连续的 table.settings（同一子节可能有多个表，极少见）
        pos = tbl_start
        all_rows: List[Tuple[str, str]] = []
        while pos < len(chunk):
            topen = chunk.find('<table class="settings"', pos)
            if topen < 0:
                break
            if i + 1 < len(combined):
                next_h = combined[i + 1].start()
                if topen >= next_h:
                    break
            tend = chunk.find("</table>", topen)
            if tend < 0:
                break
            inner = chunk[topen : tend + len("</table>")]
            all_rows.extend(_parse_settings_table(inner))
            pos = tend + 1
        if all_rows:
            parts.append({"title": title, "level": level, "rows": all_rows})
    return parts


def parse_player_page(html: str, source_url: str) -> Dict[str, Any]:
    if "prosettings.net" not in source_url:
        source_url = BASE + "…"

    h1_m = re.search(
        r'<div[^>]*class="[^"]*name[^"]*"[^>]*>\s*<h1[^>]*>([^<]+)</h1>',
        html,
        re.I,
    )
    display_name = _clean_text(h1_m.group(1)) if h1_m else "—"

    team = "—"
    tm = re.search(
        r'<tr class="[^"]*field-team[^"]*"[^>]*>[\s\S]*?<td[^>]*>([\s\S]*?)</td>',
        html,
        re.I,
    )
    if tm:
        team = _clean_text(tm.group(1))

    updated = "—"
    ut = re.search(
        r'<time[^>]*datetime="([^"]+)"[^>]*>', html, re.I
    )
    if ut:
        updated = ut.group(1)

    ordered = _top_level_settings_sections(html)
    if not ordered:
        raise ValueError("无法解析页面结构（未找到 settings-group 区块）")

    gear: List[Dict[str, str]] = []
    if "gear" in ordered:
        gear = _parse_gear_block(_slice_between_sections(html, "gear", ordered))

    setup_extra: List[Dict[str, str]] = []
    for sec in ("setupstreaming", "pcspecs"):
        if sec in ordered:
            setup_extra.extend(
                _parse_gear_block(_slice_between_sections(html, sec, ordered))
            )

    game_prefix = None
    for sid in ordered:
        if "_" in sid and sid not in (
            "graphics_card",
            "monitor",
            "gear",
            "setupstreaming",
            "pcspecs",
            "authorship",
        ):
            game_prefix = sid.split("_", 1)[0] + "_"
            break

    game_sections: List[Dict[str, Any]] = []
    mouse_device = None
    mouse_rows: List[Tuple[str, str]] = []

    for sid in ordered:
        if not game_prefix or not sid.startswith(game_prefix):
            continue
        if sid.endswith("_crosshair"):
            continue
        chunk = _slice_between_sections(html, sid, ordered)
        if not chunk:
            continue
        h3_m = re.search(r"<h3[^>]*>([^<]+)</h3>", chunk, re.I)
        main_title = _clean_text(h3_m.group(1)) if h3_m else sid

        if "section--mouse" in chunk or sid.endswith("_mouse"):
            mouse_device = _parse_promo_device_name(chunk)
            for tbl in re.finditer(
                r'<table class="settings"[^>]*>([\s\S]*?)</table>', chunk, re.I
            ):
                mouse_rows = _parse_settings_table(tbl.group(0))
            # 灵敏度等已写入 mouse，避免在 game_sections 中重复
            continue

        subsections = _parse_section_tables(chunk, skip_crosshair_gallery=True)
        game_sections.append(
            {
                "id": sid,
                "main_title": main_title,
                "device": _parse_promo_device_name(chunk),
                "subsections": subsections,
                "flat_rows": [],
            }
        )

    monitor_parts: List[Dict[str, Any]] = []
    gpu_parts: List[Dict[str, Any]] = []

    for sid, target in (("monitor", monitor_parts), ("graphics_card", gpu_parts)):
        if sid not in ordered:
            continue
        chunk = _slice_between_sections(html, sid, ordered)
        h3_m = re.search(r"<h3[^>]*>([^<]+)</h3>", chunk, re.I)
        main_title = _clean_text(h3_m.group(1)) if h3_m else sid
        subsections = _parse_section_tables(chunk, skip_crosshair_gallery=False)
        target.append(
            {
                "id": sid,
                "main_title": main_title,
                "device": _parse_promo_device_name(chunk),
                "subsections": subsections,
            }
        )

    return {
        "display_name": display_name,
        "team": team,
        "last_updated": updated,
        "source_url": source_url.rstrip("/") + "/"
        if not source_url.endswith("/")
        else source_url,
        "gear": gear,
        "setup_extra": setup_extra,
        "mouse": {"device": mouse_device, "rows": mouse_rows},
        "game_sections": game_sections,
        "monitor": monitor_parts,
        "gpu": gpu_parts,
    }


def fetch_and_parse(slug: str) -> Dict[str, Any]:
    slug = slug.strip().strip("/")
    url = BASE + slug + "/"
    html = fetch_player_html(slug)
    return parse_player_page(html, url)


def format_summary_text(data: Dict[str, Any]) -> str:
    lines: List[str] = []
    lines.append("ProSettings.net · 选手设置摘要")
    lines.append(f"选手：{data['display_name']}  |  战队：{data['team']}")
    lines.append(f"页面更新：{data['last_updated']}")
    lines.append(f"来源：{data['source_url']}")
    lines.append("")
    if data["gear"]:
        lines.append("【外设 · Gear】")
        for g in data["gear"]:
            lines.append(f"  · {g['category']}: {g['name']}")
        lines.append("")
    if data.get("setup_extra"):
        lines.append("【主机 / 桌椅等】")
        for g in data["setup_extra"]:
            lines.append(f"  · {g['category']}: {g['name']}")
        lines.append("")
    m = data.get("mouse") or {}
    if m.get("device") or m.get("rows"):
        lines.append("【鼠标 / 灵敏度】")
        if m.get("device"):
            lines.append(f"  鼠标：{m['device']}")
        for k, v in m.get("rows") or []:
            lines.append(f"  {k}: {v}")
        lines.append("")
    for sec in data.get("game_sections") or []:
        if sec.get("flat_rows"):
            lines.append(f"【{sec.get('main_title', sec['id'])}】")
            for k, v in sec["flat_rows"]:
                lines.append(f"  {k}: {v}")
            lines.append("")
        elif sec.get("subsections"):
            lines.append(f"【{sec.get('main_title', sec['id'])}】")
            for sub in sec["subsections"]:
                lines.append(f"  · {sub['title']}")
                for k, v in sub["rows"]:
                    lines.append(f"      {k}: {v}")
            lines.append("")
    for label, key in (("显示器 OSD", "monitor"), ("显卡驱动", "gpu")):
        blocks = data.get(key) or []
        for blk in blocks:
            if not blk.get("subsections"):
                continue
            lines.append(f"【{label} · {blk.get('main_title', blk['id'])}】")
            if blk.get("device"):
                lines.append(f"  设备：{blk['device']}")
            for sub in blk["subsections"]:
                lines.append(f"  · {sub['title']}")
                for k, v in sub["rows"]:
                    lines.append(f"      {k}: {v}")
            lines.append("")
    lines.append(
        "数据来自第三方站点，可能滞后；交叉准星等以原页为准。"
    )
    return "\n".join(lines).rstrip() + "\n"
