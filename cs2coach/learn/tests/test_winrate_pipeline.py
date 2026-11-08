#!/usr/bin/env python3
"""TRAIN-PLAN v2 主线自测：胜率数据层 + LightGBM 训练 + T2 掉点。

约束：
- 纯数据/切分/CLI 测试不依赖 lightgbm；训练测试在缺依赖时 skip；
- 全部产物落临时目录；
- 断言「按 match 切分不泄漏」「outcome 字段不进特征」「<1000 回合标记降级」。

跑法：
  learn/.venv/bin/python -m unittest discover -s learn/tests -v
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Optional
from unittest import mock

HERE = Path(__file__).resolve().parent
LEARN = HERE.parent
REPO = LEARN.parent
sys.path.insert(0, str(LEARN))

import train_winrate  # noqa: E402
import winrate_data  # noqa: E402

try:
    import lightgbm  # noqa: F401
    import sklearn  # noqa: F401
    import numpy  # noqa: F401

    HAS_ML = True
except ImportError:  # pragma: no cover
    HAS_ML = False


def _round_row(
    match: str,
    rnd: int,
    *,
    won: bool = True,
    map_name: str = "mirage",
    money_up: Optional[bool] = None,
    equip_up: Optional[bool] = None,
) -> dict:
    # 让钱/装备只编码 XOR 的两个分量，不直接编码 won；否则基线和模型都会满分，
    # 测不出「模型必须比线性基线高 +0.10」这条门槛。
    money_up = won if money_up is None else money_up
    equip_up = won if equip_up is None else equip_up
    return {
        "source_file": match,
        "map": map_name,
        "tick_rate": 64,
        "focus_player": "111",
        "round_number": rnd,
        "half": "H1" if rnd <= 10 else "H2",
        "score_before": {"t": rnd // 2, "ct": (rnd + 1) // 2},
        "t_money": 4500 if money_up else 1500,
        "ct_money": 3000 if money_up else 4000,
        "t_equip_freeze_end": 22000 if equip_up else 5000,
        "ct_equip_freeze_end": 16000 if equip_up else 18000,
        "t_money_spent": 4000,
        "ct_money_spent": 3000,
        "focus_buy": "full" if money_up else "eco",
        "enemy_buy": "force" if equip_up else "full",
        "has_first_kill": money_up,
        "first_killer_side": "T" if money_up else "CT",
        "first_victim_side": "CT" if money_up else "T",
        "first_kill_round_time": 20.0,
        "traded_within_window": equip_up,
        "focus_util_before_contact": 3 if money_up else 1,
        "enemy_util_before_contact": 2,
        "flash_count": 2,
        "smoke_count": 2,
        "he_count": 1,
        "molotov_count": 1,
        "he_damage_total": 20,
        "start_t_alive": 5,
        "start_ct_alive": 5,
        "clutch_entered": False,
        "clutch_situation": "",
        "man_advantage_spans": [
            {
                "start_tick": 100,
                "end_tick": 200,
                "adv_side": "T",
                "adv_delta": 2,
                "threw": not money_up,
            }
        ],
        "winner_side": "T" if won else "CT",
        "focus_won": won,
        "end_reason": "elimination",
        "win_cost": 0.0,
        "cause_tags": [],
    }


def _write_round_jsonl(path: Path, n_matches: int = 6, n_rounds: int = 20) -> None:
    rows = []
    for m in range(n_matches):
        for r in range(1, n_rounds + 1):
            # XOR 信号：逻辑回归（线性）学不好，LightGBM 能学；用于验证 AUC 门槛
            money_up = (r + m) % 3 != 0
            equip_up = (r * 2 + m) % 4 != 0
            won = money_up != equip_up
            rows.append(
                _round_row(
                    "demo_%d.dem" % m,
                    r,
                    won=won,
                    map_name=["mirage", "inferno", "nuke"][m % 3],
                    money_up=money_up,
                    equip_up=equip_up,
                )
            )
    path.write_text("\n".join(json.dumps(row) for row in rows) + "\n", encoding="utf-8")


def _fake_match() -> dict:
    return {
        "meta": {
            "source_file": "fake.dem",
            "map": "mirage",
            "tick_rate": 64,
            "focus_player": "111",
        },
        "players": [
            {"steamid64": 111, "name": "F", "side": "T"},
            {"steamid64": 222, "name": "E", "side": "CT"},
        ],
        "rounds": [
            {
                "number": 1,
                "start_tick": 100,
                "end_tick": 1000,
                "winner": "CT",
                "half": "H1",
                "players": [
                    {"steamid64": 111, "name": "F", "side": "T"},
                    {"steamid64": 222, "name": "E", "side": "CT"},
                ],
                "alive": [
                    {"tick": 100, "t_alive": 5, "ct_alive": 5},
                    {"tick": 300, "t_alive": 4, "ct_alive": 5},
                ],
                "kills": [
                    {
                        "tick": 300,
                        "killer_id": "222",
                        "killer_name": "E",
                        "killer_side": "CT",
                        "victim_id": "111",
                        "victim_name": "F",
                        "victim_side": "T",
                        "weapon": "M4A1",
                    }
                ],
                "bomb": [
                    {"tick": 250, "type": "planted", "site": "A", "player_name": "E"}
                ],
                "utility": [
                    {"tick": 200, "type": "smoke", "thrower_side": "T"},
                    {"tick": 210, "type": "flash", "thrower_side": "CT"},
                ],
                "economy": {
                    "t": {"money": 4000, "equip_value_freeze_end": 20000},
                    "ct": {"money": 3500, "equip_value_freeze_end": 18000},
                },
                "score_after": {"t": 0, "ct": 1},
            }
        ],
    }


def _fake_analysis() -> dict:
    return {
        "rounds": [
            {
                "number": 1,
                "duels": [
                    {
                        "tick": 300,
                        "pre_aim_measured": True,
                        "pre_aim_delta_deg": 23.4,
                        "moving_shot": True,
                        "outcome": "focus_death",
                        "has_both_shots": True,
                        "first_shot_diff_sec": 0.08,
                    }
                ],
                "positioning": {
                    "tick": 300,
                    "exposed_lines": 3,
                    "isolated": True,
                    "cover_utility": False,
                    "high_risk": True,
                },
            }
        ]
    }


class WinrateDataTest(unittest.TestCase):
    def test_round_features_exclude_post_hoc_fields(self):
        feat = winrate_data._round_feature_row(_round_row("m.dem", 1, won=True))
        for field in (
            "man_adv_threw",
            "man_adv_max_delta",
            "man_adv_spans",
            "t_money_spent",
            "flash_count",
            "is_clutch",
        ):
            self.assertNotIn(field, feat)
            self.assertIn(field, winrate_data.LEAKAGE_FIELDS)
        for field in winrate_data.LEAKAGE_FIELDS:
            self.assertNotIn(field, winrate_data.ROUND_FEATURES)
            self.assertNotIn(field, winrate_data.SNAPSHOT_FEATURES)

    def test_snapshots_from_match(self):
        rows = winrate_data.snapshots_from_match(_fake_match(), _fake_analysis())
        self.assertEqual(len(rows), 3)
        first, bomb, death = rows
        self.assertEqual(first["focus_side"], "T")
        self.assertEqual(first["focus_alive"], 5)
        self.assertEqual(first["enemy_alive"], 5)
        self.assertEqual(first["focus_won"], False)
        self.assertEqual(bomb["tick"], 250)
        self.assertEqual(bomb["bomb_planted"], 1)
        self.assertEqual(bomb["event_type"], "bomb_plant")
        self.assertGreater(bomb["bomb_time_left"], 0)
        self.assertEqual(death["focus_alive"], 4)
        self.assertEqual(death["focus_dead"], 1)
        self.assertEqual(death["event_type"], "kill")
        self.assertIn("击杀", death["event_desc"])
        self.assertEqual(death["bomb_planted"], 1)
        self.assertEqual(death["duel_preaim_delta_deg"], 23.4)
        self.assertEqual(death["duel_outcome"], -1)
        self.assertEqual(death["death_exposed_lines"], 3)
        self.assertEqual(death["death_high_risk"], 1)

    def test_load_rows_detects_tick(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "match.json").write_text(
                json.dumps(_fake_match()), encoding="utf-8"
            )
            (root / "analysis.json").write_text(
                json.dumps(_fake_analysis()), encoding="utf-8"
            )
            rows, mode = winrate_data.load_rows(match_json=[str(root)])
            self.assertEqual(mode, "tick")
            self.assertEqual(len(rows), 3)

    def test_mixed_round_and_tick_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            round_path = root / "samples.jsonl"
            round_path.write_text(
                json.dumps(_round_row("m.dem", 1)) + "\n", encoding="utf-8"
            )
            tick_path = root / "snapshots.jsonl"
            tick_path.write_text(
                json.dumps({"tick": 100, "round_number": 1, "focus_won": True}) + "\n",
                encoding="utf-8",
            )
            with self.assertRaises(ValueError):
                winrate_data.load_rows(
                    data=[str(round_path)], snapshots=[str(tick_path)]
                )

    def test_split_by_match_no_leakage(self):
        groups = ["a"] * 10 + ["b"] * 10 + ["c"] * 10 + ["d"] * 10
        train, valid = winrate_data.split_by_match(groups, valid_ratio=0.5, seed=0)
        self.assertTrue(train and valid)
        train_groups = {groups[i] for i in train}
        valid_groups = {groups[i] for i in valid}
        self.assertFalse(train_groups & valid_groups)

    def test_prepare_rows_drops_draws_and_unknown_labels(self):
        rows = [
            _round_row("m.dem", 1, won=True),
            {
                **_round_row("m.dem", 2, won=False),
                "winner_side": "draw",
                "focus_won": None,
            },
            {**_round_row("m.dem", 3, won=True), "focus_won": None, "winner_side": ""},
        ]
        prepared = winrate_data.prepare_rows(rows, "round", drop_draws=True)
        self.assertEqual(len(prepared["labels"]), 1)
        self.assertEqual(prepared["skipped"], 2)

    def test_unknown_category_encodes_to_minus_one(self):
        train = [
            winrate_data._round_feature_row(_round_row("m.dem", 1, map_name="mirage"))
        ]
        enc = winrate_data.fit_cat_encoders(train, winrate_data.ROUND_CATEGORICAL)
        X = winrate_data.encode_matrix(
            [
                winrate_data._round_feature_row(
                    _round_row("m.dem", 2, map_name="new_map")
                )
            ],
            winrate_data.ROUND_FEATURES,
            winrate_data.ROUND_CATEGORICAL,
            enc,
        )
        self.assertEqual(X[0][winrate_data.ROUND_FEATURES.index("map")], -1.0)


class WinrateTrainTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="cs2-winrate-test-"))

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _settings(self, data: Path, **kwargs):
        base = dict(
            data=[str(data)],
            out_dir=self.tmp / "model",
            min_rounds=50,
            min_auc_delta=0.05,
            valid_ratio=0.34,
            cv_folds=3,
            seed=0,
        )
        base.update(kwargs)
        return train_winrate.Settings(**base)

    @unittest.skipUnless(HAS_ML, "lightgbm/sklearn/numpy 未安装")
    def test_train_saves_artifacts_and_acceptance(self):
        data = self.tmp / "samples.jsonl"
        _write_round_jsonl(data, n_matches=8, n_rounds=20)
        settings = self._settings(data)
        self.assertEqual(train_winrate.train(settings), 0)
        self.assertTrue((self.tmp / "model" / "winrate.txt").exists())
        meta = json.loads(
            (self.tmp / "model" / "winrate_meta.json").read_text(encoding="utf-8")
        )
        self.assertEqual(meta["mode"], "round")
        self.assertTrue(meta["data_gate"]["pass"])
        self.assertTrue(meta["acceptance"]["valid_pass"])
        self.assertGreater(meta["acceptance"]["auc_delta"], 0.0)
        # round v0 只是兼容路径，不能冒充 v2 的 T1
        self.assertFalse(meta["acceptance"]["mode_pass"])
        self.assertFalse(meta["acceptance"]["t1_pass"])
        self.assertTrue((self.tmp / "model" / "report.md").exists())

    @unittest.skipUnless(HAS_ML, "lightgbm/sklearn/numpy 未安装")
    def test_single_match_cannot_pass_t1(self):
        data = self.tmp / "samples.jsonl"
        _write_round_jsonl(data, n_matches=1, n_rounds=20)
        settings = self._settings(
            data, min_rounds=10, min_auc_delta=0.0, valid_ratio=0.2
        )
        self.assertEqual(train_winrate.train(settings), 0)
        meta = json.loads(
            (self.tmp / "model" / "winrate_meta.json").read_text(encoding="utf-8")
        )
        self.assertFalse(meta["acceptance"]["valid_pass"])
        self.assertFalse(meta["acceptance"]["t1_pass"])

    @unittest.skipUnless(HAS_ML, "lightgbm/sklearn/numpy 未安装")
    def test_strict_fails_when_t1_not_pass(self):
        data = self.tmp / "samples.jsonl"
        _write_round_jsonl(data, n_matches=1, n_rounds=20)
        settings = self._settings(
            data, min_rounds=10, min_auc_delta=0.0, valid_ratio=0.2
        )
        self.assertEqual(train_winrate.train(settings, strict=True), 2)

    @unittest.skipUnless(HAS_ML, "lightgbm/sklearn/numpy 未安装")
    def test_oof_mask_excludes_single_class_folds(self):
        import numpy as np

        X = np.random.RandomState(0).rand(60, 4)
        y = np.array([1] * 20 + [0, 1] * 20)
        groups = ["A"] * 20 + ["B"] * 20 + ["C"] * 20
        oof, folds, mask = train_winrate._oof_predictions(
            X, y, groups, 3, train_winrate.lightgbm_params(train_winrate.Settings()), []
        )
        self.assertEqual(len(folds), 2)
        self.assertLess(int(mask.sum()), len(y))
        self.assertGreater(int((oof[mask] != 0).sum()), 0)

    def test_cli_check_and_dry_run_without_ml(self):
        data = self.tmp / "samples.jsonl"
        _write_round_jsonl(data, n_matches=2, n_rounds=10)
        check = subprocess.run(
            [
                sys.executable,
                str(LEARN / "train_winrate.py"),
                "--data",
                str(data),
                "--check",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(check.returncode, 0, msg=check.stderr)
        self.assertIn("数据模式", check.stdout)
        dry = subprocess.run(
            [
                sys.executable,
                str(LEARN / "train_winrate.py"),
                "--data",
                str(data),
                "--dry-run",
            ],
            capture_output=True,
            text=True,
        )
        self.assertEqual(dry.returncode, 0, msg=dry.stderr)
        self.assertIn("计划切分", dry.stdout)
        self.assertIn("dry-run", dry.stdout)

    def test_drop_points_requires_tick_mode(self):
        data = self.tmp / "samples.jsonl"
        _write_round_jsonl(data, n_matches=2, n_rounds=10)
        settings = train_winrate.Settings(data=[str(data)], out_dir=self.tmp / "model")
        self.assertEqual(train_winrate.run_drop_points(settings, top=5), 2)

    def test_score_drops_agreement(self):
        drops = [
            {"match_id": "m.dem", "round": 1, "tick": 100 + i, "drop": 0.1}
            for i in range(20)
        ]
        judgments = [{"index": i + 1, "agree": i < 12} for i in range(20)]
        result = train_winrate.score_drops(drops, judgments)
        self.assertEqual(result["agree"], 12)
        self.assertEqual(result["disagree"], 8)
        self.assertEqual(result["agreement_rate"], 0.6)

        drops_path = self.tmp / "drops.jsonl"
        judgments_path = self.tmp / "judgments.jsonl"
        drops_path.write_text(
            "\n".join(json.dumps(row) for row in drops) + "\n", encoding="utf-8"
        )
        judgments_path.write_text(
            "\n".join(json.dumps(row) for row in judgments) + "\n", encoding="utf-8"
        )
        report = self.tmp / "drops_report.md"
        self.assertEqual(
            train_winrate.run_score_drops(
                str(drops_path),
                str(judgments_path),
                min_agreement=0.60,
                min_samples=20,
                report_path=str(report),
            ),
            0,
        )
        self.assertTrue(report.exists())
        self.assertIn("认同率", report.read_text(encoding="utf-8"))

        fail_judgments = [{"index": i + 1, "agree": i < 11} for i in range(20)]
        judgments_path.write_text(
            "\n".join(json.dumps(row) for row in fail_judgments) + "\n",
            encoding="utf-8",
        )
        self.assertEqual(
            train_winrate.run_score_drops(
                str(drops_path), str(judgments_path), min_agreement=0.60, min_samples=20
            ),
            2,
        )

    def test_drop_points_groups_and_sorts(self):
        snapshots = self.tmp / "snapshots.jsonl"
        rows = []
        for round_no, probs in ((1, [0.8, 0.5, 0.2]), (2, [0.4, 0.3, 0.1])):
            for tick, _ in zip((100, 200, 300), probs):
                rows.append(
                    {
                        "source_file": "m.dem",
                        "match_id": "m.dem",
                        "round_number": round_no,
                        "tick": tick,
                        "t_alive": 5,
                        "ct_alive": 5,
                        "focus_alive": 5,
                        "enemy_alive": 5,
                        "round_time": (tick - 100) / 64.0,
                        "focus_side": "T",
                        "focus_won": True,
                    }
                )
        snapshots.write_text(
            "\n".join(json.dumps(row) for row in rows) + "\n", encoding="utf-8"
        )
        settings = train_winrate.Settings(
            snapshots=[str(snapshots)], out_dir=self.tmp / "model"
        )
        output = self.tmp / "drops.jsonl"
        with (
            mock.patch.object(
                train_winrate, "_load_model", return_value=(None, {"mode": "tick"})
            ),
            mock.patch.object(
                train_winrate,
                "predict_one",
                side_effect=lambda model, meta, row: {
                    100: 0.8,
                    200: 0.5,
                    300: 0.2,
                }[row["tick"]],
            ),
        ):
            self.assertEqual(
                train_winrate.run_drop_points(settings, top=5, out_path=str(output)), 0
            )
        items = [
            json.loads(line) for line in output.read_text(encoding="utf-8").splitlines()
        ]
        self.assertEqual(len(items), 4)
        self.assertGreaterEqual(items[0]["drop"], items[-1]["drop"])
        self.assertEqual(items[0]["event"], "tick")


class WinrateConfigTest(unittest.TestCase):
    def test_config_values(self):
        cfg = train_winrate.load_config(LEARN / "configs" / "winrate.yaml")
        settings = train_winrate.settings_from_config(cfg)
        self.assertEqual(settings.min_rounds, 1000)
        self.assertEqual(settings.min_auc_delta, 0.10)
        self.assertEqual(settings.n_estimators, 300)
        self.assertEqual(settings.cv_folds, 5)


if __name__ == "__main__":
    unittest.main(verbosity=2)
