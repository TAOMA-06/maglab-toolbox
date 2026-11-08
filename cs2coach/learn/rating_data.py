"""头 B：场级行为聚合 → 5E Rating2 回归（反向训练第一步）。

特征：从 match.json 按（场，选手）聚合 kills/deaths/assists/damage/
首杀/首死/存活/多杀/T-CT 分开。标签：ratings5e.jsonl 的 rating2。
验收：Spearman + MAE，打均值基线。用法：
  python learn/rating_data.py --match-json out/ --ratings learn/data/ratings5e.jsonl \\
      --out-dir learn/models/rating
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from pathlib import Path

import lightgbm as lgb
import numpy as np

REPO = Path(__file__).resolve().parent.parent

FEATURES = [
    "rounds", "kills", "deaths", "assists", "damage", "adr",
    "kd", "kast_proxy", "first_kills", "first_deaths",
    "multi2", "multi3p", "survived",
    "kills_t", "kills_ct", "deaths_t", "deaths_ct",
    "pistol_kills",
]


def load_manifest_map():
    m = {}
    for src in ("5e_demos", "hltv_demos", "renown_demos"):
        p = REPO / "learn" / "data" / src / "manifest.jsonl"
        if not p.exists():
            continue
        for line in p.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line:
                continue
            d = json.loads(line)
            base = os.path.splitext(os.path.basename(d.get("path", "")))[0]
            m[base] = d.get("match_id")
    return m


def aggregate(match: dict) -> dict:
    """(steamid64,) -> 聚合特征。"""
    agg: dict = {}
    rounds = match.get("rounds") or []
    if isinstance(rounds, dict):
        rounds = list(rounds.values())
    for rnd in rounds:
        rno = rnd.get("round_number") or rnd.get("number") or 0
        for pl in rnd.get("players") or []:
            sid = str(pl.get("steamid64") or pl.get("steam_id") or pl.get("name"))
            e = agg.setdefault(sid, {
                "name": pl.get("name"), "rounds": 0, "kills": 0, "deaths": 0,
                "assists": 0, "damage": 0.0, "first_kills": 0, "first_deaths": 0,
                "multi2": 0, "multi3p": 0, "survived": 0,
                "kills_t": 0, "kills_ct": 0, "deaths_t": 0, "deaths_ct": 0,
                "pistol_kills": 0, "kast_rounds": set(),
            })
            e["rounds"] += 1
            k, dt, ast = int(pl.get("kills") or 0), int(pl.get("deaths") or 0), int(pl.get("assists") or 0)
            e["kills"] += k
            e["deaths"] += dt
            e["assists"] += ast
            e["damage"] += float(pl.get("damage_dealt") or 0)
            if pl.get("first_kill"):
                e["first_kills"] += 1
            if pl.get("alive_at_end"):
                e["survived"] += 1
            if pl.get("first_death"):
                e["first_deaths"] += 1
            if k >= 2:
                e["multi2"] += 1
            if k >= 3:
                e["multi3p"] += 1
            side = (pl.get("side") or "").upper()
            if side == "T":
                e["kills_t"] += k
                e["deaths_t"] += dt
            elif side == "CT":
                e["kills_ct"] += k
                e["deaths_ct"] += dt
            if rno in (1, 2, 13, 14):
                e["pistol_kills"] += k
            if k > 0 or ast > 0 or dt == 0:
                e["kast_rounds"].add(rno)
    out = {}
    for sid, e in agg.items():
        r = max(e["rounds"], 1)
        out[sid] = {
            "name": e["name"], "rounds": e["rounds"], "kills": e["kills"],
            "deaths": e["deaths"], "assists": e["assists"], "damage": e["damage"],
            "adr": e["damage"] / r,
            "kd": e["kills"] / max(e["deaths"], 1),
            "kast_proxy": len(e["kast_rounds"]) / r,
            "first_kills": e["first_kills"], "first_deaths": e["first_deaths"],
            "multi2": e["multi2"], "multi3p": e["multi3p"],
            "survived": e["survived"],
            "kills_t": e["kills_t"], "kills_ct": e["kills_ct"],
            "deaths_t": e["deaths_t"], "deaths_ct": e["deaths_ct"],
            "pistol_kills": e["pistol_kills"],
        }
    return out


def build_dataset(match_dirs, ratings_path):
    ratings = {}
    for line in open(ratings_path, encoding="utf-8"):
        r = json.loads(line)
        st = r.get("steam")
        sid = (st or {}).get("steamId") if isinstance(st, dict) else st
        try:
            ratings[(r["match_id"], str(sid))] = float(r["rating2"])
        except (TypeError, ValueError):
            continue
    dirmap = load_manifest_map()
    X, y, info = [], [], []
    for mp in sorted(glob.glob(os.path.join(match_dirs, "*", "match.json"))):
        d = os.path.basename(os.path.dirname(mp))
        mid = next((m for b, m in dirmap.items() if d == b or d.endswith(b) or b in d), None)
        if mid is None:
            continue
        match = json.load(open(mp, encoding="utf-8"))
        for sid, f in aggregate(match).items():
            if (mid, sid) not in ratings:
                continue
            X.append([f[c] for c in FEATURES])
            y.append(ratings[(mid, sid)])
            info.append({"match_id": mid, "steam": sid, "name": f["name"]})
    return np.asarray(X, dtype=float), np.asarray(y, dtype=float), info


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--match-json", default="out/")
    ap.add_argument("--ratings", default="learn/data/ratings5e.jsonl")
    ap.add_argument("--out-dir", default="learn/models/rating")
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()

    X, y, info = build_dataset(args.match_json, args.ratings)
    print(f"样本: {len(y)}（场-人对），特征 {len(FEATURES)}")
    if len(y) < 50:
        print("样本太少，拒绝训练", file=sys.stderr)
        return 2
    rng = np.random.default_rng(args.seed)
    mids = sorted({i["match_id"] for i in info})
    rng.shuffle(mids)
    cut = max(1, int(len(mids) * 0.2))
    va = set(mids[:cut])
    tri = [i for i, n in enumerate(info) if n["match_id"] not in va]
    vai = [i for i, n in enumerate(info) if n["match_id"] in va]
    params = {"objective": "regression", "num_leaves": 15, "min_data_in_leaf": 20,
              "feature_fraction": 0.9, "bagging_fraction": 0.9, "bagging_freq": 1,
              "learning_rate": 0.05, "verbose": -1, "seed": args.seed}
    ds_tr = lgb.Dataset(X[tri], label=y[tri])
    ds_va = lgb.Dataset(X[vai], label=y[vai], reference=ds_tr)
    model = lgb.train(params, ds_tr, num_boost_round=500,
                      valid_sets=[ds_va])
    pv = model.predict(X[vai])
    yv = y[vai]
    mae = float(np.mean(np.abs(pv - yv)))
    base = float(np.mean(np.abs(yv - y[tri].mean())))
    import pandas as pd
    sp = float(pd.Series(pv).corr(pd.Series(yv), method="spearman"))
    print(f"valid n={len(yv)} MAE={mae:.4f}（均值基线 {base:.4f}） Spearman={sp:.4f}")
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    model.save_model(str(out / "rating.txt"))
    fi = model.feature_importance(importance_type="gain")
    tot = float(sum(fi)) or 1.0
    (out / "rating_meta.json").write_text(json.dumps({
        "features": FEATURES,
        "importance_gain_pct": {n: round(100 * float(v) / tot, 2) for n, v in zip(FEATURES, fi)},
        "metrics": {"mae": mae, "baseline_mae": base, "spearman": sp, "n_valid": len(yv)},
        "seed": args.seed,
    }, ensure_ascii=False, indent=2), encoding="utf-8")
    verdict = "通过" if (mae < base and sp > 0.5) else "未通过"
    print(f"头 B 验收: {verdict}（MAE 优于均值基线 且 Spearman>0.5）")
    return 0 if verdict == "通过" else 1


if __name__ == "__main__":
    raise SystemExit(main())
