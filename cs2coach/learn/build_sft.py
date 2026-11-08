#!/usr/bin/env python3
"""build_sft.py —— 把 exporter 的「每回合 JSONL」变成 A 层 SFT 数据（prompt / completion）。

设计锁（docs/CONTRACTS.md）：**解析层只提事实；LLM 只解读不发明。**
因此这里做两件事，界线必须清楚：

1. prompt = 规则层事实的结构化中文渲染（回合号 + tick 锚点 + 可引用 tick 清单）。
   渲染过程不添加任何事实：没有的字段就写「数据里没有」，绝不补一个像样的数值。
2. completion = 教练解读。默认 teacher=rule：从 outcome.cause_tags + cause 证据文本
   确定性拼装（免费、可控、可复现）；teacher=llm 只是一个外部命令 hook，没有硬依赖。

输入兼容两种形态（--input 可以是文件或目录）：
- exporter 每回合 JSONL：meta / context / economy / opening / utility / alive / bomb / outcome
- 战术层 analysis.json：root.rounds[]（便于在 exporter 落地前先跑通流程）

输出（--out-dir，默认 learn/data/sft）：
- train.jsonl / valid.jsonl（外加 --format chat 时写 messages 格式）
- 每行默认只有 prompt / completion 两个键；--emit-meta 会多一个 mlx-lm 会忽略的 meta 键，
  方便排查数据来源，也方便测试验证「按 match 切分、不泄漏」。

用法：
  python3 learn/build_sft.py --input out/rounds.jsonl --out-dir learn/data/sft
  python3 learn/build_sft.py --input out/rounds.jsonl --probs out/pro_win_prob.jsonl
  python3 learn/build_sft.py --input out/rounds.jsonl --teacher llm \
      --teacher-cmd "ollama run qwen2.5:7b"

只依赖标准库；没有 mlx / torch / openai 也能跑。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import shlex
import statistics
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, Iterable, Iterator, List, Optional, Sequence, Set, Tuple

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
DEFAULT_OUT_DIR = HERE / "data" / "sft"
DEFAULT_CONFIG = HERE / "configs" / "model.yaml"

# ---- 渲染预算：1.5B 小模型的上下文很贵，字段越多越容易复述噪声 ----
MAX_TRADES = 3          # 补枪事件最多 3 条（只保留 8 秒内的，超窗的是后续击杀不是补枪）
MAX_UTIL_EVENTS = 5     # 道具投掷最多 5 条
MAX_ALIVE_STEPS = 8     # 存活序列只保留「人数变化」的点
MAX_MAN_ADV = 2         # 人数优势区间最多 2 段
MAX_EVIDENCE = 3        # 未被结构化事实覆盖的补充证据最多 3 条
MAX_WHY_BULLETS = 2     # completion「为什么」最多解释 2 个归因标签
MAX_FACTS_LINES = 3     # completion「发生了什么」最多 3 条
TEACHER_TICK_MIN_DEV = 0.15  # B 层预测偏差小于这个值就不写进 completion（噪声）

END_REASON_CN = {
    "elimination": "团灭",
    "bomb_exploded": "炸弹爆炸",
    "bomb_defused": "拆包成功",
    "time": "时间耗尽",
    "draw": "平局",
    "surrender": "投降",
}
BUY_CN = {"full": "全起", "force": "强起", "half": "半起", "eco": "经济局"}
CAUSE_LABEL = {
    "first_blood_gift": "首杀白给",
    "trade_fail": "补枪失败",
    "utility_gap": "道具缺口",
    "eco_force": "经济局强起",
    "clutch_throw": "残局决策",
    "man_advantage_lost": "人数优势被翻",
}
# 归因标签的「通用解释」与「下把怎么做」。只解释机制，不补充本场数字；
# 本场数字永远来自 cause_detail（exporter / tactics 层写的证据句）。
TAG_WHY = {
    "first_blood_gift": (
        "开局首杀被对方拿走，5v5 直接变成 4v5，后面的每个点都要以少打多，节奏被对手牵着走。"
    ),
    "trade_fail": (
        "队友倒地后的 5 秒是补枪窗口，窗口里没人推枪线，对手白拿一个人头还全身而退，下一波又得少打多。"
    ),
    "utility_gap": (
        "交火前道具投入比对手少，进点没有烟和闪掩护，等于把身体送到对方架好的枪线上；带着道具死更是双倍浪费。"
    ),
    "eco_force": (
        "经济局/强起去接对面的满配，枪械和道具都吃亏，赢了只换掉一两个人，输了连下回合经济一起赔进去。"
    ),
    "clutch_throw": (
        "已经进入对焦点方有利的残局，人数或血量占优时最怕给对手 1v1 单挑，一次对枪失手就把优势局送掉。"
    ),
    "man_advantage_lost": (
        "回合里已经拿到人数优势却被逐个击破，说明优势期没有抱团，给了对面单挑翻盘的机会。"
    ),
}
TAG_ACTION = {
    "first_blood_gift": (
        "开局先按默认位架枪，首死的人立刻报点，离得最近的两人 5 秒内推同一条枪线补枪；"
        "补不到就全队退回包点，别在 4v5 时分散找人。"
    ),
    "trade_fail": (
        "进点前先约定补枪顺位：第一人倒地，最近的队友立刻推同一条枪线补枪，其他人补位架枪，不要各自拉开。"
    ),
    "utility_gap": (
        "默认开局先交 2 颗烟切断对面视野再进；突破手进场前把闪交掉，别把烟闪带到死。"
    ),
    "eco_force": (
        "经济局不接团：5 人抱团守一个点或集体保枪，等下一回合满配再打；要打就 5 人一起打一个点，不要分批送。"
    ),
    "clutch_throw": (
        "残局先数人数：占优就合围、卡死退路，不给 1v1；血量低就先退到掩体后等队友，不要单独追。"
    ),
    "man_advantage_lost": (
        "领先一人就抱团走一起，用道具清点再进，不要分头找人对枪。"
    ),
}
# 归因标签 → 优先引用的 tick 来源（保证 completion 的引用落在本回合 whitelist 内）
TAG_TICK_SOURCE = {
    "first_blood_gift": ("opening_tick", "death_tick", "tick_start"),
    # 补枪失败也可能发生在队友身上（没有焦点死亡），退到开局 tick 比退到回合入口更贴近事实
    "trade_fail": ("death_tick", "trade_tick", "opening_tick", "tick_start"),
    "utility_gap": ("death_tick", "tick_start"),
    "eco_force": ("tick_start",),
    "clutch_throw": ("clutch_tick", "tick_start"),
    "man_advantage_lost": ("manadv_tick", "tick_start"),
}
UTIL_CN = {
    "smoke": "烟雾弹",
    "flash": "闪光弹",
    "flashbang": "闪光弹",
    "he": "高爆手雷",
    "hegrenade": "高爆手雷",
    "molotov": "燃烧弹",
    "incendiary": "燃烧弹",
    "fire": "燃烧弹",
    "decoy": "诱饵弹",
}

TICK_RE = re.compile(r"\[tick\s*(\d+)\]")
GOTO_RE = re.compile(r"demo_gototick\s*(\d+)")
CITE_ROUND_RE = re.compile(r"回合\s*(\d+)")


# --------------------------------------------------------------------------
# 通用取值：exporter 的键名可能微调，这里用别名表吸收差异；取不到就是 None
# --------------------------------------------------------------------------


def pick(d: Any, *names: str) -> Any:
    """按顺序返回第一个存在且非空的键值（0 是有效值，不会被跳过）。"""
    if not isinstance(d, dict):
        return None
    for name in names:
        if name in d:
            v = d[name]
            if v is not None and v != "":
                return v
    return None


def as_int(v: Any) -> Optional[int]:
    if isinstance(v, bool) or v is None:
        return None
    if isinstance(v, int):
        return v
    if isinstance(v, float):
        return int(round(v))
    if isinstance(v, str):
        s = v.strip().replace(",", "")
        if not s:
            return None
        try:
            return int(float(s))
        except ValueError:
            return None
    return None


def as_float(v: Any) -> Optional[float]:
    if isinstance(v, bool) or v is None:
        return None
    if isinstance(v, (int, float)):
        return float(v)
    if isinstance(v, str):
        s = v.strip().replace("%", "")
        if not s:
            return None
        try:
            return float(s)
        except ValueError:
            return None
    return None


def norm_side(v: Any) -> str:
    s = str(v or "").strip().lower()
    if s in ("t", "terrorist", "terrorists", "t_side", "2"):
        return "T"
    if s in ("ct", "counter-terrorist", "counterterrorist", "counter_terrorists", "3"):
        return "CT"
    if s in ("draw", "tie", "none"):
        return "draw"
    return ""


def norm_buy(v: Any) -> str:
    s = str(v or "").strip().lower()
    return s if s in BUY_CN else (s or "")


def score_of(v: Any) -> Dict[str, int]:
    """比分统一成 {'T': int, 'CT': int}，缺的边不写（不补 0，避免造出 0:0 的假事实）。"""
    out: Dict[str, int] = {}
    if isinstance(v, dict):
        for key, val in v.items():
            side = norm_side(key)
            n = as_int(val)
            if side in ("T", "CT") and n is not None:
                out[side] = n
    return out


def money(v: Optional[int]) -> str:
    return "数据里没有" if v is None else "${:,}".format(v)


def yes_no(v: Optional[bool]) -> str:
    if v is None:
        return "数据里没有"
    return "是" if v else "否"


def cite(round_no: int, tick: Optional[int]) -> str:
    """引用格式固定：回合R [tick T]；没有 tick 时只写回合号，绝不编一个。"""
    if tick:
        return "回合{} [tick {}]".format(round_no, tick)
    return "回合{}".format(round_no)


def cjk_ratio_estimate(text: str) -> int:
    """粗略 token 估算（Qwen 系中文约 1 token/字，ASCII 约 0.3）。只用于预算告警。"""
    cjk = sum(1 for ch in text if "\u4e00" <= ch <= "\u9fff" or "\u3000" <= ch <= "\u303f")
    other = len(text) - cjk
    return int(cjk + other * 0.3)


# --------------------------------------------------------------------------
# 归一化：把 exporter 的 sections / analysis.rounds[] 收敛成一个 RoundFacts
# --------------------------------------------------------------------------


@dataclass
class RoundFacts:
    round_no: int
    match_id: str = ""
    map_name: str = ""
    focus_name: str = ""
    focus_side: str = ""
    half: str = ""
    tick_start: int = 0
    tick_end: int = 0
    score_before: Dict[str, int] = field(default_factory=dict)
    score_after: Dict[str, int] = field(default_factory=dict)
    winner_side: str = ""
    focus_won: Optional[bool] = None
    end_reason: str = ""
    win_cost: Optional[float] = None
    buys: Dict[str, str] = field(default_factory=dict)
    buy_focus: str = ""
    econ: Dict[str, Dict[str, Any]] = field(default_factory=dict)
    opening: Optional[Dict[str, Any]] = None
    trades: List[Dict[str, Any]] = field(default_factory=list)
    focus_death: Optional[Dict[str, Any]] = None
    clutch: Optional[Dict[str, Any]] = None
    man_adv: List[Dict[str, Any]] = field(default_factory=list)
    util_before: Dict[str, int] = field(default_factory=dict)
    util_events: List[Dict[str, Any]] = field(default_factory=list)
    alive: List[Dict[str, int]] = field(default_factory=list)
    bombs: List[Dict[str, Any]] = field(default_factory=list)
    evidence: List[Dict[str, Any]] = field(default_factory=list)
    cause_tags: List[str] = field(default_factory=list)
    cause_detail: Dict[str, str] = field(default_factory=dict)
    pro_win_prob: Optional[float] = None


def _root_meta(root: Dict[str, Any]) -> Dict[str, Any]:
    """把 analysis.json 根节点与 exporter 首行的 meta 收敛成同一套键。"""
    meta = dict(root.get("meta") or {}) if isinstance(root, dict) else {}
    focus = root.get("focus") if isinstance(root, dict) else None
    if isinstance(focus, dict):
        meta.setdefault("focus_player_name", focus.get("name"))
        meta.setdefault("focus_player", focus.get("steamid64"))
    if isinstance(root, dict) and root.get("map"):
        meta.setdefault("map", root.get("map"))
    return meta


def _focus_side_for_half(root: Dict[str, Any], half: str) -> str:
    focus = root.get("focus") if isinstance(root, dict) else None
    if not isinstance(focus, dict):
        return ""
    for note in focus.get("sides") or []:
        if not isinstance(note, dict):
            continue
        if str(note.get("half") or "") == half:
            return norm_side(note.get("side"))
    return ""


def _normalize_econ(raw: Any) -> Tuple[Dict[str, Dict[str, Any]], Dict[str, str]]:
    econ: Dict[str, Dict[str, Any]] = {}
    buys: Dict[str, str] = {}
    if isinstance(raw, dict):
        items: List[Tuple[str, Any]] = list(raw.items())
    elif isinstance(raw, list):
        items = [(str(e.get("side") or ""), e) for e in raw if isinstance(e, dict)]
    else:
        items = []
    for key, val in items:
        side = norm_side(key)
        if side not in ("T", "CT") or not isinstance(val, dict):
            continue
        buy = norm_buy(pick(val, "buy", "buy_type", "type", "buy_type_cn"))
        equip = as_int(pick(val, "equip_value", "equipment_value", "equip", "value"))
        cash = as_int(pick(val, "money", "cash", "money_total"))
        avg = as_int(pick(val, "avg_equip", "avg", "equip_avg"))
        if avg is None and equip is not None:
            players = as_int(pick(val, "players", "alive", "count"))
            if players:
                avg = int(round(equip / players))
        econ[side] = {"buy": buy, "equip": equip, "money": cash, "avg": avg}
        if buy:
            buys[side] = buy
    return econ, buys


def _normalize_alive(raw: Any) -> List[Dict[str, int]]:
    out: List[Dict[str, int]] = []
    if not isinstance(raw, list):
        return out
    for item in raw:
        if isinstance(item, dict):
            tick = as_int(pick(item, "tick", "tick_number"))
            t_alive = as_int(pick(item, "t_alive", "t_alive_count", "terrorists", "t"))
            ct_alive = as_int(pick(item, "ct_alive", "ct_alive_count", "counter_terrorists", "ct"))
        elif isinstance(item, (list, tuple)) and len(item) >= 3:
            tick = as_int(item[0])
            t_alive = as_int(item[1])
            ct_alive = as_int(item[2])
        else:
            continue
        if tick is None or t_alive is None or ct_alive is None:
            continue
        out.append({"tick": tick, "T": t_alive, "CT": ct_alive})
    out.sort(key=lambda x: x["tick"])
    return out


def _normalize_bombs(raw: Any) -> List[Dict[str, Any]]:
    if isinstance(raw, list):
        return [e for e in raw if isinstance(e, dict)]
    if isinstance(raw, dict):
        events = pick(raw, "events", "list")
        if isinstance(events, list) and events:
            return [e for e in events if isinstance(e, dict)]
        out: List[Dict[str, Any]] = []
        site = pick(raw, "site", "bombsite")
        player = pick(raw, "player", "player_name")
        if raw.get("planted") or raw.get("plant") or pick(raw, "plant_tick"):
            out.append({"kind": "plant", "tick": pick(raw, "plant_tick", "tick"), "site": site, "player": player})
        if raw.get("defused") or pick(raw, "defuse_tick"):
            # 拆包者只认 defuser 字段：raw["player"] 往往是下包的人，不能顺手复用
            out.append({
                "kind": "defuse",
                "tick": pick(raw, "defuse_tick"),
                "player": pick(raw, "defuser", "defuser_name"),
            })
        if raw.get("exploded") or pick(raw, "explode_tick"):
            out.append({"kind": "explode", "tick": pick(raw, "explode_tick")})
        return out
    return []


def _normalize_trades(raw: Any) -> List[Dict[str, Any]]:
    if not isinstance(raw, list):
        return []
    out = []
    for t in raw:
        if not isinstance(t, dict):
            continue
        delay = as_float(pick(t, "delay_sec", "delay", "trade_delay_sec"))
        # 超过补枪窗口的「补枪」其实是后续击杀，留着只会误导模型
        if delay is not None and delay > 8.0:
            continue
        out.append(t)
    out.sort(key=lambda x: as_int(pick(x, "tick")) or 0)
    return out


def _normalize_causes(raw_tags: Any, raw_detail: Any) -> Tuple[List[str], Dict[str, str]]:
    tags: List[str] = []
    if isinstance(raw_tags, str):
        tags = [raw_tags]
    elif isinstance(raw_tags, list):
        tags = [str(t) for t in raw_tags if t]
    detail: Dict[str, str] = {}
    if isinstance(raw_detail, dict):
        for tag, text in raw_detail.items():
            if isinstance(text, str) and text.strip():
                detail[str(tag)] = text.strip()
            elif isinstance(text, dict):
                t = pick(text, "text", "detail", "action", "evidence")
                if isinstance(t, str) and t.strip():
                    detail[str(tag)] = t.strip()
    elif isinstance(raw_detail, list):
        for item in raw_detail:
            if not isinstance(item, dict):
                continue
            tag = pick(item, "tag", "cause", "label")
            text = pick(item, "text", "detail", "action", "evidence")
            if tag and isinstance(text, str) and text.strip():
                detail[str(tag)] = text.strip()
    return tags, detail


def normalize_round(rec: Dict[str, Any], root: Dict[str, Any], sticky: Dict[str, Any]) -> Optional[RoundFacts]:
    """把一条 exporter 回合记录（或 analysis.rounds[i]）收敛成 RoundFacts。"""
    root_meta = sticky.get("root_meta") or _root_meta(root)
    meta = root_meta
    rec_meta = rec.get("meta")
    if isinstance(rec_meta, dict) and rec_meta:
        meta = dict(root_meta)
        meta.update(rec_meta)

    ctx = rec.get("context") if isinstance(rec.get("context"), dict) else {}
    if ctx:
        sticky["context"] = ctx  # 后续行可能省略 context，沿用上一行的
    else:
        ctx = sticky.get("context") or {}

    round_no = as_int(pick(ctx, "round", "number", "round_number"))
    if round_no is None:
        round_no = as_int(pick(rec, "round", "number", "round_number"))
    if round_no is None:
        return None

    tick_range = pick(ctx, "tick_range") or pick(rec, "tick_range")
    tick_start = tick_end = 0
    if isinstance(tick_range, (list, tuple)) and len(tick_range) >= 2:
        tick_start = as_int(tick_range[0]) or 0
        tick_end = as_int(tick_range[1]) or 0
    if not tick_start:
        tick_start = as_int(pick(ctx, "tick_start", "start_tick")) or as_int(pick(rec, "tick_start", "start_tick")) or 0
    if not tick_end:
        tick_end = as_int(pick(ctx, "tick_end", "end_tick")) or as_int(pick(rec, "tick_end", "end_tick")) or 0

    outcome = rec.get("outcome") if isinstance(rec.get("outcome"), dict) else {}
    economy_raw = pick(rec, "economy") or pick(ctx, "economy") or {}
    econ, buys = _normalize_econ(economy_raw)
    buys_raw = pick(rec, "buys")
    if isinstance(buys_raw, dict):
        for key, val in buys_raw.items():
            side = norm_side(key)
            if side in ("T", "CT"):
                buys[side] = norm_buy(val)

    opening = pick(rec, "opening", "opening_duel")
    opening = opening if isinstance(opening, dict) else None
    focus_death = pick(rec, "focus_death", "focus_death_info")
    focus_death = focus_death if isinstance(focus_death, dict) else None
    clutch = pick(rec, "clutch", "clutch_info")
    clutch = clutch if isinstance(clutch, dict) else None

    utility = pick(rec, "utility")
    util_events: List[Dict[str, Any]] = []
    util_before_raw: Any = pick(rec, "util_before_contact")
    if isinstance(utility, dict):
        util_events = list(pick(utility, "events", "throws", "projectiles") or [])
        if util_before_raw is None:
            util_before_raw = pick(utility, "before_contact", "util_before_contact")
    elif isinstance(utility, list):
        util_events = [e for e in utility if isinstance(e, dict)]
    util_before: Dict[str, int] = {}
    if isinstance(util_before_raw, dict):
        for key, val in util_before_raw.items():
            side = norm_side(key)
            n = as_int(val)
            if side in ("T", "CT") and n is not None:
                util_before[side] = n

    halves = str(pick(ctx, "half") or pick(rec, "half") or "")
    focus_side = norm_side(
        pick(meta, "focus_side", "focus_team")
        or pick(ctx, "focus_side")
        or pick(rec, "focus_side")
        or _focus_side_for_half(root, halves)
    )
    winner_side = norm_side(pick(outcome, "winner_side", "winner") or pick(rec, "winner", "winner_side"))
    focus_won_raw = pick(outcome, "focus_won")
    if focus_won_raw is None:
        focus_won_raw = pick(rec, "focus_won")
    focus_won = bool(focus_won_raw) if isinstance(focus_won_raw, bool) else None
    if focus_won is None and winner_side and focus_side and winner_side != "draw":
        focus_won = winner_side == focus_side  # 由「胜方 + 焦点阵营」推得，不是猜
    if not focus_side and focus_won is not None and winner_side in ("T", "CT"):
        focus_side = winner_side if focus_won else ("CT" if winner_side == "T" else "T")

    cause_tags, cause_detail = _normalize_causes(
        pick(outcome, "cause_tags", "causes") or pick(rec, "causes", "cause_tags"),
        pick(outcome, "cause_detail", "cause_details", "cause_actions", "cause_action")
        or pick(rec, "cause_detail", "cause_actions"),
    )

    focus_econ = economy_raw.get("focus") if isinstance(economy_raw, dict) else None
    buy_focus = norm_buy(
        pick(rec, "focus_buy")
        or (focus_econ if isinstance(focus_econ, str) else "")
        or pick(focus_econ if isinstance(focus_econ, dict) else {}, "buy", "buy_type")
        or pick(economy_raw or {}, "focus_buy")
        or pick(econ.get(focus_side) or {}, "buy")
        or ""
    )

    f = RoundFacts(
        round_no=round_no,
        match_id=str(pick(meta, "match_id", "demo_id", "id") or ""),
        map_name=str(pick(meta, "map", "map_name") or ""),
        focus_name=str(pick(meta, "focus_player_name", "focus_name", "player_name") or ""),
        focus_side=focus_side,
        half=halves,
        tick_start=tick_start,
        tick_end=tick_end,
        score_before=score_of(pick(ctx, "score_before", "score_start") or pick(rec, "score_before")),
        score_after=score_of(pick(ctx, "score_after", "score_end") or pick(rec, "score_after")),
        winner_side=winner_side,
        focus_won=focus_won,
        end_reason=str(pick(outcome, "end_reason", "reason") or pick(rec, "end_reason") or ""),
        win_cost=as_float(pick(outcome, "win_cost", "wincost") or pick(rec, "win_cost")),
        buys=buys,
        buy_focus=buy_focus,
        econ=econ,
        opening=opening,
        trades=_normalize_trades(pick(rec, "trades")),
        focus_death=focus_death,
        clutch=clutch,
        man_adv=[x for x in (pick(rec, "man_adv", "man_advantage") or []) if isinstance(x, dict)][:MAX_MAN_ADV],
        util_before=util_before,
        util_events=[e for e in util_events if isinstance(e, dict)],
        alive=_normalize_alive(pick(rec, "alive", "alive_series")),
        bombs=_normalize_bombs(pick(rec, "bomb", "bombs")),
        evidence=[e for e in (pick(rec, "evidence") or []) if isinstance(e, dict)],
        cause_tags=cause_tags,
        cause_detail=cause_detail,
    )
    # tick 区间缺失时，用本回合事实里出现的最小/最大 tick 兜底（是事实的最小上界，不是编造）
    if not f.tick_start or not f.tick_end:
        ticks = _fact_ticks(f)
        if ticks:
            f.tick_start = f.tick_start or min(ticks)
            f.tick_end = f.tick_end or max(ticks)
    return f


def _fact_ticks(f: RoundFacts) -> List[int]:
    out = []
    for d in [f.opening, f.focus_death, f.clutch] + f.trades + f.man_adv + f.bombs + f.evidence:
        if isinstance(d, dict):
            t = as_int(pick(d, "tick", "start_tick", "plant_tick"))
            if t:
                out.append(t)
    for a in f.alive:
        out.append(a["tick"])
    for e in f.util_events:
        t = as_int(pick(e, "tick", "throw_tick"))
        if t:
            out.append(t)
    return out


# --------------------------------------------------------------------------
# prompt 渲染：只写事实 + 可引用 tick
# --------------------------------------------------------------------------


def rules_text(f: RoundFacts, max_seq_length: int) -> str:
    return (
        "你是 CS2 赛后复盘教练，正在复盘一个回合。下面「本回合事实」是唯一事实来源，"
        "你只解读它，不许发明。\n\n"
        "【硬规则】违反任意一条即为废稿\n"
        "1. 只能使用「本回合事实」里的内容；事件、数字、点位、玩家行为都不许编，"
        "事实里没有的写「数据里没有」。\n"
        "2. 每条结论后必须紧跟引用，格式固定：回合R [tick T]。R 只能是 {r}，"
        "T 只能抄「可引用 tick」。\n"
        "3. 「下把怎么做」只写 1 个具体动作，必须带 demo_gototick T，"
        "T 同样只能抄「可引用 tick」。\n"
        "4. 数字只能照抄事实；不要用通用理论补数字。\n"
        "5. 全中文，直接、可执行、不客套；不写开场白，不复述提示词。\n\n"
        "【输出格式】严格照抄标题与顺序\n"
        "**回合R 结论**：<赢/输 + 主因> 回合R [tick T]\n"
        "## 发生了什么\n"
        "- <1-2 条客观事实，每条带引用>\n"
        "## 为什么\n"
        "- <用归因标签解释，不加新事实> 回合R [tick T]\n"
        "## 下把怎么做\n"
        "- <1 个具体动作> demo_gototick T\n"
    ).format(r=f.round_no)


def render_facts(f: RoundFacts) -> Tuple[str, Dict[str, Any]]:
    """渲染事实块 + 供 completion 复用的锚点（保证两边说的是同一批事实）。"""
    lines: List[str] = ["# 本回合事实（唯一事实来源；禁止发明数据之外的任何内容）", ""]
    used_ticks: Set[int] = set()
    anchors: Dict[str, Any] = {
        "opening_tick": None,
        "opening_line": "",
        "opening_focus_role": "",
        "death_tick": None,
        "death_line": "",
        "trade_tick": None,
        "trade_line": "",
        "clutch_tick": None,
        "clutch_line": "",
        "manadv_tick": None,
        "manadv_line": "",
        "bomb_tick": None,
        "bomb_line": "",
        "final_alive_line": "",
        "positive": [],
        "tick_start": f.tick_start or None,
        "ticks": used_ticks,
    }

    def add(text: str, tick: Optional[int] = None) -> None:
        if tick:
            used_ticks.add(int(tick))
            lines.append("- [tick {}] {}".format(int(tick), text))
        else:
            lines.append("- {}".format(text))

    # ---- 回合头（回合号 + tick 锚点必须在最前面）----
    lines.append("## 回合 {}".format(f.round_no))
    head_parts = []
    if f.map_name:
        head_parts.append("地图 {}".format(f.map_name))
    if f.match_id:
        head_parts.append("比赛 {}".format(f.match_id))
    if f.focus_name:
        side = "（{}）".format(f.focus_side) if f.focus_side in ("T", "CT") else ""
        head_parts.append("焦点玩家 {}{}".format(f.focus_name, side))
    if f.half:
        head_parts.append("半场 {}".format(f.half))
    if head_parts:
        lines.append("- " + "；".join(head_parts))
    if f.tick_start or f.tick_end:
        tick_text = "tick 区间：{} ~ {}".format(f.tick_start or "?", f.tick_end or "?")
        if f.tick_start:
            tick_text += "；回放入口：demo_gototick {}".format(f.tick_start)
        lines.append("- " + tick_text)
        if f.tick_start:
            used_ticks.add(f.tick_start)
        if f.tick_end:
            used_ticks.add(f.tick_end)
    else:
        lines.append("- tick 区间：数据里没有（不要写 [tick N]，也不要写 demo_gototick）")

    if f.score_after or f.score_before:
        if f.score_before and f.score_after:
            lines.append(
                "- 比分：回合前 T {} - CT {} → 回合后 T {} - CT {}".format(
                    f.score_before.get("T", "?"), f.score_before.get("CT", "?"),
                    f.score_after.get("T", "?"), f.score_after.get("CT", "?"),
                )
            )
        else:
            s = f.score_after or f.score_before
            lines.append("- 比分（回合后）：T {} - CT {}".format(s.get("T", "?"), s.get("CT", "?")))

    # ---- 经济 ----
    if f.econ or f.buys or f.buy_focus:
        lines.append("")
        lines.append("## 经济与买型")
        for side in ("T", "CT"):
            e = f.econ.get(side) or {}
            buy = e.get("buy") or f.buys.get(side) or ""
            parts = []
            if buy:
                parts.append("买型 {}{}".format(buy, "（{}）".format(BUY_CN[buy]) if buy in BUY_CN else ""))
            if e.get("avg") is not None:
                parts.append("人均装备 {}".format(money(e["avg"])))
            elif e.get("equip") is not None:
                parts.append("队伍装备 {}".format(money(e["equip"])))
            if e.get("money") is not None:
                parts.append("回合开始现金 {}".format(money(e["money"])))
            if parts:
                lines.append("- {}：{}".format(side, "；".join(parts)))
        if f.buy_focus:
            lines.append("- 焦点方买型：{}".format(f.buy_focus))

    # ---- 开局对枪 ----
    op = f.opening or {}
    if op:
        lines.append("")
        lines.append("## 开局对枪")
        killer = pick(op, "killer_name", "killer", "attacker_name", "attacker") or "数据里没有"
        victim = pick(op, "victim_name", "victim") or "数据里没有"
        kside = norm_side(pick(op, "killer_side", "attacker_side"))
        vside = norm_side(pick(op, "victim_side"))
        weapon = pick(op, "weapon", "weapon_name")
        traded = pick(op, "traded_within_window", "traded")
        role_raw = str(pick(op, "focus_role") or "none").lower()
        role = {"killer": "凶手", "victim": "被击杀", "assist": "助攻"}.get(role_raw, "无")
        who = "{}（{}）".format(killer, kside) if kside in ("T", "CT") else killer
        vic = "{}（{}）".format(victim, vside) if vside in ("T", "CT") else victim
        if weapon:
            text = "开局对枪：{}用 {} 击杀 {}".format(who, weapon, vic)
            short = "{}用 {} 击杀 {}".format(who, weapon, vic)
        else:
            text = "开局对枪：{}击杀 {}".format(who, vic)
            short = "{}击杀 {}".format(who, vic)
        if isinstance(traded, bool):
            text += "；5 秒内被补：{}".format(yes_no(traded))
        text += "；焦点玩家角色：{}".format(role)
        tick = as_int(pick(op, "tick"))
        add(text, tick)
        anchors.update({
            "opening_tick": tick,
            "opening_line": "{}，5 秒内{}。".format(
                short,
                "被补掉" if traded is True else ("没有被补掉" if traded is False else "补枪情况数据里没有"),
            ),
            "opening_focus_role": role_raw,
        })
        if role_raw == "killer":
            anchors["positive"].append({"text": "焦点玩家自己拿到首杀，开局就占了人数优势。", "tick": tick})

    # ---- 补枪 ----
    if f.trades:
        lines.append("")
        lines.append("## 补枪（队友倒地后 5 秒窗口）")
        for t in f.trades[:MAX_TRADES]:
            victim = pick(t, "victim_name", "victim") or "数据里没有"
            killer = pick(t, "killed_name", "killer", "killer_name") or "数据里没有"
            avenger = pick(t, "avenger_name", "avenger") or "无人"
            delay = as_float(pick(t, "delay_sec", "delay"))
            ok = pick(t, "success", "traded")
            tick = as_int(pick(t, "tick"))
            text = "{} 死后 {} 秒 {} 击杀 {}：{}".format(
                victim,
                "{:.1f}".format(delay) if delay is not None else "?",
                avenger,
                killer,
                "补枪成功" if ok is True else ("补枪失败" if ok is False else "结果数据里没有"),
            )
            add(text, tick)
            if anchors["trade_tick"] is None or ok is False:
                if ok is True:
                    trade_state = "被队友补掉"
                elif ok is False:
                    trade_state = "没人补枪"
                else:
                    trade_state = "补枪结果数据里没有"
                anchors["trade_tick"] = tick or anchors["trade_tick"]
                anchors["trade_line"] = "{} 死后 {} 秒{}。".format(
                    victim,
                    "{:.1f}".format(delay) if delay is not None else "?",
                    trade_state,
                )

    # ---- 焦点玩家死亡 ----
    d = f.focus_death or {}
    if d:
        lines.append("")
        lines.append("## 焦点玩家死亡")
        killer = pick(d, "killer_name", "killer") or "数据里没有/世界"
        kside = norm_side(pick(d, "killer_side"))
        weapon = pick(d, "weapon", "weapon_name")
        pos = pick(d, "pos_label", "position_label") or "点位数据里没有"
        rtime = as_float(pick(d, "round_time", "time"))
        util_held = as_int(pick(d, "utility_held", "utility_at_death"))
        flags = []
        if pick(d, "headshot"):
            flags.append("爆头")
        if pick(d, "through_smoke"):
            flags.append("穿烟")
        if pick(d, "traded") is True:
            flags.append("被队友补")
        if pick(d, "is_first_death"):
            flags.append("首死")
        killer_text = "{}（{}）".format(killer, kside) if kside in ("T", "CT") else killer
        if weapon:
            text = "焦点玩家死亡：被 {}用 {} 击杀于 {}".format(killer_text, weapon, pos)
        else:
            text = "焦点玩家死亡：被 {}击杀于 {}".format(killer_text, pos)
        if rtime is not None:
            text += "（回合内 {:.1f} 秒）".format(rtime)
        if util_held is not None:
            text += "；死时剩 {} 个道具".format(util_held)
        if flags:
            text += "；" + "、".join(flags)
        tick = as_int(pick(d, "tick"))
        add(text, tick)
        anchors["death_tick"] = tick
        anchors["death_line"] = (
            "焦点玩家在{}被 {}用 {} 击杀{}。".format(
                "回合内 {:.1f} 秒".format(rtime) if rtime is not None else "回合中",
                killer_text,
                weapon or "未知武器",
                "，死时还剩 {} 个道具".format(util_held) if util_held else "",
            )
        )

    # ---- 残局 ----
    c = f.clutch or {}
    if c:
        lines.append("")
        lines.append("## 残局")
        situation = pick(c, "situation") or "数据里没有"
        result = str(pick(c, "result") or "")
        result_cn = {"won": "赢", "lost": "输", "drawn": "平", "draw": "平"}.get(result, result or "数据里没有")
        winner = norm_side(pick(c, "winner_side", "winner"))
        focus_in = pick(c, "focus_in_clutch")
        adv = pick(c, "advantageous")
        hp_focus = as_int(pick(c, "hp_focus"))
        hp_opp = as_int(pick(c, "hp_opp_min"))
        text = "残局 {}：结果 {}，胜方 {}，焦点在残局：{}，开局占优：{}".format(
            situation, result_cn, winner or "数据里没有", yes_no(focus_in), yes_no(adv)
        )
        if hp_focus is not None or hp_opp is not None:
            text += "，血量 {} vs {}".format(hp_focus if hp_focus is not None else "?", hp_opp if hp_opp is not None else "?")
        tick = as_int(pick(c, "start_tick", "tick"))
        add(text, tick)
        anchors["clutch_tick"] = tick
        anchors["clutch_line"] = "残局 {}，结果{}。".format(situation, result_cn)
        if focus_in is True and result in ("won",):
            anchors["positive"].append({"text": "焦点方赢下了 {} 残局。".format(situation), "tick": tick})

    # ---- 人数优势 ----
    manadv_header = False
    for sp in f.man_adv:
        adv_side = norm_side(pick(sp, "adv_side", "side"))
        delta = as_int(pick(sp, "adv_delta", "delta"))
        threw = pick(sp, "threw")
        start = as_int(pick(sp, "start_tick", "tick"))
        end = as_int(pick(sp, "end_tick"))
        if not adv_side or delta is None:
            continue
        if not manadv_header:
            lines.append("")
            lines.append("## 人数优势")
            manadv_header = True
        text = "人数优势：{} +{}（{} ~ {}），{}".format(
            adv_side, delta, start or "?", end or "?", "被翻盘" if threw is True else "保持到回合结束"
        )
        add(text, start)
        if end:
            used_ticks.add(end)
        anchors["manadv_tick"] = start
        anchors["manadv_line"] = "回合里 {} 曾经 +{} 人（{} ~ {}），{}。".format(
            adv_side, delta, start or "?", end or "?", "最后被翻盘" if threw is True else "保持到最后"
        )
        if threw is False and focus_side_matches(f, adv_side):
            anchors["positive"].append({"text": "回合里拿到 {} 人优势并保持到最后。".format(delta), "tick": start})

    # ---- 交火前道具 ----
    if f.util_before and sum(f.util_before.values()) > 0:
        lines.append("")
        lines.append("## 交火前道具")
        lines.append("- " + " / ".join("{} {}".format(s, f.util_before[s]) for s in ("T", "CT") if s in f.util_before))

    # ---- 道具投掷 ----
    if f.util_events:
        lines.append("")
        lines.append("## 道具投掷")
        for e in f.util_events[:MAX_UTIL_EVENTS]:
            raw_type = str(pick(e, "type", "kind", "grenade", "name") or "").lower()
            util = UTIL_CN.get(raw_type, raw_type or "道具")
            thrower = pick(e, "thrower", "thrower_name", "player") or "数据里没有"
            land = pick(e, "land", "land_pos_label", "target", "area")
            tick = as_int(pick(e, "tick", "throw_tick"))
            text = "{} 投掷 {}".format(thrower, util)
            if land:
                text += " → {}".format(land)
            add(text, tick)

    # ---- 存活序列（只保留人数变化点）----
    if f.alive:
        lines.append("")
        lines.append("## 存活序列（只列人数变化）")
        prev = None
        parts = []
        for a in f.alive:
            pair = (a["T"], a["CT"])
            if pair == prev:
                continue
            prev = pair
            used_ticks.add(a["tick"])
            parts.append("[tick {}] {}v{}".format(a["tick"], a["T"], a["CT"]))
            if len(parts) >= MAX_ALIVE_STEPS:
                break
        lines.append("- " + "；".join(parts))
        if f.alive:
            last = f.alive[-1]
            anchors["final_alive_line"] = "终局存活 {}v{}。".format(last["T"], last["CT"])
            anchors["final_alive_tick"] = last["tick"]

    # ---- 炸弹 ----
    if f.bombs:
        lines.append("")
        lines.append("## 炸弹")
        for b in f.bombs[:3]:
            kind = str(pick(b, "kind", "type", "event") or "").lower()
            tick = as_int(pick(b, "tick", "plant_tick", "defuse_tick", "explode_tick"))
            site_raw = pick(b, "site", "bombsite")
            site = ""
            if site_raw:
                s = str(site_raw).lower().replace("bombsite", "").strip()
                site = "{} 点".format(s.upper()) if s else str(site_raw)
            player = pick(b, "player", "player_name")
            if kind in ("plant", "planted", "bomb_planted", "bombplant"):
                text = "下包" + ("：{}".format(player) if player else "")
                if site:
                    text += " 在 {}".format(site)
            elif kind in ("defuse", "defused", "bomb_defused", "bombdefused"):
                text = "拆包成功" + ("：{}".format(player) if player else "")
            elif kind in ("explode", "exploded", "bomb_exploded", "bombexploded"):
                text = "炸弹爆炸"
            elif kind in ("drop", "pickup"):
                text = "炸弹{}".format("掉落" if kind == "drop" else "被捡起")
            else:
                continue
            add(text, tick)
            if anchors["bomb_tick"] is None:
                anchors["bomb_tick"] = tick
                anchors["bomb_line"] = text.rstrip("。") + "。"

    # ---- 补充证据（未被结构化事实覆盖的部分）----
    extra = []
    for e in f.evidence:
        tick = as_int(pick(e, "tick"))
        text = pick(e, "text", "desc", "description")
        if not isinstance(text, str) or not text.strip():
            continue
        # 没有正 tick 的证据无法被引用，而且 tactics 层的这类文本里常带「tick 0」，
        # 留在 prompt 里只会教模型抄一个假锚点
        if tick is None or tick <= 0:
            continue
        if tick in used_ticks:
            continue
        kind = str(pick(e, "kind", "type") or "").lower()
        if kind == "alive" and anchors["final_alive_line"]:
            continue
        if kind == "clutch" and f.clutch:
            continue
        if kind == "bomb" and f.bombs:
            continue
        extra.append((tick, text.strip(), kind))
        if len(extra) >= MAX_EVIDENCE:
            break
    if extra:
        lines.append("")
        lines.append("## 其他事实")
        for tick, text, _kind in extra:
            add(text, tick)

    # ---- 结果 ----
    lines.append("")
    lines.append("## 结果")
    result_word = "丢分" if f.focus_won is False else ("赢下" if f.focus_won is True else "数据里没有")
    lines.append(
        "- 胜方：{}；焦点方：{}；结束原因：{}".format(
            f.winner_side or "数据里没有",
            result_word,
            END_REASON_CN.get(f.end_reason, f.end_reason or "数据里没有"),
        )
    )
    if f.cause_tags:
        parts = []
        for tag in f.cause_tags:
            label = CAUSE_LABEL.get(tag, tag)
            detail = f.cause_detail.get(tag)
            parts.append("{}（{}）{}".format(tag, label, "—— " + detail if detail else ""))
        lines.append("- 归因标签：" + "；".join(parts))
    else:
        lines.append("- 归因标签：（规则层没有给出）")

    # ---- B 层模型对照（可选）----
    if f.pro_win_prob is not None and f.focus_won is not None:
        actual = 1.0 if f.focus_won else 0.0
        dev = actual - f.pro_win_prob
        if abs(dev) < TEACHER_TICK_MIN_DEV:
            judge = "与结果基本一致"
        elif dev > 0:
            judge = "模型低估焦点方"
        else:
            judge = "模型高估焦点方"
        lines.append("")
        lines.append("## 模型对照（B 层 LightGBM 的胜率预测；只能当对照，不是比赛事实）")
        lines.append(
            "- 预测焦点方胜率 {:.2f}；实际焦点方{}；偏差 {:+.2f}（{}）".format(
                f.pro_win_prob, "赢" if f.focus_won else "输", dev, judge
            )
        )

    # ---- 可引用 tick ----
    lines.append("")
    lines.append("## 可引用 tick（completion 里 [tick N] 与 demo_gototick N 只能抄这里）")
    allowed = sorted(used_ticks)
    if allowed:
        text = "- 回合 {}：tick {}".format(f.round_no, "、".join(str(t) for t in allowed))
        if f.tick_start and f.tick_end:
            text += "（区间 {} ~ {}，回放入口 demo_gototick {}）".format(f.tick_start, f.tick_end, f.tick_start)
        lines.append(text)
    else:
        lines.append("- 回合 {}：数据里没有 tick；不要写 [tick N] / demo_gototick".format(f.round_no))

    anchors["ticks"] = used_ticks
    return "\n".join(lines), anchors


def focus_side_matches(f: RoundFacts, side: str) -> bool:
    return bool(side) and side == f.focus_side


# --------------------------------------------------------------------------
# teacher=rule：确定性拼装教练解读
# --------------------------------------------------------------------------


def _tag_anchor_tick(f: RoundFacts, tag: str, anchors: Dict[str, Any]) -> Optional[int]:
    for src in TAG_TICK_SOURCE.get(tag, ("tick_start",)):
        tick = anchors.get(src)
        if tick:
            return int(tick)
    ticks = anchors.get("ticks") or set()
    return min(ticks) if ticks else None


def _demo_tick(f: RoundFacts, anchors: Dict[str, Any]) -> Optional[int]:
    if f.tick_start:
        return int(f.tick_start)
    ticks = anchors.get("ticks") or set()
    return min(ticks) if ticks else None


def build_completion_rule(f: RoundFacts, anchors: Dict[str, Any]) -> str:
    """规则模板 teacher：只用 cause_tags + cause_detail + 已渲染的锚点，确定性拼装。"""
    start = _demo_tick(f, anchors)
    side = f.focus_side if f.focus_side in ("T", "CT") else "焦点方"
    tags = [t for t in f.cause_tags if t]
    end_cn = END_REASON_CN.get(f.end_reason, f.end_reason or "数据里没有")

    positives = [p for p in (anchors.get("positive") or []) if isinstance(p, dict) and p.get("text")]

    if f.focus_won is False:
        if tags:
            primary = tags[0]
            concl = "焦点方（{}）丢掉了回合{}，主因是{}。".format(side, f.round_no, CAUSE_LABEL.get(primary, primary))
        else:
            concl = "焦点方（{}）丢掉了回合{}；规则层没有给出归因标签，不猜原因。".format(side, f.round_no)
    elif f.focus_won is True:
        reason = "没有出现规则层标记的丢分问题"
        if anchors.get("opening_focus_role") == "killer":
            reason = "开局首杀建立人数优势"
        elif positives and "残局" in positives[0]["text"]:
            reason = "残局处理到位"
        concl = "焦点方（{}）赢下回合{}（{}），{}。".format(side, f.round_no, end_cn, reason)
    else:
        concl = "回合{} 没有分出胜负（{}）。".format(f.round_no, end_cn)

    # 结论优先引用最相关的事实：丢分引主因标签的锚点，赢的引第一个正面事实，其余退回回合入口
    if f.focus_won is False and tags:
        concl_tick = _tag_anchor_tick(f, tags[0], anchors) or start
    elif positives:
        concl_tick = positives[0].get("tick") or start
    else:
        concl_tick = start
    out = ["**回合{} 结论**：{} {}".format(f.round_no, concl, cite(f.round_no, concl_tick)).rstrip(),
           "## 发生了什么"]

    # ---- 发生了什么：优先开局 / 焦点死亡 / 炸弹 / 残局，最多 3 条 ----
    happened: List[Tuple[str, Optional[int]]] = []
    if anchors.get("opening_line"):
        happened.append((anchors["opening_line"], anchors.get("opening_tick")))
    # 首杀就是焦点玩家自己时，开局行和死亡行是同一件事，只留一条
    if anchors.get("death_line") and not (
        anchors.get("opening_tick") and anchors.get("opening_tick") == anchors.get("death_tick")
    ):
        happened.append((anchors["death_line"], anchors.get("death_tick")))
    if anchors.get("bomb_line"):
        happened.append((anchors["bomb_line"], anchors.get("bomb_tick")))
    if anchors.get("clutch_line"):
        happened.append((anchors["clutch_line"], anchors.get("clutch_tick")))
    if anchors.get("manadv_line"):
        happened.append((anchors["manadv_line"], anchors.get("manadv_tick")))
    if anchors.get("final_alive_line"):
        happened.append((anchors["final_alive_line"], anchors.get("final_alive_tick")))
    if not happened:
        score_text = ""
        if f.score_after:
            score_text = "比分 T {} - CT {}；".format(f.score_after.get("T", "?"), f.score_after.get("CT", "?"))
        happened.append(("{}结束，焦点方{}。".format(score_text, "丢分" if f.focus_won is False else ("赢下" if f.focus_won is True else "结果数据里没有")), f.tick_start or None))
    for text, tick in happened[:MAX_FACTS_LINES]:
        out.append("- {} {}".format(text.rstrip("。") + "。", cite(f.round_no, tick or start)).replace("。。", "。"))

    # ---- 为什么 ----
    out.append("## 为什么")
    why_lines: List[Tuple[str, Optional[int]]] = []
    if f.focus_won is False:
        for tag in tags[:MAX_WHY_BULLETS]:
            label = CAUSE_LABEL.get(tag, tag)
            detail = f.cause_detail.get(tag)
            why = TAG_WHY.get(tag, "规则层把这一回合归到「{}」。".format(label))
            text = "{}：{}。{}".format(label, detail.rstrip("。") if detail else "本回合证据见上面的回合事实", why)
            why_lines.append((text, _tag_anchor_tick(f, tag, anchors)))
        if len(tags) > MAX_WHY_BULLETS:
            rest = tags[MAX_WHY_BULLETS:]
            parts = []
            for tag in rest:
                label = CAUSE_LABEL.get(tag, tag)
                detail = f.cause_detail.get(tag)
                parts.append("{}（{}）{}".format(label, tag, "—— " + detail if detail else ""))
            why_lines.append(("其他归因：" + "；".join(parts) + "。", _tag_anchor_tick(f, rest[0], anchors)))
        if not tags:
            why_lines.append((
                "规则层没有给出归因标签，无法判断具体错在哪；能确定的只有结果：焦点方丢了这一分。",
                start,
            ))
    elif f.focus_won is True:
        for item in positives:
            why_lines.append((item["text"], item.get("tick") or start))
        if not why_lines:
            why_lines.append((
                "规则层没有给出任何丢分归因标签，这一回合没有出现首杀白给、补枪失败、道具缺口、经济强起、残局丢分这些已知问题。",
                start,
            ))
    else:
        why_lines.append(("回合没有分出胜负，没有可归因的丢分。", start))

    # B 层偏差：只在偏差够大时补一句（数据里没有预测就不写）
    if f.pro_win_prob is not None and f.focus_won is not None:
        dev = (1.0 if f.focus_won else 0.0) - f.pro_win_prob
        if abs(dev) >= TEACHER_TICK_MIN_DEV:
            if f.focus_won is False and f.pro_win_prob >= 0.5:
                why_lines.append((
                    "B 层模型给这回合的胜率是 {:.2f}，数据面上是优势局，丢在这里比丢在劣势局更伤。".format(f.pro_win_prob),
                    start,
                ))
            elif f.focus_won is True and f.pro_win_prob <= 0.5:
                why_lines.append((
                    "B 层模型只给这回合 {:.2f} 的胜率，这一分是逆着预测拿下的，值得保留。".format(f.pro_win_prob),
                    start,
                ))
    for text, tick in why_lines[: MAX_WHY_BULLETS + 1]:
        out.append("- {} {}".format(text.rstrip("。") + "。", cite(f.round_no, tick or start)).replace("。。", "。"))

    # ---- 下把怎么做 ----
    out.append("## 下把怎么做")
    if f.focus_won is False:
        if tags:
            action = TAG_ACTION.get(tags[0], "先回放这一回合，确认问题再改。")
        else:
            action = "先守住默认架点，不在人数劣势时接团；下一场把这一回合单独回放确认问题。"
    elif f.focus_won is True:
        if anchors.get("opening_focus_role") == "killer":
            action = "继续用这回合的开局：先抢首杀、拿到 5v4 再提速进点。"
        elif positives and "残局" in positives[0]["text"]:
            action = "残局继续按这回合的打法：先数人数，占优就合围，不给 1v1。"
        else:
            action = "保持这回合的买型和默认站位，别因为赢了一分就改成强起或单独找人。"
    else:
        action = "按默认位打，先保证人数不亏再谈提速。"

    if start:
        out.append("- {} demo_gototick {}".format(action, start))
    else:
        out.append("- {}（demo_gototick：数据里没有 tick）".format(action))

    return "\n".join(out)


# --------------------------------------------------------------------------
# 校验：completion 不许引用 prompt 之外的 tick / 回合
# --------------------------------------------------------------------------


def validate_completion(text: str, f: RoundFacts, allowed_ticks: Iterable[int]) -> List[str]:
    allowed = {int(t) for t in allowed_ticks}
    issues: List[str] = []
    for raw in TICK_RE.findall(text):
        if int(raw) not in allowed:
            issues.append("tick {} 不在可引用清单".format(raw))
    for raw in GOTO_RE.findall(text):
        if int(raw) not in allowed:
            issues.append("demo_gototick {} 不在可引用清单".format(raw))
    for raw in CITE_ROUND_RE.findall(text):
        if int(raw) != f.round_no:
            issues.append("引用回合 {} 不是本回合 {}".format(raw, f.round_no))
    return issues


def completion_ticks(text: str) -> Set[int]:
    return {int(t) for t in TICK_RE.findall(text)} | {int(t) for t in GOTO_RE.findall(text)}


# --------------------------------------------------------------------------
# 输入读取 / 概率文件 / 切分 / 输出
# --------------------------------------------------------------------------


def iter_input(path: Path) -> Iterator[Tuple[Dict[str, Any], Dict[str, Any], str]]:
    """遍历输入，产出 (root_meta, round_record, 来源名)。JSONL 逐行且继承首行 meta。"""
    files: List[Path]
    if path.is_dir():
        files = sorted([p for p in path.iterdir() if p.suffix.lower() in (".jsonl", ".json")])
    else:
        files = [path]
    for file in files:
        sticky: Dict[str, Any] = {}
        if file.suffix.lower() == ".jsonl":
            with file.open("r", encoding="utf-8") as fh:
                for lineno, line in enumerate(fh, 1):
                    line = line.strip()
                    if not line or line.startswith("#"):
                        continue
                    try:
                        rec = json.loads(line)
                    except json.JSONDecodeError as exc:
                        raise SystemExit("{}:{} JSON 解析失败：{}".format(file, lineno, exc))
                    if not isinstance(rec, dict):
                        continue
                    # exporter 可能只在首行写 meta；后续行继承
                    if isinstance(rec.get("meta"), dict) and rec["meta"]:
                        sticky["meta"] = dict(sticky.get("meta") or {})
                        sticky["meta"].update(rec["meta"])
                    root = {"meta": sticky.get("meta") or {}}
                    yield root, rec, "{}:{}".format(file.name, lineno)
        else:
            with file.open("r", encoding="utf-8") as fh:
                try:
                    doc = json.load(fh)
                except json.JSONDecodeError as exc:
                    raise SystemExit("{} JSON 解析失败：{}".format(file, exc))
            if isinstance(doc, dict) and isinstance(doc.get("rounds"), list):
                for r in doc["rounds"]:
                    if isinstance(r, dict):
                        yield doc, r, file.name
            elif isinstance(doc, list):
                for r in doc:
                    if isinstance(r, dict):
                        yield {}, r, file.name
            elif isinstance(doc, dict):
                yield {}, doc, file.name
        # sticky 只在一个文件内有效


def load_probs(path: Optional[Path]) -> Dict[str, float]:
    """读 B 层预测。支持：
    - JSONL: {"match_id": "...", "round": 7, "pro_win_prob": 0.71}
    - JSON  : {"<match_id>:<round>": 0.71} 或 {"probs": [ ... ]} 或 {"<round>": 0.71}
    """
    out: Dict[str, float] = {}
    if not path:
        return out

    def add(match_id: Any, rnd: Any, prob: Any) -> None:
        r = as_int(rnd)
        p = as_float(prob)
        if r is None or p is None:
            return
        out["{}:{}".format(str(match_id or ""), r)] = p

    def walk(obj: Any) -> None:
        if isinstance(obj, list):
            for item in obj:
                walk(item)
            return
        if not isinstance(obj, dict):
            return
        if ("round" in obj or "round_no" in obj) and any(
            k in obj for k in ("pro_win_prob", "pro_prob", "prob", "p")
        ):
            add(pick(obj, "match_id", "demo_id", "id"), pick(obj, "round", "round_no"),
                pick(obj, "pro_win_prob", "pro_prob", "prob", "p"))
            return
        for key, val in obj.items():
            if isinstance(val, bool):
                continue
            if isinstance(val, (int, float)):
                if ":" in str(key):
                    mid, rnd = str(key).rsplit(":", 1)
                    add(mid, rnd, val)
                else:
                    add("", key, val)
            elif isinstance(val, dict):
                if any(k in val for k in ("pro_win_prob", "pro_prob", "prob", "p")):
                    mid, _, rnd = str(key).rpartition(":")
                    add(mid, rnd or key, pick(val, "pro_win_prob", "pro_prob", "prob", "p"))
                elif any(isinstance(v2, (int, float)) and not isinstance(v2, bool) for v2 in val.values()):
                    for rnd, prob in val.items():
                        add(key, rnd, prob)
                else:
                    walk(val)
            elif isinstance(val, list):
                walk(val)

    text = path.read_text(encoding="utf-8")
    if path.suffix.lower() == ".jsonl":
        for line in text.splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                walk(json.loads(line))
            except json.JSONDecodeError:
                continue
    else:
        try:
            walk(json.loads(text))
        except json.JSONDecodeError:
            for line in text.splitlines():
                if line.strip():
                    walk(json.loads(line))
    return out


def lookup_prob(probs: Dict[str, float], f: RoundFacts) -> Optional[float]:
    for key in ("{}:{}".format(f.match_id, f.round_no), ":{}".format(f.round_no), "{}:{}".format("", f.round_no)):
        if key in probs:
            return probs[key]
    return None


def split_key(f: RoundFacts) -> str:
    return f.match_id or "__no_match__"


def bucket(key: str, salt: str, ratio: float) -> bool:
    h = hashlib.sha256(("{}|{}".format(salt, key)).encode("utf-8")).digest()
    return (int.from_bytes(h[:4], "big") / 2 ** 32) < ratio


def read_max_seq_length(config_path: Path) -> int:
    """只从 model.yaml 里抠 max_seq_length（不引入 YAML 依赖）。"""
    try:
        text = config_path.read_text(encoding="utf-8")
    except OSError:
        return 2048
    m = re.search(r"^\s*max_seq_length\s*:\s*(\d+)", text, re.MULTILINE)
    return int(m.group(1)) if m else 2048


def write_jsonl(path: Path, rows: Sequence[Dict[str, Any]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, ensure_ascii=False) + "\n")


# --------------------------------------------------------------------------
# LLM teacher hook（无硬依赖：任何「stdin 进 prompt、stdout 出 completion」的命令都行）
# --------------------------------------------------------------------------


def call_teacher_cmd(cmd: str, prompt: str, timeout: int) -> str:
    argv = shlex.split(cmd)
    if not argv:
        raise ValueError("--teacher-cmd 是空命令")
    proc = subprocess.run(argv, input=prompt, capture_output=True, text=True, timeout=timeout)
    if proc.returncode != 0:
        raise RuntimeError(
            "teacher 命令退出码 {}：{}".format(proc.returncode, (proc.stderr or "").strip()[:400])
        )
    text = (proc.stdout or "").strip()
    if not text:
        raise RuntimeError("teacher 命令没有输出（stdout 为空）")
    return text


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------


@dataclass
class Prepared:
    facts: RoundFacts
    system: str
    user_facts: str
    prompt: str
    completion: str
    source: str


def build_records(args: argparse.Namespace) -> Tuple[List[Prepared], Dict[str, int]]:
    probs = load_probs(Path(args.probs).expanduser() if args.probs else None)
    max_seq = read_max_seq_length(Path(args.config).expanduser())
    rows: List[Prepared] = []
    stats = {"records": 0, "no_round": 0, "no_ticks": 0, "bad_teacher": 0, "no_focus_side": 0, "too_long": 0}
    current_file = ""
    sticky_ctx: Dict[str, Any] = {}

    for root, rec, source in iter_input(Path(args.input).expanduser()):
        file_key = source.rsplit(":", 1)[0]
        if file_key != current_file:
            current_file, sticky_ctx = file_key, {}
        sticky = {"root_meta": _root_meta(root), "context": sticky_ctx}
        f = normalize_round(rec, root, sticky)
        sticky_ctx = sticky.get("context") or {}  # exporter 省略 context 时沿用上一条
        if f is None:
            stats["no_round"] += 1
            continue
        if args.match_id and not f.match_id:
            f.match_id = args.match_id
        if probs:
            f.pro_win_prob = lookup_prob(probs, f)
        if not f.focus_side:
            stats["no_focus_side"] += 1

        user_facts, anchors = render_facts(f)
        if not anchors["ticks"]:
            stats["no_ticks"] += 1
        system = rules_text(f, max_seq)
        prompt = system + "\n" + user_facts

        if args.teacher == "llm":
            if not args.teacher_cmd:
                raise RuntimeError("teacher=llm 但没有 --teacher-cmd（main 里应已拦截）")
            try:
                completion = call_teacher_cmd(args.teacher_cmd, prompt, args.teacher_timeout)
            except (RuntimeError, subprocess.TimeoutExpired, ValueError) as exc:
                if args.on_bad_teacher == "keep":
                    raise SystemExit("teacher 调用失败：{}".format(exc))
                print("! teacher 调用失败，跳过 {} 回合 {}：{}".format(f.match_id or "-", f.round_no, exc),
                      file=sys.stderr)
                stats["bad_teacher"] += 1
                continue
        else:
            completion = build_completion_rule(f, anchors)

        issues = validate_completion(completion, f, anchors["ticks"])
        if issues:
            if args.teacher == "rule":
                # 规则 teacher 引用越界是自己代码的 bug，直接报错，不许静默产出脏数据
                raise SystemExit("规则 teacher 产出非法引用（回合 {}）：{}".format(f.round_no, "；".join(issues)))
            if args.on_bad_teacher == "keep":
                pass
            elif args.on_bad_teacher == "warn":
                print("! 回合 {} 的 LLM completion 引用越界：{}".format(f.round_no, "；".join(issues)), file=sys.stderr)
            else:
                stats["bad_teacher"] += 1
                continue

        if len(prompt) + len(completion) > args.max_chars:
            stats["too_long"] += 1
        rows.append(Prepared(f, system, user_facts, prompt, completion, source))
        stats["records"] += 1
        if args.limit and stats["records"] >= args.limit:
            break
    return rows, stats


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        description="把 exporter 的每回合 JSONL 变成 A 层 SFT 数据（prompt/completion）",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument("--input", required=True, help="exporter 回合 JSONL 文件或目录（.jsonl/.json）")
    parser.add_argument("--out-dir", default=str(DEFAULT_OUT_DIR), help="输出目录")
    parser.add_argument("--config", default=str(DEFAULT_CONFIG), help="model.yaml（读 max_seq_length 做预算检查）")
    parser.add_argument("--teacher", choices=["rule", "llm"], default="rule",
                        help="rule=规则模板（确定性、免费）；llm=调用 --teacher-cmd")
    parser.add_argument("--teacher-cmd", default="", help="teacher=llm 时的外部命令（stdin→stdout）")
    parser.add_argument("--teacher-timeout", type=int, default=180, help="teacher 命令超时秒数")
    parser.add_argument("--on-bad-teacher", choices=["drop", "warn", "keep"], default="drop",
                        help="teacher 引用越界/调用失败时的处理")
    parser.add_argument("--probs", default="", help="B 层 pro_win_prob 文件（JSONL 或 JSON）")
    parser.add_argument("--valid-ratio", type=float, default=0.15, help="valid 比例（按 match 切分，防泄漏）")
    parser.add_argument("--seed", default="42", help="切分盐（同输入同参数输出完全一致）")
    parser.add_argument("--format", choices=["completion", "chat"], default="completion",
                        help="completion=prompt/completion 两键；chat=messages（交给模型 chat template）")
    parser.add_argument("--emit-meta", action="store_true", help="每行多写一个 mlx-lm 会忽略的 meta 键")
    parser.add_argument("--max-chars", type=int, default=6000, help="prompt+completion 超过此字符数计数告警")
    parser.add_argument("--limit", type=int, default=0, help="只处理前 N 条（调试用）")
    parser.add_argument("--match-id", default="", help="输入没有 match_id 时补一个（切分用）")
    args = parser.parse_args(argv)

    if not Path(args.input).expanduser().exists():
        print("输入不存在：{}".format(args.input), file=sys.stderr)
        return 2
    if args.valid_ratio < 0 or args.valid_ratio >= 1:
        print("--valid-ratio 必须在 [0,1) 内", file=sys.stderr)
        return 2
    if args.teacher == "llm" and not args.teacher_cmd.strip():
        print(
            "--teacher llm 需要 --teacher-cmd。示例：\n"
            "  --teacher llm --teacher-cmd \"ollama run qwen2.5:7b\"\n"
            "（命令从 stdin 读 prompt、往 stdout 写 completion）",
            file=sys.stderr,
        )
        return 2

    rows, stats = build_records(args)
    if args.limit and len(rows) > args.limit:
        rows = rows[: args.limit]
    if not rows:
        print("没有可用回合（缺回合号 {}，teacher 不合格 {}；检查输入格式，JSONL 每行一个回合）".format(
            stats.get("no_round", 0), stats.get("bad_teacher", 0)), file=sys.stderr)
        return 3

    rows.sort(key=lambda p: (p.facts.match_id, p.facts.round_no))
    train_rows, valid_rows = [], []
    for item in rows:
        target = valid_rows if bucket(split_key(item.facts), str(args.seed), args.valid_ratio) else train_rows
        if args.format == "chat":
            row: Dict[str, Any] = {
                "messages": [
                    {"role": "system", "content": item.system},
                    {"role": "user", "content": item.user_facts},
                    {"role": "assistant", "content": item.completion},
                ]
            }
        else:
            row = {"prompt": item.prompt, "completion": item.completion}
        if args.emit_meta:
            row["meta"] = {
                "match_id": item.facts.match_id,
                "round": item.facts.round_no,
                "map": item.facts.map_name,
                "focus_won": item.facts.focus_won,
                "cause_tags": item.facts.cause_tags,
                "source": item.source,
            }
        target.append(row)

    out_dir = Path(args.out_dir).expanduser()
    write_jsonl(out_dir / "train.jsonl", train_rows)
    if valid_rows:
        write_jsonl(out_dir / "valid.jsonl", valid_rows)
    else:
        stale = out_dir / "valid.jsonl"
        if stale.exists():
            stale.unlink()
        if args.valid_ratio > 0:
            print(
                "! valid 为空：可能只有一场比赛/缺 match_id，或切分哈希把全部 match 都分到了 train。"
                "按 match 切分是为了避免同一场比赛的回合同时进 train/valid；"
                "多攒几场 demo 后重跑 build_sft，或换 --seed，或显式 --valid-ratio 0 只出 train。",
                file=sys.stderr,
            )

    # ---- 统计 ----
    all_text = [item.prompt + item.completion for item in rows]
    est = [cjk_ratio_estimate(t) for t in all_text]
    max_seq = read_max_seq_length(Path(args.config))
    tag_counts: Dict[str, int] = {}
    for item in rows:
        for tag in item.facts.cause_tags:
            tag_counts[tag] = tag_counts.get(tag, 0) + 1
    wins = sum(1 for item in rows if item.facts.focus_won is True)
    losses = sum(1 for item in rows if item.facts.focus_won is False)

    print("写出 {} 条：train {} / valid {}".format(len(rows), len(train_rows), len(valid_rows)))
    print("  -> {}".format((out_dir / "train.jsonl")))
    if valid_rows:
        print("  -> {}".format((out_dir / "valid.jsonl")))
    print("回合分布：赢 {} / 丢 {} / 其它 {}".format(wins, losses, len(rows) - wins - losses))
    print("tag 分布：{}".format(
        "、".join("{} {}".format(k, v) for k, v in sorted(tag_counts.items(), key=lambda kv: -kv[1])) or "（无）"
    ))
    print("长度：prompt+completion 平均 {:.0f} 字符，最长 {} 字符；估算 token 平均 {:.0f}，最长 {}".format(
        statistics.mean(len(t) for t in all_text), max(len(t) for t in all_text),
        statistics.mean(est), max(est),
    ))
    budget = int(max_seq * 0.9)
    if max(est) > budget:
        print("! 最长样本估算 {} tokens 超过 max_seq_length {} 的 90%：训练会被截断，"
              "先压 prompt（减少证据条数）或把 max_seq_length 提到 3072".format(max(est), max_seq))
    dropped = stats.get("no_round", 0) + stats.get("bad_teacher", 0)
    print("丢弃：{}（缺回合号 {} / teacher 不合格 {}）".format(
        dropped, stats.get("no_round", 0), stats.get("bad_teacher", 0)))
    if stats.get("no_ticks"):
        print("! {} 条没有 tick（completion 只能写回合号，不能写 [tick N]）；"
              "建议 exporter 补齐 tick_range 后再训练".format(stats["no_ticks"]))
    if stats.get("no_focus_side"):
        print("! {} 条缺焦点阵营（结论里写「焦点方」）；exporter 的 meta.focus_side 建议补上".format(stats["no_focus_side"]))
    if stats.get("too_long"):
        print("! {} 条 prompt+completion 超过 {} 字符，检查是不是证据太多".format(stats["too_long"], args.max_chars))
    if stats.get("bad_teacher"):
        print("! {} 条 teacher 输出被丢弃（引用越界或调用失败）".format(stats["bad_teacher"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
