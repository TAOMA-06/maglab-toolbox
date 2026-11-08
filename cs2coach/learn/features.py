"""features.py — Read JSONL export → feature matrix for LightGBM baseline.

Key design: categoricals use LabelEncoder (int mapping), not one-hot.
This avoids the "single sample has different columns" problem.
LightGBM natively supports categorical features via categorical_feature param.

Returns: (X, y, categorical_col_indices, feature_names)
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Dict, List, Tuple

import numpy as np
import pandas as pd


def _extract_baseline_features(row: dict) -> dict:
    """Extract round-start + opening features from one round dict.

    Excludes outcome fields: winner_side, focus_won, end_reason, win_cost,
    cause_tags, planted, defused, exploded.
    """
    feat = {}
    # Round-start
    feat["map"] = str(row["map"])
    feat["half"] = row.get("half", "H1")
    feat["round_number"] = int(row.get("round_number", 0))
    sb = row.get("score_before", {})
    feat["score_diff"] = int(sb.get("t", 0)) - int(sb.get("ct", 0))
    feat["money_diff"] = int(row.get("t_money", 0)) - int(row.get("ct_money", 0))
    feat["equip_diff"] = int(row.get("t_equip_freeze_end", 0)) - int(row.get("ct_equip_freeze_end", 0))
    feat["t_equip_freeze_end"] = int(row.get("t_equip_freeze_end", 0))
    feat["ct_equip_freeze_end"] = int(row.get("ct_equip_freeze_end", 0))
    feat["t_money"] = int(row.get("t_money", 0))
    feat["ct_money"] = int(row.get("ct_money", 0))
    feat["t_money_spent"] = int(row.get("t_money_spent", 0))
    feat["ct_money_spent"] = int(row.get("ct_money_spent", 0))
    feat["focus_buy"] = row.get("focus_buy", "eco")
    feat["enemy_buy"] = row.get("enemy_buy", "eco")

    # Opening
    feat["has_first_kill"] = int(row.get("has_first_kill", False))
    feat["first_killer_side"] = row.get("first_killer_side", "")
    feat["first_victim_side"] = row.get("first_victim_side", "")
    feat["first_kill_round_time"] = float(row.get("first_kill_round_time", 0.0))
    feat["traded_within_window"] = int(row.get("traded_within_window", False))
    feat["focus_util_before_contact"] = int(row.get("focus_util_before_contact", 0))
    feat["enemy_util_before_contact"] = int(row.get("enemy_util_before_contact", 0))
    feat["flash_count"] = int(row.get("flash_count", 0))
    feat["smoke_count"] = int(row.get("smoke_count", 0))
    feat["he_count"] = int(row.get("he_count", 0))
    feat["molotov_count"] = int(row.get("molotov_count", 0))
    feat["he_damage_total"] = int(row.get("he_damage_total", 0))
    feat["start_t_alive"] = int(row.get("start_t_alive", 0))
    feat["start_ct_alive"] = int(row.get("start_ct_alive", 0))

    # Derived
    feat["utility_diff"] = feat["focus_util_before_contact"] - feat["enemy_util_before_contact"]
    feat["total_util"] = feat["focus_util_before_contact"] + feat["enemy_util_before_contact"]
    feat["eco_round"] = int(feat["focus_buy"] == "eco")
    feat["force_round"] = int(feat["focus_buy"] == "force")
    feat["enemy_eco"] = int(feat["enemy_buy"] == "eco")
    feat["enemy_force"] = int(feat["enemy_buy"] == "force")

    # Man disadvantage (start alive + clutch)
    feat["man_disadvantage"] = int(0)
    feat["is_clutch"] = int(0)
    feat["clutch_disadvantage_2vX"] = int(0)
    if feat["start_t_alive"] > 0 and feat["start_ct_alive"] > 0:
        if feat["start_t_alive"] < 2 or feat["start_ct_alive"] < 2:
            feat["man_disadvantage"] = 1
    clutch_entered = bool(row.get("clutch_entered", False))
    clutch_sit = row.get("clutch_situation", "")
    if clutch_entered and clutch_sit:
        feat["is_clutch"] = 1
        if "2v" in clutch_sit and "v1" in clutch_sit:
            feat["clutch_disadvantage_2vX"] = 1

    return feat


def _load_rows(jsonl_path: str | Path) -> List[dict]:
    rows = []
    with open(jsonl_path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    if not rows:
        raise ValueError(f"No valid samples in {jsonl_path}")
    return rows


# ── Categorical columns (LightGBM handles via categorical_feature) ──────────
CATEGORICAL_NAMES = [
    "map", "half", "focus_buy", "enemy_buy",
    "first_killer_side", "first_victim_side",
]

# All column names in order (CATEGORICAL_NAMES first, then numeric)
ALL_FEATURE_NAMES = CATEGORICAL_NAMES + [
    "round_number", "score_diff", "money_diff", "equip_diff",
    "t_equip_freeze_end", "ct_equip_freeze_end",
    "t_money", "ct_money", "t_money_spent", "ct_money_spent",
    "first_kill_round_time", "has_first_kill", "traded_within_window",
    "focus_util_before_contact", "enemy_util_before_contact",
    "flash_count", "smoke_count", "he_count", "molotov_count", "he_damage_total",
    "start_t_alive", "start_ct_alive",
    "utility_diff", "total_util",
    "eco_round", "force_round", "enemy_eco", "enemy_force",
    "man_disadvantage", "is_clutch", "clutch_disadvantage_2vX",
]


def load_features_for_baseline(jsonl_path: str | Path) -> Tuple[np.ndarray, np.ndarray, List[str]]:
    """Load and encode features for baseline training.

    Returns (X_numpy, y_focus, feature_names).
    Categorical columns are label-encoded (int mapping).
    """
    rows = _load_rows(jsonl_path)
    feat_rows = [_extract_baseline_features(r) for r in rows]
    df = pd.DataFrame(feat_rows)

    # Label-encode categoricals
    cat_encoders: Dict[str, Dict[str, int]] = {}
    for col in CATEGORICAL_NAMES:
        if col not in df.columns:
            continue
        uniques = sorted(df[col].dropna().unique())
        encoder = {v: i for i, v in enumerate(uniques)}
        cat_encoders[col] = encoder
        df[col] = df[col].map(encoder).fillna(-1).astype(int)

    # Build X in ALL_FEATURE_NAMES order
    X = np.column_stack([df[name].values.astype(float) for name in ALL_FEATURE_NAMES])
    y = np.array([int(r.get("focus_won", 0)) for r in rows], dtype=int)

    # Encode cat indices
    cat_indices = [ALL_FEATURE_NAMES.index(c) for c in CATEGORICAL_NAMES if c in df.columns]

    return X, y, ALL_FEATURE_NAMES, cat_encoders, cat_indices


def encode_single(context: dict, cat_encoders: Dict[str, Dict[str, int]]) -> np.ndarray:
    """Encode a single context dict using the same label encoders.

    Handles unknown categories gracefully (maps to -1).
    """
    feat = {}
    feat["map"] = str(context.get("map", "unknown"))
    feat["half"] = context.get("half", "H1")
    feat["round_number"] = int(context.get("round_number", 0))
    sb = context.get("score_before", {})
    feat["score_diff"] = int(sb.get("t", 0)) - int(sb.get("ct", 0))
    feat["money_diff"] = int(context.get("t_money", 0)) - int(context.get("ct_money", 0))
    feat["equip_diff"] = 0
    feat["t_equip_freeze_end"] = int(context.get("t_equip_freeze_end", 0))
    feat["ct_equip_freeze_end"] = int(context.get("ct_equip_freeze_end", 0))
    feat["t_money"] = int(context.get("t_money", 0))
    feat["ct_money"] = int(context.get("ct_money", 0))
    feat["t_money_spent"] = int(context.get("t_money_spent", 0))
    feat["ct_money_spent"] = int(context.get("ct_money_spent", 0))
    feat["focus_buy"] = context.get("focus_buy", "eco")
    feat["enemy_buy"] = context.get("enemy_buy", "eco")
    feat["has_first_kill"] = int(context.get("has_first_kill", False))
    feat["first_killer_side"] = context.get("first_killer_side", "")
    feat["first_victim_side"] = context.get("first_victim_side", "")
    feat["first_kill_round_time"] = float(context.get("first_kill_round_time", 0.0))
    feat["traded_within_window"] = int(context.get("traded_within_window", False))
    feat["focus_util_before_contact"] = int(context.get("focus_util_before_contact", 0))
    feat["enemy_util_before_contact"] = int(context.get("enemy_util_before_contact", 0))
    feat["flash_count"] = int(context.get("flash_count", 0))
    feat["smoke_count"] = int(context.get("smoke_count", 0))
    feat["he_count"] = int(context.get("he_count", 0))
    feat["molotov_count"] = int(context.get("molotov_count", 0))
    feat["he_damage_total"] = int(context.get("he_damage_total", 0))
    feat["start_t_alive"] = int(context.get("start_t_alive", 0))
    feat["start_ct_alive"] = int(context.get("start_ct_alive", 0))
    feat["utility_diff"] = feat["focus_util_before_contact"] - feat["enemy_util_before_contact"]
    feat["total_util"] = feat["focus_util_before_contact"] + feat["enemy_util_before_contact"]
    feat["eco_round"] = int(feat["focus_buy"] == "eco")
    feat["force_round"] = int(feat["focus_buy"] == "force")
    feat["enemy_eco"] = int(feat["enemy_buy"] == "eco")
    feat["enemy_force"] = int(feat["enemy_buy"] == "force")
    feat["man_disadvantage"] = int(0)
    feat["is_clutch"] = int(0)
    feat["clutch_disadvantage_2vX"] = int(0)

    # Label-encode categoricals using saved encoders
    for col in CATEGORICAL_NAMES:
        if col in feat and col in cat_encoders:
            raw_val = str(feat[col])
            feat[col] = cat_encoders[col].get(raw_val, -1)
        else:
            feat[col] = 0

    X = np.array([feat[name] for name in ALL_FEATURE_NAMES], dtype=float).reshape(1, -1)
    return X
