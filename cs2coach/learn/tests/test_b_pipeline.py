"""test_b_pipeline.py — Self-test the B-layer pipeline with synthetic data.

Run:
    python -m pytest learn/tests/test_b_pipeline.py -v
"""

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))


def _make_synthetic_sample(i: int) -> dict:
    np.random.seed(i * 17 + 3)
    map_names = ["inferno", "mirage", "nuke", "ancient", "anubis"]
    half = "H1" if i % 2 == 0 else "H2"
    buy_options = ["eco", "force", "normal", "full", "pistol"]

    t_money = 4000 if i % 3 == 0 else int(np.random.choice([1600, 1900, 2200, 2700, 2900, 3500, 4000, 4750, 5700, 6400]))
    ct_money = int(np.random.choice([4000, 1600, 1900, 2700, 2900, 3500, 4750, 5700, 6400]))
    t_equip = 0 if t_money < 2000 else int(np.random.randint(0, 3000))
    ct_equip = 0 if ct_money < 2000 else int(np.random.randint(0, 3000))

    return {
        "source_file": "synthetic.dem",
        "map": str(map_names[i % len(map_names)]),
        "tick_rate": 64,
        "focus_player": "test_steamid",
        "round_number": int(i + 1),
        "half": str(half),
        "score_before": {"t": int(i % 10), "ct": int((i + 2) % 10)},
        "t_money": int(t_money),
        "ct_money": int(ct_money),
        "t_equip_freeze_end": int(t_equip),
        "ct_equip_freeze_end": int(ct_equip),
        "t_money_spent": int(max(0, int(t_money) - int(np.random.randint(0, 1000)))),
        "ct_money_spent": int(max(0, int(ct_money) - int(np.random.randint(0, 1000)))),
        "focus_buy": str(buy_options[i % len(buy_options)]),
        "enemy_buy": str(buy_options[(i + 1) % len(buy_options)]),
        "has_first_kill": bool(i % 7 != 0),
        "first_killer_side": "CT" if i % 2 == 0 else "T",
        "first_victim_side": "T" if i % 2 == 0 else "CT",
        "first_kill_round_time": round(float(np.random.uniform(5, 80)), 4),
        "traded_within_window": bool(i % 3 != 0),
        "focus_util_before_contact": int(np.random.randint(0, 4)),
        "enemy_util_before_contact": int(np.random.randint(0, 4)),
        "flash_count": int(np.random.randint(0, 3)),
        "smoke_count": int(np.random.randint(0, 3)),
        "he_count": int(np.random.randint(0, 2)),
        "molotov_count": int(np.random.randint(0, 2)),
        "he_damage_total": int(np.random.randint(0, 80)),
        "start_t_alive": 5 if i < 15 else int(np.random.choice([3, 4, 5])),
        "start_ct_alive": 5 if i < 15 else int(np.random.choice([3, 4, 5])),
        "clutch_entered": bool(i % 5 == 0),
        "clutch_situation": "2v1" if i % 5 == 0 and i % 2 == 0 else ("3v2" if i % 5 == 0 else ""),
        "man_advantage_spans": [],
        "planted": bool(i % 3 == 1),
        "plant_site": "B" if i % 2 == 0 else "A",
        "defused": bool(i % 4 == 0),
        "exploded": bool(i % 6 == 0),
        "plant_round_time": round(float(np.random.uniform(30, 50)), 4),
        "winner_side": "CT" if i % 2 == 0 else "T",
        "focus_won": int(i % 2),
        "end_reason": "elimination" if i % 3 != 2 else "bomb_exploded",
        "win_cost": 0 if i % 2 == 0 else int(np.random.randint(0, 2000)),
        "cause_tags": [] if i % 3 != 0 else ["defuse", "retake"],
    }


def _write_synthetic_jsonl(path: str, n: int = 60) -> str:
    with open(path, "w") as f:
        for i in range(n):
            f.write(json.dumps(_make_synthetic_sample(i)) + "\n")
    return path


def test_load_features():
    from features import load_features_for_baseline
    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=20)
    X, y, feature_names, cat_enc, cat_idx = load_features_for_baseline(f.name)
    assert X.shape[0] == 20, f"Expected 20 rows, got {X.shape[0]}"
    assert X.shape[1] > 10, f"Expected >10 features, got {X.shape[1]}"
    assert len(y) == 20
    assert set(np.unique(y)).issubset({0, 1}), "Labels should be 0/1"
    assert len(feature_names) > 5
    os.unlink(f.name)


def test_train_baseline():
    from train_baseline import train
    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=40)
    out_dir = tempfile.mkdtemp()
    meta = train(f.name, out_dir, cv_folds=3)

    assert meta["n_samples"] == 40
    assert meta["n_features"] > 5
    assert meta["cv_auc_mean"] > 0.4, f"AUC should be > 0.4, got {meta['cv_auc_mean']}"
    assert "feature_names" in meta
    assert len(meta["top_features"]) == 10
    assert meta["cv_folds"] == 3

    assert Path(out_dir, "baseline.txt").exists(), "Model file not saved"
    assert Path(out_dir, "baseline_meta.json").exists(), "Metadata not saved"
    os.unlink(f.name)


