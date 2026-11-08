"""baseline.py — Load trained model and compute deviation from pro-player baseline.

Usage as library:
    from baseline import ProBaseline
    bl = ProBaseline("learn/models")
    prob = bl.predict_win_prob(context_dict)
    result = bl.compare(context_dict, actual_won=True)

Usage as CLI:
    python baseline.py --context '{"map":"inferno","t_money":2900,...}'
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from pathlib import Path

import lightgbm as lgb
import numpy as np
from sklearn.preprocessing import StandardScaler

from features import encode_single


@dataclass
class DeviationResult:
    context_hash: str
    pro_win_prob: float
    actual_won: bool
    gap: float
    verdict: str

    def to_dict(self) -> dict:
        return {
            "context_hash": self.context_hash,
            "pro_win_prob": round(self.pro_win_prob, 4),
            "actual_won": self.actual_won,
            "gap": round(self.gap, 4),
            "verdict": self.verdict,
        }


class ProBaseline:
    def __init__(self, model_dir: str | Path):
        self.model_dir = Path(model_dir)
        self.model = lgb.Booster(model_file=str(self.model_dir / "baseline.txt"))

        with open(self.model_dir / "baseline_meta.json") as f:
            self.meta = json.load(f)

        self.feature_names = self.meta["feature_names"]
        self.cat_encoders = self.meta["cat_encoders"]
        self.cat_indices = self.meta["cat_indices"]
        print(f"ProBaseline loaded: {len(self.feature_names)} features, "
              f"CV AUC={self.meta['cv_auc_mean']}")

    def predict_win_prob(self, context: dict) -> float:
        X = encode_single(context, self.cat_encoders)
        prob = float(self.model.predict(X)[0])
        return max(0.0, min(1.0, prob))

    def compare(self, context: dict, actual_won: bool, threshold: float = 0.7) -> DeviationResult:
        pro_prob = self.predict_win_prob(context)
        gap = float(actual_won) - pro_prob

        if gap > threshold:
            verdict = "超出预期" if actual_won else "严重失误"
        elif gap > 0.1:
            verdict = "符合预期" if actual_won else "低于预期"
        elif gap > -0.1:
            verdict = "符合预期"
        elif gap > -threshold:
            verdict = "低于预期" if actual_won else "符合预期"
        else:
            verdict = "严重失误" if actual_won else "超出预期"

        context_hash = f"{context.get('map','?')}_{context.get('round_number',0)}_{context.get('half','?')}"
        return DeviationResult(context_hash=context_hash, pro_win_prob=pro_prob,
                               actual_won=actual_won, gap=gap, verdict=verdict)

    def get_context_stats(self, samples_path: str | Path) -> dict:
        rows = _load_rows(samples_path)
        if not rows:
            return {}

        stats = {}
        for btype in ["eco", "force", "normal", "full", "pistol"]:
            subset = [r for r in rows if r.get("focus_buy") == btype]
            if not subset:
                continue
            won = sum(1 for r in subset if r.get("focus_won"))
            n = len(subset)
            stats[btype] = {
                "n": n,
                "win_rate": round(won / n, 4),
                "avg_t_money": round(np.mean([r.get("t_money", 0) for r in subset]), 0),
                "avg_ct_money": round(np.mean([r.get("ct_money", 0) for r in subset]), 0),
            }

        eco_stats = {}
        for situation in ["eco_vs_eco", "eco_vs_full", "force_vs_eco", "full_vs_eco", "full_vs_full"]:
            subset = _filter_by_situation(rows, situation)
            if not subset:
                continue
            won = sum(1 for r in subset if r.get("focus_won"))
            eco_stats[situation] = {"n": len(subset), "win_rate": round(won / len(subset), 4)}

        clutch_stats = {}
        for clutch_sit in ["2v1", "3v2", "3v1"]:
            subset = [r for r in rows if r.get("clutch_situation") == clutch_sit]
            if not subset:
                continue
            won = sum(1 for r in subset if r.get("focus_won"))
            clutch_stats[clutch_sit] = {"n": len(subset), "win_rate": round(won / len(subset), 4)}
        if clutch_stats:
            clutch_top = clutch_stats

        return {"buy_type": stats, "economy": eco_stats, "clutch": clutch_top if clutch_stats else {}}


def _load_rows(path: str | Path) -> list:
    rows = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return rows


def _filter_by_situation(rows, situation):
    result = []
    for r in rows:
        fb = r.get("focus_buy", "")
        eb = r.get("enemy_buy", "")
        if situation == "eco_vs_eco" and fb == "eco" and eb == "eco":
            result.append(r)
        elif situation == "eco_vs_full" and fb == "eco" and eb in ("full", "normal"):
            result.append(r)
        elif situation == "force_vs_eco" and fb == "force" and eb == "eco":
            result.append(r)
        elif situation == "full_vs_eco" and fb in ("full", "normal") and eb == "eco":
            result.append(r)
        elif situation == "full_vs_full" and fb in ("full", "normal") and eb in ("full", "normal"):
            result.append(r)
    return result


def main():
    parser = argparse.ArgumentParser(description="Pro baseline query")
    parser.add_argument("--model", default="learn/models")
    parser.add_argument("--context", help="JSON context string")
    parser.add_argument("--won", type=bool, help="Actual outcome (true/false)")
    parser.add_argument("--stats", help="Path to samples.jsonl for context stats")
    args = parser.parse_args()

    bl = ProBaseline(args.model)

    if args.context:
        context = json.loads(args.context)
        won = args.won if args.won is not None else True
        result = bl.compare(context, won)
        print(json.dumps(result.to_dict(), indent=2, ensure_ascii=False))
    elif args.stats:
        stats = bl.get_context_stats(args.stats)
        print(json.dumps(stats, indent=2, ensure_ascii=False))
    else:
        print("Usage: python baseline.py --context '{...}' --won true/false")
        return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
