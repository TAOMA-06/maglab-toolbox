#!/usr/bin/env python3
"""winrate_data.py —— TRAIN-PLAN v2 T0/T1 的数据与特征层。

支持两种输入，统一成「局面快照 + 回合胜负标签」：

1. 回合级样本（现有 `cs2coach export` 的 samples.jsonl）：
   一行一个回合，特征 = 回合开始经济/买型/开局对枪/道具/存活/人数优势区间。
   这是 v0 的兼容路径，先保证管线跑通。

2. tick 级快照（推荐，TRAIN-PLAN v2 的主线）：
   - 直接读快照 JSONL（每行一个 tick 局面）；
   - 或读 `cs2coach run` 产出的 match.json（+ 同目录 analysis.json），
     用 Round.Alive / Kills / Utility / Bomb / Duels 生成 tick 快照。
   match.json 是 M2a 事实源；analysis.json 提供 M2b 的站位/对枪指标。
   生成过程只做「按 tick 取截至该 tick 的事实」，不推断、不补数。

标签统一为 `focus_won`（焦点方是否赢下本回合）；draw 默认丢弃。
切分统一按 `match_id`（优先 source_file）分组，避免同一场比赛泄漏到验证集。

本模块只依赖标准库；numpy / LightGBM 在 train_winrate.py 里按需导入。
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Set, Tuple

# --------------------------------------------------------------------------
# 特征列
# --------------------------------------------------------------------------

ROUND_CATEGORICAL = [
    "map",
    "half",
    "focus_buy",
    "enemy_buy",
    "first_killer_side",
    "first_victim_side",
]
ROUND_NUMERIC = [
    "round_number",
    "score_diff",
    "money_diff",
    "equip_diff",
    "t_equip_freeze_end",
    "ct_equip_freeze_end",
    "t_money",
    "ct_money",
    "first_kill_round_time",
    "has_first_kill",
    "traded_within_window",
    "focus_util_before_contact",
    "enemy_util_before_contact",
    "start_t_alive",
    "start_ct_alive",
    "utility_diff",
    "total_util",
    "eco_round",
    "force_round",
    "enemy_eco",
    "enemy_force",
    "man_disadvantage",
]
ROUND_FEATURES = ROUND_CATEGORICAL + ROUND_NUMERIC

SNAPSHOT_CATEGORICAL = ["map", "half", "focus_side", "bomb_site"]
SNAPSHOT_NUMERIC = [
    "round_number",
    "round_time",
    "round_progress",
    "focus_alive",
    "enemy_alive",
    "alive_diff",
    "focus_money",
    "enemy_money",
    "money_diff",
    "focus_equip",
    "enemy_equip",
    "equip_diff",
    "score_diff",
    "bomb_planted",
    "bomb_time_left",
    "kills_focus",
    "kills_enemy",
    "kill_diff",
    "first_blood_focus",
    "focus_dead",
    "util_focus",
    "util_enemy",
    "util_diff",
    "flash_focus",
    "smoke_focus",
    "he_focus",
    "molotov_focus",
    "flash_enemy",
    "smoke_enemy",
    "he_enemy",
    "molotov_enemy",
    "duel_preaim_delta_deg",
    "duel_moving_shot",
    "duel_outcome",
    "duel_first_shot_diff_sec",
    "duel_age_sec",
    "death_exposed_lines",
    "death_isolated",
    "death_cover_utility",
    "death_high_risk",
]
SNAPSHOT_FEATURES = SNAPSHOT_CATEGORICAL + SNAPSHOT_NUMERIC

# 明确排除的 outcome / 事后字段：绝不允许进特征
LEAKAGE_FIELDS = {
    "winner_side",
    "winner",
    "focus_won",
    "end_reason",
    "win_cost",
    "cause_tags",
    "causes",
    "cause_detail",
    "suspect",
    "suspect_tags",
    "planted",
    "defused",
    "exploded",
    "plant_site",
    "plant_round_time",
    "man_adv_threw",
    "man_adv_max_delta",
    "man_adv_spans",
    "t_money_spent",
    "ct_money_spent",
    "flash_count",
    "smoke_count",
    "he_count",
    "molotov_count",
    "he_damage_total",
    "is_clutch",
    "clutch_disadvantage_2vX",
}


# --------------------------------------------------------------------------
# 基础工具
# --------------------------------------------------------------------------


def _pick(d: Any, *names: str) -> Any:
    if not isinstance(d, dict):
        return None
    for name in names:
        if name in d:
            value = d[name]
            if value is not None and value != "":
                return value
    return None


def _as_int(value: Any, default: int = 0) -> int:
    if isinstance(value, bool):
        return int(value)
    if isinstance(value, (int, float)):
        return int(value)
    if isinstance(value, str):
        text = value.strip().replace(",", "")
        if not text:
            return default
        try:
            return int(float(text))
        except ValueError:
            return default
    return default


def _as_float(value: Any, default: float = 0.0) -> float:
    if isinstance(value, bool):
        return float(value)
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        text = value.strip().replace("%", "")
        if not text:
            return default
        try:
            return float(text)
        except ValueError:
            return default
    return default


def _as_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    return str(value or "").strip().lower() in {"1", "true", "yes", "on"}


def _norm_side(value: Any) -> str:
    text = str(value or "").strip().lower()
    if text in {"t", "terrorist", "terrorists", "2"}:
        return "T"
    if text in {"ct", "counter-terrorist", "counterterrorist", "3"}:
        return "CT"
    if text in {"draw", "tie", "none"}:
        return "draw"
    return ""


def _other_side(side: str) -> str:
    return "CT" if side == "T" else ("T" if side == "CT" else "")


def _score_value(score: Any, side: str) -> int:
    if not isinstance(score, dict):
        return 0
    return _as_int(score.get(side.lower(), score.get(side, 0)))


def read_jsonl(path: Path) -> List[Tuple[int, Dict[str, Any]]]:
    path = Path(path)
    rows: List[Tuple[int, Dict[str, Any]]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line_no, line in enumerate(handle, 1):
            if not line.strip():
                continue
            try:
                value = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(
                    "{}:{}: JSON 解析失败：{}".format(path, line_no, exc)
                ) from exc
            if not isinstance(value, dict):
                raise ValueError("{}:{}: 每行必须是 JSON object".format(path, line_no))
            rows.append((line_no, value))
    return rows


def match_id_of(row: Dict[str, Any]) -> str:
    """比赛分组键：优先 source_file（export 每条样本都带），其次 match_id/demo_id。"""
    for name in ("source_file", "match_id", "demo_id", "match", "file"):
        value = row.get(name)
        if value:
            return str(value)
    return "unknown"


def round_number_of(row: Dict[str, Any]) -> int:
    return _as_int(_pick(row, "round_number", "round", "number", "round_no"), 0)


def round_key_of(row: Dict[str, Any]) -> Tuple[str, int]:
    return match_id_of(row), round_number_of(row)


def label_of(row: Dict[str, Any], drop_draws: bool = True) -> Optional[int]:
    """返回 0/1；draw 或标签缺失返回 None。"""
    winner = _norm_side(_pick(row, "winner_side", "winner"))
    if drop_draws and winner == "draw":
        return None
    value = _pick(row, "focus_won")
    if isinstance(value, bool):
        return 1 if value else 0
    if isinstance(value, (int, float)):
        return 1 if value else 0
    if isinstance(value, str):
        text = value.strip().lower()
        if text in {"1", "true", "yes", "won", "win"}:
            return 1
        if text in {"0", "false", "no", "lost", "loss"}:
            return 0
    return None


# --------------------------------------------------------------------------
# 回合级特征（兼容现有 samples.jsonl）
# --------------------------------------------------------------------------


def _round_feature_row(row: Dict[str, Any]) -> Dict[str, Any]:
    feat: Dict[str, Any] = {}
    feat["map"] = str(row.get("map") or "unknown")
    feat["half"] = str(row.get("half") or "H1")
    feat["round_number"] = _as_int(row.get("round_number"), 0)
    score_before = row.get("score_before") or {}
    feat["score_diff"] = _as_int(score_before.get("t")) - _as_int(
        score_before.get("ct")
    )
    feat["money_diff"] = _as_int(row.get("t_money")) - _as_int(row.get("ct_money"))
    feat["equip_diff"] = _as_int(row.get("t_equip_freeze_end")) - _as_int(
        row.get("ct_equip_freeze_end")
    )
    feat["t_equip_freeze_end"] = _as_int(row.get("t_equip_freeze_end"))
    feat["ct_equip_freeze_end"] = _as_int(row.get("ct_equip_freeze_end"))
    feat["t_money"] = _as_int(row.get("t_money"))
    feat["ct_money"] = _as_int(row.get("ct_money"))
    feat["focus_buy"] = str(row.get("focus_buy") or "eco")
    feat["enemy_buy"] = str(row.get("enemy_buy") or "eco")
    feat["has_first_kill"] = int(_as_bool(row.get("has_first_kill")))
    feat["first_killer_side"] = _norm_side(row.get("first_killer_side"))
    feat["first_victim_side"] = _norm_side(row.get("first_victim_side"))
    feat["first_kill_round_time"] = _as_float(row.get("first_kill_round_time"))
    feat["traded_within_window"] = int(_as_bool(row.get("traded_within_window")))
    feat["focus_util_before_contact"] = _as_int(row.get("focus_util_before_contact"))
    feat["enemy_util_before_contact"] = _as_int(row.get("enemy_util_before_contact"))
    feat["start_t_alive"] = _as_int(row.get("start_t_alive"))
    feat["start_ct_alive"] = _as_int(row.get("start_ct_alive"))
    feat["utility_diff"] = (
        feat["focus_util_before_contact"] - feat["enemy_util_before_contact"]
    )
    feat["total_util"] = (
        feat["focus_util_before_contact"] + feat["enemy_util_before_contact"]
    )
    feat["eco_round"] = int(feat["focus_buy"] == "eco")
    feat["force_round"] = int(feat["focus_buy"] == "force")
    feat["enemy_eco"] = int(feat["enemy_buy"] == "eco")
    feat["enemy_force"] = int(feat["enemy_buy"] == "force")
    feat["man_disadvantage"] = int(
        feat["start_t_alive"] > 0
        and feat["start_ct_alive"] > 0
        and (feat["start_t_alive"] < 2 or feat["start_ct_alive"] < 2)
    )
    return feat


# --------------------------------------------------------------------------
# match.json → tick 快照
# --------------------------------------------------------------------------


def _focus_side_for_round(rnd: Dict[str, Any], focus_id: str) -> str:
    for player in rnd.get("players") or []:
        if not isinstance(player, dict):
            continue
        if str(player.get("steamid64") or "") == focus_id:
            return _norm_side(player.get("side"))
    return ""


def _roster_counts(rnd: Dict[str, Any]) -> Tuple[int, int]:
    t_alive = 0
    ct_alive = 0
    for player in rnd.get("players") or []:
        side = _norm_side((player or {}).get("side"))
        if side == "T":
            t_alive += 1
        elif side == "CT":
            ct_alive += 1
    return t_alive, ct_alive


def _alive_timeline(
    rnd: Dict[str, Any],
    start_tick: int,
    roster_t: int,
    roster_ct: int,
) -> List[Dict[str, int]]:
    """返回按 tick 升序的存活序列；缺失时用击杀事实推导（每死一人减一）。"""
    raw = rnd.get("alive") or []
    timeline: Dict[int, Tuple[int, int]] = {}
    for item in raw if isinstance(raw, list) else []:
        if not isinstance(item, dict):
            continue
        tick = _as_int(item.get("tick"), -1)
        if tick < 0:
            continue
        timeline[tick] = (
            _as_int(item.get("t_alive"), roster_t),
            _as_int(item.get("ct_alive"), roster_ct),
        )
    if start_tick not in timeline:
        timeline[start_tick] = (roster_t, roster_ct)
    if not raw:
        # 没有 alive 序列时，用击杀事实推导；只减不增，不补任何不存在的击杀。
        t_alive, ct_alive = roster_t, roster_ct
        for kill in sorted(
            (k for k in (rnd.get("kills") or []) if isinstance(k, dict)),
            key=lambda k: _as_int(k.get("tick")),
        ):
            tick = _as_int(kill.get("tick"), -1)
            if tick <= start_tick:
                continue
            victim_side = _norm_side(kill.get("victim_side"))
            if victim_side == "T":
                t_alive = max(0, t_alive - 1)
            elif victim_side == "CT":
                ct_alive = max(0, ct_alive - 1)
            timeline[tick] = (t_alive, ct_alive)
    return [
        {"tick": tick, "t_alive": values[0], "ct_alive": values[1]}
        for tick, values in sorted(timeline.items())
    ]


def _event_at_tick(
    tick: int,
    kills: Sequence[Dict[str, Any]],
    bomb_events: Sequence[Dict[str, Any]],
    previous_alive: Optional[Tuple[int, int]],
    current_alive: Tuple[int, int],
) -> Tuple[str, str]:
    for kill in kills:
        if _as_int(kill.get("tick"), -1) == tick:
            killer = kill.get("killer_name") or "世界"
            killer_side = _norm_side(kill.get("killer_side"))
            victim = kill.get("victim_name") or "未知"
            victim_side = _norm_side(kill.get("victim_side"))
            weapon = kill.get("weapon") or "未知武器"
            return "kill", "{}（{}）用 {} 击杀 {}（{}）".format(
                killer, killer_side or "?", weapon, victim, victim_side or "?"
            )
    for event in bomb_events:
        if _as_int(event.get("tick"), -1) == tick:
            kind = str(event.get("type") or "bomb")
            player = event.get("player_name") or ""
            site = event.get("site") or ""
            if kind == "planted":
                return "bomb_plant", "{} 在 {} 点下包".format(
                    player or "某人", site or "?"
                )
            if kind == "defused":
                return "bomb_defuse", "{} 拆包成功".format(player or "某人")
            if kind == "exploded":
                return "bomb_explode", "炸弹爆炸"
            return "bomb_{}".format(kind), "炸弹事件 {}".format(kind)
    if previous_alive is not None and previous_alive != current_alive:
        return "alive_change", "存活变化 {}v{} → {}v{}".format(
            previous_alive[0], previous_alive[1], current_alive[0], current_alive[1]
        )
    return "tick", ""


def _analysis_rounds(analysis: Optional[Dict[str, Any]]) -> Dict[int, Dict[str, Any]]:
    if not isinstance(analysis, dict):
        return {}
    rounds = analysis.get("rounds")
    if not isinstance(rounds, list):
        root = analysis.get("root")
        rounds = root.get("rounds") if isinstance(root, dict) else None
    out: Dict[int, Dict[str, Any]] = {}
    for item in rounds or []:
        if isinstance(item, dict):
            number = _as_int(item.get("number"), -1)
            if number >= 0:
                out[number] = item
    return out


def snapshots_from_match(
    match: Dict[str, Any],
    analysis: Optional[Dict[str, Any]] = None,
) -> List[Dict[str, Any]]:
    """把一场 match.json 转成 tick 快照列表。只取截至该 tick 的事实。"""
    if not isinstance(match, dict):
        return []
    meta = match.get("meta") or {}
    source_file = str(meta.get("source_file") or "unknown")
    map_name = str(meta.get("map") or "unknown")
    tick_rate = _as_float(meta.get("tick_rate"), 64.0) or 64.0
    focus_id = str(meta.get("focus_player") or "")
    analysis_by_round = _analysis_rounds(analysis)

    rows: List[Dict[str, Any]] = []
    score_before = {"t": 0, "ct": 0}
    for rnd in match.get("rounds") or []:
        if not isinstance(rnd, dict):
            continue
        round_no = _as_int(rnd.get("number"), 0)
        start_tick = _as_int(rnd.get("start_tick"), 0)
        winner = _norm_side(rnd.get("winner"))
        focus_side = _focus_side_for_round(rnd, focus_id)
        if winner not in ("T", "CT") or focus_side not in ("T", "CT"):
            score_before = rnd.get("score_after") or score_before
            continue
        focus_won = winner == focus_side
        enemy_side = _other_side(focus_side)
        half = str(rnd.get("half") or "")

        economy = rnd.get("economy") or {}
        t_econ = economy.get("t") or {}
        ct_econ = economy.get("ct") or {}
        t_money = _as_int(t_econ.get("money"))
        ct_money = _as_int(ct_econ.get("money"))
        t_equip = _as_int(t_econ.get("equip_value_freeze_end"))
        ct_equip = _as_int(ct_econ.get("equip_value_freeze_end"))

        roster_t, roster_ct = _roster_counts(rnd)
        alive = _alive_timeline(rnd, start_tick, roster_t, roster_ct)
        kills = sorted(
            (k for k in (rnd.get("kills") or []) if isinstance(k, dict)),
            key=lambda k: _as_int(k.get("tick")),
        )
        bomb_events = sorted(
            (b for b in (rnd.get("bomb") or []) if isinstance(b, dict)),
            key=lambda b: _as_int(b.get("tick")),
        )
        utility = sorted(
            (u for u in (rnd.get("utility") or []) if isinstance(u, dict)),
            key=lambda u: _as_int(u.get("tick")),
        )
        analysis_round = analysis_by_round.get(round_no, {})
        duels = sorted(
            (d for d in (analysis_round.get("duels") or []) if isinstance(d, dict)),
            key=lambda d: _as_int(d.get("tick")),
        )
        positioning = analysis_round.get("positioning")
        if not isinstance(positioning, dict):
            positioning = None

        # 采样点 = 回合开始 + 存活变化 + 炸弹事件 + 接敌事件。
        # 事件对齐而不是逐 tick 全量：M2a 只给存活序列，中间状态不猜。
        alive_by_tick = {
            _as_int(item.get("tick"), start_tick): (
                _as_int(item.get("t_alive"), roster_t),
                _as_int(item.get("ct_alive"), roster_ct),
            )
            for item in alive
        }
        timeline = {start_tick} | {tick for tick in alive_by_tick if tick >= start_tick}
        timeline.update(
            _as_int(event.get("tick"), -1)
            for event in bomb_events
            if _as_int(event.get("tick"), -1) >= start_tick
        )
        timeline.update(
            _as_int(duel.get("tick"), -1)
            for duel in duels
            if _as_int(duel.get("tick"), -1) >= start_tick
        )

        previous_alive: Optional[Tuple[int, int]] = None
        current_alive = (roster_t, roster_ct)
        for tick in sorted(timeline):
            if tick in alive_by_tick:
                current_alive = alive_by_tick[tick]
            t_alive, ct_alive = current_alive

            focus_alive = t_alive if focus_side == "T" else ct_alive
            enemy_alive = ct_alive if focus_side == "T" else t_alive
            focus_money = t_money if focus_side == "T" else ct_money
            enemy_money = ct_money if focus_side == "T" else t_money
            focus_equip = t_equip if focus_side == "T" else ct_equip
            enemy_equip = ct_equip if focus_side == "T" else t_equip
            score_focus = _score_value(score_before, focus_side)
            score_enemy = _score_value(score_before, enemy_side)

            kills_upto = [k for k in kills if _as_int(k.get("tick"), -1) <= tick]
            kills_focus = sum(
                1 for k in kills_upto if _norm_side(k.get("killer_side")) == focus_side
            )
            kills_enemy = sum(
                1 for k in kills_upto if _norm_side(k.get("killer_side")) == enemy_side
            )
            first_kill = kills[0] if kills else None
            first_blood_focus = 0
            if first_kill and _as_int(first_kill.get("tick"), -1) <= tick:
                killer_side = _norm_side(first_kill.get("killer_side"))
                victim_side = _norm_side(first_kill.get("victim_side"))
                if killer_side == focus_side:
                    first_blood_focus = 1
                elif victim_side == focus_side:
                    first_blood_focus = -1
            focus_dead = any(
                str(k.get("victim_id") or "") == focus_id
                and _as_int(k.get("tick"), -1) <= tick
                for k in kills
            )

            util_upto = [u for u in utility if _as_int(u.get("tick"), -1) <= tick]
            util_focus = sum(
                1 for u in util_upto if _norm_side(u.get("thrower_side")) == focus_side
            )
            util_enemy = sum(
                1 for u in util_upto if _norm_side(u.get("thrower_side")) == enemy_side
            )

            def util_count(side: str, kinds: Set[str]) -> int:
                return sum(
                    1
                    for u in util_upto
                    if _norm_side(u.get("thrower_side")) == side
                    and str(u.get("type") or "").lower() in kinds
                )

            flash_focus = util_count(focus_side, {"flash", "flashbang"})
            smoke_focus = util_count(focus_side, {"smoke"})
            he_focus = util_count(focus_side, {"he", "hegrenade"})
            molotov_focus = util_count(focus_side, {"molotov", "incendiary"})
            flash_enemy = util_count(enemy_side, {"flash", "flashbang"})
            smoke_enemy = util_count(enemy_side, {"smoke"})
            he_enemy = util_count(enemy_side, {"he", "hegrenade"})
            molotov_enemy = util_count(enemy_side, {"molotov", "incendiary"})

            planted = False
            defused = False
            exploded = False
            bomb_site = ""
            plant_tick = 0
            for event in bomb_events:
                event_tick = _as_int(event.get("tick"), -1)
                if event_tick > tick:
                    break
                kind = str(event.get("type") or "").lower()
                if kind == "planted":
                    planted = True
                    plant_tick = event_tick
                    bomb_site = str(event.get("site") or bomb_site or "")
                elif kind == "defused":
                    defused = True
                elif kind == "exploded":
                    exploded = True
            bomb_active = planted and not defused and not exploded
            bomb_time_left = (
                max(0.0, 40.0 - (tick - plant_tick) / tick_rate) if bomb_active else 0.0
            )

            latest_duel = None
            for duel in duels:
                if _as_int(duel.get("tick"), -1) <= tick:
                    latest_duel = duel
                else:
                    break
            duel_preaim = -1.0
            duel_moving = 0
            duel_outcome = 0
            duel_first_shot_diff = 0.0
            duel_age = -1.0
            if latest_duel:
                duel_preaim = (
                    _as_float(latest_duel.get("pre_aim_delta_deg"))
                    if _as_bool(latest_duel.get("pre_aim_measured"))
                    else -1.0
                )
                duel_moving = int(_as_bool(latest_duel.get("moving_shot")))
                outcome = str(latest_duel.get("outcome") or "")
                duel_outcome = (
                    1
                    if outcome == "focus_kill"
                    else (-1 if outcome == "focus_death" else 0)
                )
                duel_first_shot_diff = (
                    _as_float(latest_duel.get("first_shot_diff_sec"))
                    if _as_bool(latest_duel.get("has_both_shots"))
                    else 0.0
                )
                duel_age = max(
                    0.0, (tick - _as_int(latest_duel.get("tick"))) / tick_rate
                )

            death_exposed = 0
            death_isolated = 0
            death_cover = 0
            death_high_risk = 0
            if (
                positioning
                and focus_dead
                and _as_int(positioning.get("tick"), -1) <= tick
            ):
                death_exposed = _as_int(positioning.get("exposed_lines"))
                death_isolated = int(_as_bool(positioning.get("isolated")))
                death_cover = int(_as_bool(positioning.get("cover_utility")))
                death_high_risk = int(_as_bool(positioning.get("high_risk")))

            event_type, event_desc = _event_at_tick(
                tick, kills, bomb_events, previous_alive, current_alive
            )
            if event_type == "tick":
                duel_here = next(
                    (d for d in duels if _as_int(d.get("tick"), -1) == tick), None
                )
                if duel_here:
                    event_type = "duel"
                    opponent = duel_here.get("opponent_name") or "对手"
                    if _as_bool(duel_here.get("pre_aim_measured")):
                        event_desc = "与 {} 接敌，预瞄差 {:.1f}°".format(
                            opponent, _as_float(duel_here.get("pre_aim_delta_deg"))
                        )
                    else:
                        event_desc = "与 {} 接敌（无预瞄快照）".format(opponent)
            round_time = max(0.0, (tick - start_tick) / tick_rate)
            rows.append(
                {
                    "source_file": source_file,
                    "match_id": source_file,
                    "map": map_name,
                    "half": half,
                    "round_number": round_no,
                    "tick": tick,
                    "tick_rate": tick_rate,
                    "round_time": round_time,
                    "round_progress": min(1.0, round_time / 115.0),
                    "focus_player": focus_id,
                    "focus_side": focus_side,
                    "enemy_side": enemy_side,
                    "focus_won": focus_won,
                    "winner_side": winner,
                    "t_alive": t_alive,
                    "ct_alive": ct_alive,
                    "focus_alive": focus_alive,
                    "enemy_alive": enemy_alive,
                    "alive_diff": focus_alive - enemy_alive,
                    "t_money": t_money,
                    "ct_money": ct_money,
                    "focus_money": focus_money,
                    "enemy_money": enemy_money,
                    "money_diff": focus_money - enemy_money,
                    "t_equip": t_equip,
                    "ct_equip": ct_equip,
                    "focus_equip": focus_equip,
                    "enemy_equip": enemy_equip,
                    "equip_diff": focus_equip - enemy_equip,
                    "score_before": dict(score_before),
                    "score_diff": score_focus - score_enemy,
                    "bomb_planted": int(bomb_active),
                    "bomb_site": bomb_site,
                    "bomb_time_left": bomb_time_left,
                    "kills_focus": kills_focus,
                    "kills_enemy": kills_enemy,
                    "kill_diff": kills_focus - kills_enemy,
                    "first_blood_focus": first_blood_focus,
                    "focus_dead": int(focus_dead),
                    "util_focus": util_focus,
                    "util_enemy": util_enemy,
                    "util_diff": util_focus - util_enemy,
                    "flash_focus": flash_focus,
                    "smoke_focus": smoke_focus,
                    "he_focus": he_focus,
                    "molotov_focus": molotov_focus,
                    "flash_enemy": flash_enemy,
                    "smoke_enemy": smoke_enemy,
                    "he_enemy": he_enemy,
                    "molotov_enemy": molotov_enemy,
                    "duel_preaim_delta_deg": duel_preaim,
                    "duel_moving_shot": duel_moving,
                    "duel_outcome": duel_outcome,
                    "duel_first_shot_diff_sec": duel_first_shot_diff,
                    "duel_age_sec": duel_age,
                    "death_exposed_lines": death_exposed,
                    "death_isolated": death_isolated,
                    "death_cover_utility": death_cover,
                    "death_high_risk": death_high_risk,
                    "event_type": event_type,
                    "event_desc": event_desc,
                }
            )
            previous_alive = current_alive

        score_before = rnd.get("score_after") or score_before
    return rows


# --------------------------------------------------------------------------
# 输入加载
# --------------------------------------------------------------------------


def _load_json_file(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def load_match_inputs(paths: Sequence[str]) -> List[Dict[str, Any]]:
    """加载 match.json（或包含 match.json 的目录），同目录 analysis.json 自动配对。"""
    files: List[Path] = []
    for item in paths:
        path = Path(item).expanduser()
        if path.is_dir():
            files.extend(sorted(path.rglob("match.json")))
        else:
            files.append(path)
    rows: List[Dict[str, Any]] = []
    for path in files:
        if not path.exists():
            raise FileNotFoundError("match.json 不存在：{}".format(path))
        match = _load_json_file(path)
        meta = match.get("meta") if isinstance(match, dict) else None
        if not isinstance(meta, dict) or not meta.get("focus_player"):
            raise ValueError(
                "{}: match.json 缺 meta.focus_player；先用 cs2coach parse/run --player 指定焦点玩家".format(
                    path
                )
            )
        analysis = None
        sibling = path.with_name("analysis.json")
        if sibling.exists():
            try:
                analysis = _load_json_file(sibling)
            except (OSError, ValueError):
                analysis = None
        rows.extend(snapshots_from_match(match, analysis))
    return rows


def load_jsonl_rows(path: str | Path) -> List[Dict[str, Any]]:
    path = Path(path)
    return [row for _, row in read_jsonl(path)]


def detect_mode(rows: Sequence[Dict[str, Any]]) -> str:
    if not rows:
        return "round"
    if all("tick" in row for row in rows):
        return "tick"
    tick_like = sum(
        1
        for row in rows
        if "tick" in row
        and (
            "t_alive" in row
            or "focus_alive" in row
            or "round_time" in row
            or "event_type" in row
        )
    )
    return "tick" if tick_like >= max(1, len(rows) // 2) else "round"


def load_rows(
    data: Sequence[str] = (),
    snapshots: Sequence[str] = (),
    match_json: Sequence[str] = (),
    mode: str = "auto",
) -> Tuple[List[Dict[str, Any]], str]:
    """统一入口：返回 (rows, mode)。mode = round / tick。"""
    rows: List[Dict[str, Any]] = []
    for item in data:
        rows.extend(load_jsonl_rows(item))
    for item in snapshots:
        rows.extend(load_jsonl_rows(item))
    for item in match_json:
        rows.extend(load_match_inputs([item]))
    if not rows:
        raise ValueError(
            "没有数据：给 --data samples.jsonl 或 --match-json <match.json|目录>"
        )
    has_tick = any("tick" in row for row in rows)
    has_round = any("tick" not in row for row in rows)
    if has_tick and has_round:
        raise ValueError(
            "回合级 samples.jsonl 和 tick 快照不能混在一次训练里；请分开跑，"
            "或只用 --match-json / --snapshots"
        )
    detected = detect_mode(rows)
    if mode == "auto":
        mode = detected
    if mode == "tick" and detected != "tick":
        raise ValueError(
            "指定了 tick 模式，但输入里没有 tick 字段；检查 --match-json 或快照 JSONL"
        )
    if mode == "round" and detected == "tick":
        raise ValueError(
            "指定了 round 模式，但输入是 tick 快照；去掉 --mode 或改用 tick"
        )
    return rows, mode


# --------------------------------------------------------------------------
# 数据集构建 / 编码 / 切分
# --------------------------------------------------------------------------


def feature_row(row: Dict[str, Any], mode: str) -> Dict[str, Any]:
    return _round_feature_row(row) if mode == "round" else dict(row)


def feature_names(mode: str) -> List[str]:
    return list(ROUND_FEATURES if mode == "round" else SNAPSHOT_FEATURES)


def categorical_names(mode: str) -> List[str]:
    return list(ROUND_CATEGORICAL if mode == "round" else SNAPSHOT_CATEGORICAL)


def prepare_rows(
    rows: Sequence[Dict[str, Any]],
    mode: str,
    drop_draws: bool = True,
) -> Dict[str, Any]:
    """过滤无标签行，返回特征字典、标签、分组键、回合键。"""
    feat_rows: List[Dict[str, Any]] = []
    labels: List[int] = []
    groups: List[str] = []
    round_keys: List[Tuple[str, int]] = []
    meta_rows: List[Dict[str, Any]] = []
    skipped = 0
    for row in rows:
        label = label_of(row, drop_draws=drop_draws)
        if label is None:
            skipped += 1
            continue
        feat_rows.append(feature_row(row, mode))
        labels.append(label)
        groups.append(match_id_of(row))
        round_keys.append(round_key_of(row))
        meta_rows.append(row)
    return {
        "feature_rows": feat_rows,
        "labels": labels,
        "groups": groups,
        "round_keys": round_keys,
        "meta_rows": meta_rows,
        "skipped": skipped,
    }


def fit_cat_encoders(
    feature_rows: Sequence[Dict[str, Any]], cat_names: Sequence[str]
) -> Dict[str, Dict[str, int]]:
    encoders: Dict[str, Dict[str, int]] = {}
    for name in cat_names:
        values = sorted({str(row.get(name) or "") for row in feature_rows})
        encoders[name] = {value: index for index, value in enumerate(values)}
    return encoders


def encode_matrix(
    feature_rows: Sequence[Dict[str, Any]],
    names: Sequence[str],
    cat_names: Sequence[str],
    encoders: Dict[str, Dict[str, int]],
) -> List[List[float]]:
    out: List[List[float]] = []
    for row in feature_rows:
        values: List[float] = []
        for name in names:
            raw = row.get(name)
            if name in cat_names:
                values.append(float(encoders.get(name, {}).get(str(raw or ""), -1)))
            else:
                values.append(_as_float(raw))
        out.append(values)
    return out


def encode_single(
    row: Dict[str, Any],
    mode: str,
    meta: Dict[str, Any],
) -> List[float]:
    names = meta["feature_names"]
    cat_names = set(meta["cat_names"])
    encoders = meta["cat_encoders"]
    feat = feature_row(row, mode)
    values: List[float] = []
    for name in names:
        raw = feat.get(name)
        if name in cat_names:
            values.append(float(encoders.get(name, {}).get(str(raw or ""), -1)))
        else:
            values.append(_as_float(raw))
    return values


def split_by_match(
    groups: Sequence[str],
    valid_ratio: float = 0.2,
    seed: int = 42,
) -> Tuple[List[int], List[int]]:
    """按 match 分组切 train/valid；同一场比赛只进一边。"""
    import random

    unique = sorted(set(groups))
    if len(unique) <= 1 or valid_ratio <= 0:
        return list(range(len(groups))), []
    random.Random(seed).shuffle(unique)
    target = max(1, int(round(len(groups) * valid_ratio)))
    valid_groups: Set[str] = set()
    valid_count = 0
    for group in unique:
        if valid_count >= target:
            break
        valid_groups.add(group)
        valid_count += sum(1 for g in groups if g == group)
    train_idx = [i for i, g in enumerate(groups) if g not in valid_groups]
    valid_idx = [i for i, g in enumerate(groups) if g in valid_groups]
    if not train_idx or not valid_idx:
        return list(range(len(groups))), []
    return train_idx, valid_idx


def audit(
    rows: Sequence[Dict[str, Any]], mode: str, prepared: Optional[Dict[str, Any]] = None
) -> Dict[str, Any]:
    prepared = prepared or prepare_rows(rows, mode)
    labels = prepared["labels"]
    round_keys = set(prepared["round_keys"])
    matches = set(prepared["groups"])
    return {
        "mode": mode,
        "rows": len(rows),
        "usable_rows": len(labels),
        "skipped_rows": prepared["skipped"],
        "rounds": len(round_keys),
        "matches": len(matches),
        "label_counts": {
            "focus_won_0": sum(1 for value in labels if value == 0),
            "focus_won_1": sum(1 for value in labels if value == 1),
        },
        "features": len(feature_names(mode)),
    }
