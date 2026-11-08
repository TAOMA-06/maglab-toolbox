#!/usr/bin/env python3
"""A 层管线自测：synthetic JSONL → build_sft 端到端 → train_sft 命令构造。

设计约束：
- 只用标准库（unittest），**没有 mlx / torch / pytest 也必须全绿**；
- 不写仓库目录（全部产物落在临时目录）；
- 断言的是「事实可追溯」：completion 里出现的每个 tick 都必须在 prompt 里出现过。

跑法：
  python3 -m unittest discover -s learn/tests -v
  python3 learn/tests/test_a_pipeline.py
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
LEARN = HERE.parent
REPO = LEARN.parent
sys.path.insert(0, str(LEARN))

import build_sft  # noqa: E402
import train_sft  # noqa: E402

TICK_RE = re.compile(r"\[tick\s*(\d+)\]")
GOTO_RE = re.compile(r"demo_gototick\s*(\d+)")

FAKE_TEACHER = """\
import sys
sys.stdin.read()
print('**回合1 结论**：外部 teacher 的假输出。 回合1 [tick 999999]')
"""


def _round(
    match_id,
    rnd,
    *,
    side="CT",
    winner="CT",
    focus_won=True,
    tags=(),
    detail=None,
    tick_start=None,
    end_reason="elimination",
    opening=None,
    trades=None,
    focus_death=None,
    clutch=None,
    man_adv=None,
    util_before=None,
    util_events=None,
    alive=None,
    bomb=None,
    with_meta=False,
    context_extra=None,
):
    tick_start = tick_start if tick_start is not None else 1000 * rnd
    rec = {}
    if with_meta:
        rec["meta"] = {
            "match_id": match_id,
            "map": "de_mirage",
            "focus_player_name": "FreeZe",
            "focus_side": "CT",
            "tick_rate": 64,
        }
    ctx = {
        "round": rnd,
        "half": "H1",
        "tick_start": tick_start,
        "tick_end": tick_start + 3000,
        "focus_side": side,
        "score_before": {"t": rnd - 1, "ct": rnd},
        "score_after": {"t": rnd, "ct": rnd},
    }
    if context_extra:
        ctx.update(context_extra)
    rec["context"] = ctx
    rec["economy"] = {
        "t": {"buy": "full", "equip_value": 23500, "money": 3200, "players": 5},
        "ct": {"buy": "eco", "equip_value": 4200, "money": 1100, "players": 5},
        "focus": {"buy": "full"},
    }
    rec["utility"] = {
        "before_contact": util_before if util_before is not None else {"t": 3, "ct": 5},
        "events": util_events or [],
    }
    rec["alive"] = alive or [
        {"tick": tick_start, "t": 5, "ct": 5},
        {"tick": tick_start + 64, "t": 4, "ct": 5},
    ]
    rec["opening"] = opening
    rec["trades"] = trades or []
    rec["focus_death"] = focus_death
    rec["clutch"] = clutch
    rec["man_adv"] = man_adv or []
    rec["bomb"] = bomb
    rec["outcome"] = {
        "winner_side": winner,
        "focus_won": focus_won,
        "end_reason": end_reason,
        "win_cost": 0.62,
        "cause_tags": list(tags),
        "cause_detail": detail or {},
    }
    return rec


def build_fixture(path):
    """4 场比赛 × 3 回合，覆盖：带标签丢分 / 无标签丢分 / 赢 / 平 / 残局 / 炸弹 / 道具。"""
    rows = []

    # m-alpha：首行带 meta，其余行省略（验证 meta 继承）；第 2 回合带 B 层预测
    rows.append(_round(
        "m-alpha", 1,
        opening={"tick": 1064, "killer_name": "FreeZe", "killer_side": "CT", "victim_name": "aaa",
                 "victim_side": "T", "weapon": "M4A1-S", "traded_within_window": True, "focus_role": "killer"},
        bomb={"planted": True, "site": "A", "tick": 2000, "player": "bbb", "defused": True, "defuse_tick": 2600},
        with_meta=True,
    ))
    rows.append(_round(
        "m-alpha", 2, side="T", winner="CT", focus_won=False,
        tags=("trade_fail", "utility_gap"),
        detail={"trade_fail": "max 死后 4.2 秒无人补枪",
                "utility_gap": "交火前只交了 3 颗道具，对面 5 颗"},
        tick_start=4000,
        opening={"tick": 4064, "killer_name": "aaa", "killer_side": "CT", "victim_name": "max",
                 "victim_side": "T", "weapon": "AWP", "traded_within_window": False, "focus_role": "none"},
        trades=[{"tick": 4100, "victim_name": "max", "killed_name": "aaa", "avenger_name": "无人",
                 "success": False, "delay_sec": 4.2}],
        focus_death={"tick": 5200, "round_time": 41.2, "killer_name": "Marts", "killer_side": "CT",
                     "weapon": "P250", "pos_label": "B 点附近", "utility_held": 2, "is_first_death": True,
                     "traded": False, "headshot": False, "through_smoke": False},
        man_adv=[{"start_tick": 4700, "end_tick": 5100, "adv_side": "CT", "adv_delta": 2, "threw": False}],
        util_events=[{"tick": 4090, "thrower": "FreeZe", "type": "smoke", "land": "B 门"}],
        bomb={"planted": True, "site": "B", "tick": 5300, "player": "Marts", "exploded": True},
        end_reason="bomb_exploded",
    ))
    rows.append(_round("m-alpha", 3, side="T", winner="CT", focus_won=False, tick_start=7000,
                       end_reason="time", context_extra={"half": "H1"}))

    # m-bravo：赢在残局 / 输在残局决策
    rows.append(_round(
        "m-bravo", 1, winner="CT", focus_won=True, with_meta=True,
        clutch={"start_tick": 1600, "end_tick": 2400, "situation": "1v2", "focus_in_clutch": True,
                "result": "won", "winner_side": "CT", "advantageous": False},
        bomb={"planted": True, "site": "A", "tick": 1500, "player": "bbb", "defused": True, "defuse_tick": 2300},
        end_reason="bomb_defused",
    ))
    rows.append(_round(
        "m-bravo", 2, side="T", winner="CT", focus_won=False, tick_start=4000,
        tags=("clutch_throw",), detail={"clutch_throw": "1v2 残局里单独追人被反杀"},
        clutch={"start_tick": 5000, "end_tick": 5800, "situation": "1v2", "focus_in_clutch": True,
                "result": "lost", "winner_side": "CT", "advantageous": True},
    ))
    rows.append(_round("m-bravo", 3, side="T", winner="T", focus_won=True, tick_start=7000,
                       opening={"tick": 7064, "killer_name": "bbb", "killer_side": "CT", "victim_name": "ccc",
                                "victim_side": "T", "weapon": "AK-47", "traded_within_window": True,
                                "focus_role": "none"}))

    # m-charlie：经济局强起 / 赢 / 平局（winner=draw，focus_won=None）
    rows.append(_round(
        "m-charlie", 1, side="T", winner="CT", focus_won=False, tags=("eco_force",),
        detail={"eco_force": "T 强起 4 把 AK 撞 CT 全起"}, end_reason="elimination",
        opening={"tick": 1064, "killer_name": "ddd", "killer_side": "CT", "victim_name": "eee",
                 "victim_side": "T", "weapon": "M4A4", "traded_within_window": False, "focus_role": "victim"},
    ))
    rows.append(_round("m-charlie", 2, winner="CT", focus_won=True, tick_start=4000))
    rows.append(_round("m-charlie", 3, winner="draw", focus_won=None, tick_start=7000, end_reason="draw"))

    # m-delta：首杀白给 + 人数优势被翻 / 赢 / 无标签丢分
    rows.append(_round(
        "m-delta", 1, side="CT", winner="T", focus_won=False, with_meta=True,
        tags=("first_blood_gift", "man_advantage_lost"),
        detail={"first_blood_gift": "焦点玩家首死且未被补",
                "man_advantage_lost": "5v4 后被逐个击破"},
        opening={"tick": 1064, "killer_name": "fff", "killer_side": "T", "victim_name": "FreeZe",
                 "victim_side": "CT", "weapon": "Glock-18", "traded_within_window": False, "focus_role": "victim"},
        man_adv=[{"start_tick": 1500, "end_tick": 2600, "adv_side": "CT", "adv_delta": 1, "threw": True}],
    ))
    rows.append(_round("m-delta", 2, winner="CT", focus_won=True, tick_start=4000))
    rows.append(_round("m-delta", 3, side="T", winner="CT", focus_won=False, tick_start=7000))

    with path.open("w", encoding="utf-8") as fh:
        for rec in rows:
            fh.write(json.dumps(rec, ensure_ascii=False) + "\n")


class APipelineTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = Path(tempfile.mkdtemp(prefix="cs2-learn-test-"))
        cls.rounds = cls.tmp / "rounds.jsonl"
        build_fixture(cls.rounds)
        cls.probs = cls.tmp / "probs.jsonl"
        cls.probs.write_text("\n".join([
            json.dumps({"match_id": "m-alpha", "round": 2, "pro_win_prob": 0.90}),
            json.dumps({"match_id": "m-charlie", "round": 3, "pro_win_prob": 0.10}),
        ]) + "\n", encoding="utf-8")

        cls.out = cls.tmp / "sft"
        cls.result = cls.run_build(cls.out, "--valid-ratio", "0.34", "--seed", "0",
                                   "--emit-meta", "--probs", str(cls.probs))
        cls.teacher_script = cls.tmp / "fake_teacher.py"
        cls.teacher_script.write_text(FAKE_TEACHER, encoding="utf-8")

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    # -- helpers ----------------------------------------------------------
    @classmethod
    def run_build(cls, out_dir, *extra):
        cmd = [sys.executable, str(LEARN / "build_sft.py"),
               "--input", str(cls.rounds), "--out-dir", str(out_dir)] + list(extra)
        return subprocess.run(cmd, capture_output=True, text=True)

    @staticmethod
    def load_rows(out_dir):
        rows = []
        for name in ("train.jsonl", "valid.jsonl"):
            p = Path(out_dir) / name
            if p.exists():
                rows.extend(json.loads(line) for line in p.read_text(encoding="utf-8").splitlines() if line.strip())
        return rows

    @staticmethod
    def prompt_ticks(prompt):
        return {int(t) for t in TICK_RE.findall(prompt)} | {int(t) for t in GOTO_RE.findall(prompt)}

    @staticmethod
    def completion_ticks(completion):
        return {int(t) for t in TICK_RE.findall(completion)} | {int(t) for t in GOTO_RE.findall(completion)}

    # -- build_sft 端到端 --------------------------------------------------
    def test_1_build_runs_and_writes_both_splits(self):
        self.assertEqual(self.result.returncode, 0, msg=self.result.stderr)
        self.assertTrue((self.out / "train.jsonl").exists())
        self.assertTrue((self.out / "valid.jsonl").exists(), "4 场比赛按 0.34 切分应至少有一场进 valid")
        rows = self.load_rows(self.out)
        self.assertEqual(len(rows), 12)
        for row in rows:
            self.assertIn("prompt", row)
            self.assertIn("completion", row)
            self.assertTrue(row["prompt"].strip())
            self.assertTrue(row["completion"].strip())

    def test_2_prompt_has_round_number_and_tick_anchors(self):
        for row in self.load_rows(self.out):
            rnd = row["meta"]["round"]
            prompt = row["prompt"]
            self.assertIn("## 回合 {}".format(rnd), prompt)
            self.assertIn("R 只能是 {}".format(rnd), prompt)
            self.assertIn("tick 区间", prompt)
            self.assertIn("demo_gototick", prompt)
            self.assertIn("可引用 tick", prompt)
            self.assertTrue(self.prompt_ticks(prompt), "prompt 必须带 tick 锚点")

    def test_3_completion_only_cites_ticks_that_exist_in_prompt(self):
        for row in self.load_rows(self.out):
            allowed = self.prompt_ticks(row["prompt"])
            cited = self.completion_ticks(row["completion"])
            self.assertTrue(cited, "completion 必须带 tick 引用")
            self.assertTrue(cited <= allowed, "completion 引用了 prompt 里没有的 tick：{}".format(cited - allowed))

    def test_4_completion_header_and_citation_format(self):
        for row in self.load_rows(self.out):
            rnd = row["meta"]["round"]
            completion = row["completion"]
            self.assertIn("**回合{} 结论**".format(rnd), completion)
            self.assertIn("## 发生了什么", completion)
            self.assertIn("## 为什么", completion)
            self.assertIn("## 下把怎么做", completion)
            self.assertIn("回合{} [tick".format(rnd), completion)
            self.assertIn("demo_gototick", completion)
            self.assertNotIn("<", completion, "completion 里不许留模板占位符")

    def test_5_cause_detail_is_quoted_verbatim(self):
        rows = {row["meta"]["round"]: row for row in self.load_rows(self.out) if row["meta"]["match_id"] == "m-alpha"}
        completion = rows[2]["completion"]
        self.assertIn("max 死后 4.2 秒无人补枪", completion)
        self.assertIn("补枪失败", completion)
        self.assertIn("道具缺口", completion)

    def test_6_split_is_by_match_not_by_round(self):
        train = [json.loads(x) for x in (self.out / "train.jsonl").read_text(encoding="utf-8").splitlines() if x]
        valid = [json.loads(x) for x in (self.out / "valid.jsonl").read_text(encoding="utf-8").splitlines() if x]
        train_matches = {r["meta"]["match_id"] for r in train}
        valid_matches = {r["meta"]["match_id"] for r in valid}
        self.assertFalse(train_matches & valid_matches, "同一场比赛的回合不能同时进 train/valid")
        self.assertEqual(len(train) + len(valid), 12)
        for group in (train, valid):
            per_match = {}
            for r in group:
                per_match.setdefault(r["meta"]["match_id"], set()).add(r["meta"]["round"])
            for match_id, rounds in per_match.items():
                self.assertEqual(rounds, {1, 2, 3}, "{} 的回合被切散：{}".format(match_id, rounds))

    def test_7_build_is_deterministic(self):
        out2 = self.tmp / "sft-again"
        again = self.run_build(out2, "--valid-ratio", "0.34", "--seed", "0",
                               "--emit-meta", "--probs", str(self.probs))
        self.assertEqual(again.returncode, 0, msg=again.stderr)
        for name in ("train.jsonl", "valid.jsonl"):
            self.assertEqual((self.out / name).read_bytes(), (out2 / name).read_bytes(),
                             "{} 两次输出不一致（切分/排序必须可复现）".format(name))

    def test_8_b_layer_prob_is_used_as_context_and_reason(self):
        rows = [r for r in self.load_rows(self.out) if r["meta"]["match_id"] == "m-alpha" and r["meta"]["round"] == 2]
        self.assertEqual(len(rows), 1)
        row = rows[0]
        self.assertIn("模型对照", row["prompt"])
        self.assertIn("0.90", row["prompt"])
        self.assertIn("B 层模型", row["completion"])
        self.assertIn("0.90", row["completion"])

    def test_9_emit_meta_off_keeps_keys_minimal(self):
        out = self.tmp / "sft-plain"
        r = self.run_build(out, "--valid-ratio", "0.34", "--seed", "0")
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        for row in self.load_rows(out):
            self.assertEqual(set(row.keys()), {"prompt", "completion"})

    def test_10_chat_format_uses_messages(self):
        out = self.tmp / "sft-chat"
        r = self.run_build(out, "--format", "chat", "--valid-ratio", "0.34", "--seed", "0")
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        row = self.load_rows(out)[0]
        self.assertIn("messages", row)
        roles = [m["role"] for m in row["messages"]]
        self.assertEqual(roles, ["system", "user", "assistant"])
        self.assertIn("R 只能是", row["messages"][0]["content"])

    def test_11_llm_teacher_without_cmd_fails_clearly(self):
        r = self.run_build(self.tmp / "sft-no-cmd", "--teacher", "llm", "--valid-ratio", "0")
        self.assertEqual(r.returncode, 2)
        self.assertIn("--teacher-cmd", r.stderr)
        self.assertNotIn("Traceback", r.stderr)

    def test_12_llm_teacher_bad_ticks_policy(self):
        out_warn = self.tmp / "sft-warn"
        r = self.run_build(out_warn, "--teacher", "llm", "--teacher-cmd",
                           "{} {}".format(sys.executable, self.teacher_script),
                           "--on-bad-teacher", "warn", "--valid-ratio", "0")
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        self.assertIn("引用越界", r.stderr)
        self.assertTrue(self.load_rows(out_warn)[0]["completion"].endswith("[tick 999999]"))

        out_drop = self.tmp / "sft-drop"
        r = self.run_build(out_drop, "--teacher", "llm", "--teacher-cmd",
                           "{} {}".format(sys.executable, self.teacher_script),
                           "--on-bad-teacher", "drop", "--valid-ratio", "0")
        self.assertEqual(r.returncode, 3)
        self.assertIn("teacher 不合格 12", r.stderr)
        self.assertFalse((out_drop / "train.jsonl").exists())

    def test_13_tickless_input_never_invents_a_tick(self):
        src = self.tmp / "tickless.jsonl"
        rec = {
            "context": {"round": 5, "focus_side": "CT"},
            "outcome": {"winner_side": "CT", "focus_won": True, "end_reason": "elimination",
                        "cause_tags": []},
        }
        src.write_text(json.dumps(rec, ensure_ascii=False) + "\n", encoding="utf-8")
        out = self.tmp / "sft-tickless"
        r = subprocess.run([sys.executable, str(LEARN / "build_sft.py"), "--input", str(src),
                            "--out-dir", str(out), "--valid-ratio", "0"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        row = self.load_rows(out)[0]
        self.assertIn("数据里没有", row["prompt"])
        self.assertNotIn("[tick", row["completion"])
        self.assertIsNone(re.search(r"demo_gototick\s*\d", row["completion"]),
                          "没有 tick 时不许编出 demo_gototick N")
        self.assertIn("回合5", row["completion"])

    def test_14_analysis_json_is_accepted(self):
        analysis = REPO / "testdata" / "e2e-out" / "analysis.json"
        if not analysis.exists():
            self.skipTest("没有 testdata/e2e-out/analysis.json")
        out = self.tmp / "sft-analysis"
        r = subprocess.run([sys.executable, str(LEARN / "build_sft.py"), "--input", str(analysis),
                            "--out-dir", str(out), "--valid-ratio", "0", "--emit-meta"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        rows = self.load_rows(out)
        self.assertEqual(len(rows), 17)
        for row in rows:
            self.assertIn("## 回合 {}".format(row["meta"]["round"]), row["prompt"])
            self.assertIn("回合{} [tick".format(row["meta"]["round"]), row["completion"])

    def test_22_completion_numbers_come_from_prompt(self):
        """设计锁：completion 里的数字必须能在 prompt 里找到，不许自造统计量。"""
        def numbers(text):
            return set(re.findall(r"\d+(?:\.\d+)?", text))

        for row in self.load_rows(self.out):
            extra = numbers(row["completion"]) - numbers(row["prompt"])
            self.assertFalse(extra, "回合 {} 的 completion 出现 prompt 里没有的数字：{}".format(
                row["meta"]["round"], sorted(extra)))

    # -- train_sft：无 mlx 也要优雅 ---------------------------------------
    def test_15_dry_run_switches_base_model_in_one_flag(self):
        r = subprocess.run([sys.executable, str(LEARN / "train_sft.py"), "--dry-run",
                            "--set", "base_model=mlx-community/Qwen3-1.7B-4bit"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        self.assertIn('model: "mlx-community/Qwen3-1.7B-4bit"', r.stdout)
        self.assertNotIn('model: "mlx-community/Qwen2.5-1.5B-Instruct-4bit"', r.stdout)
        self.assertIn("mlx_lm", r.stdout)
        self.assertIn("lora_parameters", r.stdout)

    def test_16_mlx_missing_exits_gracefully_with_hint(self):
        cmd = [sys.executable, str(LEARN / "train_sft.py"), "--check", "--data-dir", str(self.out)]
        r = subprocess.run(cmd, capture_output=True, text=True)
        combined = r.stdout + r.stderr
        self.assertNotIn("Traceback", combined)
        if train_sft.mlx_missing():
            self.assertEqual(r.returncode, 2)
            self.assertIn("mlx-lm", combined)
            self.assertIn("uv pip install", combined)
        else:
            self.assertEqual(r.returncode, 0, msg=combined)

    def test_17_fuse_without_adapter_fails_clearly(self):
        r = subprocess.run([sys.executable, str(LEARN / "train_sft.py"), "--fuse",
                            "--adapter-path", str(self.tmp / "no-such-adapter")],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 2)
        self.assertIn("适配器不存在", r.stderr)
        self.assertNotIn("Traceback", r.stderr)

    def test_18_list_models_shows_alternatives(self):
        r = subprocess.run([sys.executable, str(LEARN / "train_sft.py"), "--list-models"],
                           capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, msg=r.stderr)
        self.assertIn("mlx-community/Qwen2.5-1.5B-Instruct-4bit", r.stdout)
        for name in ("DeepSeek-R1-Distill-Qwen-1.5B-4bit", "Qwen3-1.7B-4bit", "Qwen3-0.6B-4bit"):
            self.assertIn(name, r.stdout)

    # -- 单元级：YAML / 校验 / 归一化 -------------------------------------
    def test_19_mini_yaml_matches_pyyaml(self):
        text = (LEARN / "configs" / "model.yaml").read_text(encoding="utf-8")
        mini = train_sft.mini_yaml(text)
        self.assertEqual(mini["base_model"], "mlx-community/Qwen2.5-1.5B-Instruct-4bit")
        self.assertEqual(mini["lora"]["num_layers"], 16)
        self.assertEqual(mini["lora"]["learning_rate"], 1e-4)
        self.assertEqual(len(mini["alternatives"]), 3)
        try:
            import yaml
        except ImportError:
            return
        self.assertEqual(mini, yaml.safe_load(text))

    def test_20_validate_completion_flags_out_of_scope_ticks(self):
        facts = build_sft.RoundFacts(round_no=3, tick_start=100)
        self.assertEqual(build_sft.validate_completion("回合3 [tick 100]", facts, {100}), [])
        bad_tick = build_sft.validate_completion("回合3 [tick 999]", facts, {100})
        self.assertTrue(bad_tick and "999" in bad_tick[0])
        bad_round = build_sft.validate_completion("回合4 [tick 100]", facts, {100})
        self.assertTrue(bad_round and "回合 4" in bad_round[0])

    def test_21_settings_read_from_config(self):
        cfg = train_sft.load_config(LEARN / "configs" / "model.yaml")
        s = train_sft.settings_from_config(cfg)
        self.assertEqual(s.iters, 600)
        self.assertEqual(s.batch_size, 1)
        self.assertEqual(s.adapter_path.name, "coach-adapter")
        self.assertIn("lora_parameters", train_sft.training_yaml(s))


if __name__ == "__main__":
    unittest.main(verbosity=2)