def test_baseline_query():
    from train_baseline import train
    from baseline import ProBaseline, DeviationResult

    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=30)
    out_dir = tempfile.mkdtemp()
    train(f.name, out_dir, cv_folds=3)

    bl = ProBaseline(out_dir)

    context = _make_synthetic_sample(0)
    prob = bl.predict_win_prob(context)
    assert 0.0 <= prob <= 1.0, f"Probability out of range: {prob}"

    result = bl.compare(context, actual_won=True)
    assert isinstance(result, DeviationResult)
    assert result.pro_win_prob >= 0
    assert result.gap == 1.0 - result.pro_win_prob
    assert result.verdict in ("超出预期", "符合预期", "低于预期", "严重失误")

    result_loss = bl.compare(context, actual_won=False)
    assert result_loss.gap == 0.0 - result_loss.pro_win_prob

    os.unlink(f.name)


def test_context_stats():
    from train_baseline import train
    from baseline import ProBaseline

    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=50)
    out_dir = tempfile.mkdtemp()
    train(f.name, out_dir, cv_folds=3)

    bl = ProBaseline(out_dir)
    stats = bl.get_context_stats(f.name)

    assert "buy_type" in stats, "Missing buy_type stats"
    assert "economy" in stats, "Missing economy stats"
    assert len(stats["buy_type"]) > 0, "No buy-type buckets found"

    for btype, st in stats["buy_type"].items():
        if "n" not in st:
            continue  # clutch or other nested keys
        assert st["n"] > 0, f"Empty bucket for {btype}"
        assert 0 <= st["win_rate"] <= 1, f"Invalid win rate for {btype}"

    os.unlink(f.name)


def test_cli_train():
    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=30)
    out_dir = tempfile.mkdtemp()

    result = subprocess.run(
        [sys.executable, "train_baseline.py", "--data", f.name, "--out", out_dir, "--cv", "3"],
        capture_output=True, text=True, cwd=str(Path(__file__).resolve().parent.parent),
    )
    assert result.returncode == 0, f"CLI failed: {result.stderr}"
    assert "AUC" in result.stdout, "No AUC in output"

    os.unlink(f.name)


def test_cli_compare():
    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=30)
    out_dir = tempfile.mkdtemp()
    from train_baseline import train
    train(f.name, out_dir, cv_folds=3)

    sample = _make_synthetic_sample(0)
    context_json = json.dumps({
        "map": sample["map"],
        "half": sample["half"],
        "score_before": sample["score_before"],
        "t_money": sample["t_money"],
        "ct_money": sample["ct_money"],
        "focus_buy": sample["focus_buy"],
        "enemy_buy": sample["enemy_buy"],
    })

    result = subprocess.run(
        [sys.executable, "baseline.py", "--model", out_dir, "--context", context_json, "--won", "true"],
        capture_output=True, text=True, cwd=str(Path(__file__).resolve().parent.parent),
    )
    assert result.returncode == 0, f"CLI compare failed: {result.stderr}"
    # stdout may have "ProBaseline loaded..." line before JSON
    text = result.stdout.strip()
    json_start = text.find("{")
    assert json_start >= 0, f"No JSON in stdout: {text}"
    output = json.loads(text[json_start:])
    assert "pro_win_prob" in output
    assert "gap" in output
    assert "verdict" in output

    os.unlink(f.name)


def test_no_leakage():
    from features import load_features_for_baseline
    with tempfile.NamedTemporaryFile(suffix=".jsonl", mode="w", delete=False) as f:
        _write_synthetic_jsonl(f.name, n=20)
    X, y, feature_names, cat_enc, cat_idx = load_features_for_baseline(f.name)

    leakage_fields = {
        "winner_side", "focus_won", "end_reason", "win_cost", "cause_tags",
        "planted", "defused", "exploded",
    }
    for field in leakage_fields:
        assert field not in feature_names, f"LEAKAGE: '{field}' found in features but should be excluded"

    os.unlink(f.name)


if __name__ == "__main__":
    import unittest
    suite = unittest.TestLoader().loadTestsFromTestCase(TestBaselinePipeline) if False else None
    runner = unittest.TextTestRunner(verbosity=2).run(suite) if suite else None
    if runner is None:
        funcs = [v for k, v in sorted(globals().items()) if k.startswith("test_") and callable(v)]
        for fn in funcs:
            try:
                fn()
                print(f"  ✓ {fn.__name__}")
            except Exception as e:
                print(f"  ✗ {fn.__name__}: {e}")
        print(f"\n{sum(1 for fn in funcs)}/{len(funcs)} tests passed")
