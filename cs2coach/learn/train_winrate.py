#!/usr/bin/env python3
"""train_winrate.py —— TRAIN-PLAN v2 主线：回合胜率/价值模型训练入口。

做什么：
1. 读回合级 samples.jsonl（v0 兼容）或 match.json / tick 快照（v2 主线）；
2. 按 **比赛（match）分组** 切 train/valid，杜绝同一场 demo 泄漏；
3. 训练 LightGBM，报 AUC / logloss / Brier / accuracy；
4. 训练「人数差 + 经济」逻辑回归基线，按 TRAIN-PLAN §2 验收：
   `model_auc >= baseline_auc + 0.10`；
5. 数据量 <1000 回合时标记「降级玩具版」，不冒充 T1 验收；
6. `--predict` 单条预测；`--drop-points` 输出 T2 的「胜率掉点」候选（带 tick 事件证据）。

用法：
  python3 learn/train_winrate.py --data out/samples.jsonl
  python3 learn/train_winrate.py --match-json out/ --out-dir learn/models/winrate
  python3 learn/train_winrate.py --check --match-json out/
  python3 learn/train_winrate.py --dry-run --match-json out/
  python3 learn/train_winrate.py --predict '{"map":"mirage",...}'
  python3 learn/train_winrate.py --drop-points out/ --top 20

依赖：lightgbm / scikit-learn / numpy（learn/requirements.txt）。
没装依赖时 --check / --dry-run 仍可跑；训练会打印安装指引并退出 2。
"""

from __future__ import annotations

import argparse
import json
import platform
import shlex
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence, Tuple

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent
DEFAULT_CONFIG = HERE / "configs" / "winrate.yaml"
DEFAULT_OUT_DIR = HERE / "models" / "winrate"

try:
    from train_sft import coerce, dig, load_config, put
    from winrate_data import (
        audit,
        categorical_names,
        encode_matrix,
        encode_single,
        feature_names,
        fit_cat_encoders,
        load_rows,
        prepare_rows,
        read_jsonl,
        split_by_match,
    )
except ImportError:  # pragma: no cover
    from .train_sft import coerce, dig, load_config, put  # type: ignore
    from .winrate_data import (  # type: ignore
        audit,
        categorical_names,
        encode_matrix,
        encode_single,
        feature_names,
        fit_cat_encoders,
        load_rows,
        prepare_rows,
        read_jsonl,
        split_by_match,
    )


INSTALL_HINT = """\
✗ 没找到训练依赖（lightgbm / scikit-learn / numpy）。

安装（独立 venv，别污染系统 Python）：
  uv venv --python 3.12 {venv}
  uv pip install --python {venv}/bin/python -r learn/requirements.txt

或者：
  {venv}/bin/python -m pip install -r learn/requirements.txt

装好后先自检：
  {venv}/bin/python learn/train_winrate.py --check --match-json <match.json|目录>
"""


# --------------------------------------------------------------------------
# 配置
# --------------------------------------------------------------------------


def _resolve(path_text: str) -> Path:
    path = Path(path_text).expanduser()
    return path if path.is_absolute() else (REPO_ROOT / path)


def _as_list(value: Any) -> List[str]:
    if value is None or value == "":
        return []
    if isinstance(value, str):
        return [value]
    if isinstance(value, (list, tuple)):
        return [str(item) for item in value if str(item).strip()]
    return [str(value)]


def _as_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value.strip().lower() in {"1", "true", "yes", "on"}
    if value is None:
        return default
    return bool(value)


@dataclass
class Settings:
    data: List[str] = field(default_factory=list)
    snapshots: List[str] = field(default_factory=list)
    match_json: List[str] = field(default_factory=list)
    out_dir: Path = DEFAULT_OUT_DIR
    mode: str = "auto"
    min_rounds: int = 1000
    min_auc_delta: float = 0.10
    valid_ratio: float = 0.2
    cv_folds: int = 5
    seed: int = 42
    drop_draws: bool = True
    n_estimators: int = 300
    learning_rate: float = 0.03
    max_depth: int = 5
    num_leaves: int = 31
    min_child_samples: int = 10
    subsample: float = 0.8
    colsample_bytree: float = 0.8
    reg_alpha: float = 0.1
    reg_lambda: float = 0.1
    n_jobs: int = -1


def settings_from_config(cfg: Dict[str, Any]) -> Settings:
    return Settings(
        data=_as_list(dig(cfg, "data", [])),
        snapshots=_as_list(dig(cfg, "snapshots", [])),
        match_json=_as_list(dig(cfg, "match_json", [])),
        out_dir=_resolve(str(dig(cfg, "out_dir", str(DEFAULT_OUT_DIR)))),
        mode=str(dig(cfg, "mode", "auto")),
        min_rounds=int(dig(cfg, "min_rounds", 1000)),
        min_auc_delta=float(dig(cfg, "min_auc_delta", 0.10)),
        valid_ratio=float(dig(cfg, "valid_ratio", 0.2)),
        cv_folds=int(dig(cfg, "cv_folds", 5)),
        seed=int(dig(cfg, "seed", 42)),
        drop_draws=_as_bool(dig(cfg, "drop_draws", True), True),
        n_estimators=int(dig(cfg, "lightgbm.n_estimators", 300)),
        learning_rate=float(dig(cfg, "lightgbm.learning_rate", 0.03)),
        max_depth=int(dig(cfg, "lightgbm.max_depth", 5)),
        num_leaves=int(dig(cfg, "lightgbm.num_leaves", 31)),
        min_child_samples=int(dig(cfg, "lightgbm.min_child_samples", 10)),
        subsample=float(dig(cfg, "lightgbm.subsample", 0.8)),
        colsample_bytree=float(dig(cfg, "lightgbm.colsample_bytree", 0.8)),
        reg_alpha=float(dig(cfg, "lightgbm.reg_alpha", 0.1)),
        reg_lambda=float(dig(cfg, "lightgbm.reg_lambda", 0.1)),
        n_jobs=int(dig(cfg, "lightgbm.n_jobs", -1)),
    )


def lightgbm_params(s: Settings) -> Dict[str, Any]:
    return {
        "objective": "binary",
        "metric": "auc",
        "verbosity": -1,
        "n_estimators": s.n_estimators,
        "learning_rate": s.learning_rate,
        "max_depth": s.max_depth,
        "num_leaves": s.num_leaves,
        "min_child_samples": s.min_child_samples,
        "subsample": s.subsample,
        "colsample_bytree": s.colsample_bytree,
        "reg_alpha": s.reg_alpha,
        "reg_lambda": s.reg_lambda,
        "random_state": s.seed,
        "n_jobs": s.n_jobs,
    }


# --------------------------------------------------------------------------
# 依赖 / 指标
# --------------------------------------------------------------------------


def missing_deps() -> List[str]:
    missing = []
    for name in ("numpy", "sklearn", "lightgbm"):
        try:
            __import__(name)
        except ImportError:
            missing.append(name)
    return missing


def _binary_metrics(y_true: Any, proba: Any) -> Dict[str, float]:
    import numpy as np
    from sklearn.metrics import (
        accuracy_score,
        brier_score_loss,
        log_loss,
        roc_auc_score,
    )

    y = np.asarray(y_true, dtype=int)
    p = np.asarray(proba, dtype=float)
    p = np.clip(p, 1e-6, 1 - 1e-6)
    metrics = {
        "accuracy": round(float(accuracy_score(y, (p >= 0.5).astype(int))), 4),
        "brier": round(float(brier_score_loss(y, p)), 4),
        "logloss": round(float(log_loss(y, p, labels=[0, 1])), 4),
        "auc": 0.5,
    }
    if len(set(y.tolist())) > 1:
        metrics["auc"] = round(float(roc_auc_score(y, p)), 4)
    return metrics


def baseline_matrix(X: Any, names: Sequence[str], mode: str) -> Any:
    """「人数差 + 经济」基线：只取人数差、现金差、装备差 3 列。"""
    import numpy as np

    index = {name: i for i, name in enumerate(names)}
    if mode == "round":
        alive = X[:, index["start_t_alive"]] - X[:, index["start_ct_alive"]]
        money = X[:, index["t_money"]] - X[:, index["ct_money"]]
        equip = X[:, index["t_equip_freeze_end"]] - X[:, index["ct_equip_freeze_end"]]
    else:
        alive = X[:, index["alive_diff"]]
        money = X[:, index["money_diff"]]
        equip = X[:, index["equip_diff"]]
    return np.column_stack([alive, money, equip])


def fit_baseline(X_train: Any, y_train: Any, X_eval: Any) -> Any:
    from sklearn.linear_model import LogisticRegression
    from sklearn.pipeline import make_pipeline
    from sklearn.preprocessing import StandardScaler

    model = make_pipeline(
        StandardScaler(),
        LogisticRegression(max_iter=1000, class_weight="balanced", random_state=42),
    )
    if len(set(y_train.tolist())) < 2:
        return None
    model.fit(X_train, y_train)
    return model.predict_proba(X_eval)[:, 1]


def _group_folds(
    groups: Sequence[str], n_splits: int
) -> List[Tuple[List[int], List[int]]]:
    from sklearn.model_selection import GroupKFold

    unique = sorted(set(groups))
    folds = min(n_splits, len(unique))
    if folds < 2:
        return []
    return list(
        GroupKFold(n_splits=folds).split(range(len(groups)), groups=list(groups))
    )


def _oof_predictions(
    X: Any,
    y: Any,
    groups: Sequence[str],
    n_splits: int,
    params: Dict[str, Any],
    cat_indices: Sequence[int],
) -> Tuple[Any, List[Dict[str, float]], Any]:
    import numpy as np
    import lightgbm as lgb

    oof = np.zeros(len(y), dtype=float)
    mask = np.zeros(len(y), dtype=bool)
    fold_metrics: List[Dict[str, float]] = []
    for train_idx, valid_idx in _group_folds(groups, n_splits):
        if len(set(y[train_idx].tolist())) < 2 or len(set(y[valid_idx].tolist())) < 2:
            # 小数据/单类 fold 不硬训，避免 AUC 未定义
            continue
        model = lgb.LGBMClassifier(**params)
        model.fit(
            X[train_idx],
            y[train_idx],
            eval_set=[(X[valid_idx], y[valid_idx])],
            eval_metric="auc",
            categorical_feature=list(cat_indices),
            callbacks=[lgb.early_stopping(50, verbose=False), lgb.log_evaluation(0)],
        )
        proba = model.predict_proba(X[valid_idx])[:, 1]
        oof[valid_idx] = proba
        mask[valid_idx] = True
        metrics = _binary_metrics(y[valid_idx], proba)
        metrics["fold"] = len(fold_metrics) + 1
        fold_metrics.append(metrics)
    return oof, fold_metrics, mask


# --------------------------------------------------------------------------
# 训练
# --------------------------------------------------------------------------


def train(settings: Settings, strict: bool = False, audit_out: str = "") -> int:
    missing = missing_deps()
    if missing:
        print(INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
        print("缺失模块：{}".format("、".join(missing)), file=sys.stderr)
        return 2

    import numpy as np
    import lightgbm as lgb

    try:
        rows, mode = load_rows(
            data=settings.data,
            snapshots=settings.snapshots,
            match_json=settings.match_json,
            mode=settings.mode,
        )
    except (OSError, ValueError) as exc:
        print("读数据失败：{}".format(exc), file=sys.stderr)
        return 2

    prepared = prepare_rows(rows, mode, drop_draws=settings.drop_draws)
    data_audit = audit(rows, mode, prepared)
    if audit_out:
        _write_audit(data_audit, audit_out)
    print_audit(data_audit, settings)

    if not prepared["labels"]:
        print("没有可用标签（focus_won 缺失或全是 draw）", file=sys.stderr)
        return 2
    if len(set(prepared["labels"])) < 2:
        print("标签只有一类，无法训练胜率模型", file=sys.stderr)
        return 2

    names = feature_names(mode)
    cat_names = categorical_names(mode)
    train_idx, valid_idx = split_by_match(
        prepared["groups"], settings.valid_ratio, settings.seed
    )
    train_features = [prepared["feature_rows"][i] for i in train_idx]
    encoders = fit_cat_encoders(train_features, cat_names)
    X_all = np.asarray(
        encode_matrix(prepared["feature_rows"], names, cat_names, encoders), dtype=float
    )
    y_all = np.asarray(prepared["labels"], dtype=int)
    groups_all = [prepared["groups"][i] for i in train_idx]
    X_train, y_train = X_all[train_idx], y_all[train_idx]
    cat_indices = [names.index(name) for name in cat_names]
    params = lightgbm_params(settings)

    print(
        "\n切分（按 match 分组）：train {} 行 / {} 场；valid {} 行 / {} 场".format(
            len(train_idx),
            len(set(groups_all)),
            len(valid_idx),
            len(set(prepared["groups"][i] for i in valid_idx)),
        )
    )

    fold_metrics: List[Dict[str, float]] = []
    oof_train = None
    oof_mask = None
    if len(set(groups_all)) >= 2:
        print("\nGroupKFold（按场次，不泄漏）：")
        oof_train, fold_metrics, oof_mask = _oof_predictions(
            X_train, y_train, groups_all, settings.cv_folds, params, cat_indices
        )
        for item in fold_metrics:
            print(
                "  Fold {}: AUC={:.4f} logloss={:.4f} acc={:.4f}".format(
                    int(item["fold"]), item["auc"], item["logloss"], item["accuracy"]
                )
            )

    if valid_idx:
        X_valid, y_valid = X_all[valid_idx], y_all[valid_idx]
        model = lgb.LGBMClassifier(**params)
        model.fit(
            X_train,
            y_train,
            eval_set=[(X_valid, y_valid)],
            eval_metric="auc",
            categorical_feature=cat_indices,
            callbacks=[lgb.early_stopping(50, verbose=False), lgb.log_evaluation(0)],
        )
        proba = model.predict_proba(X_valid)[:, 1]
        model_metrics = _binary_metrics(y_valid, proba)
        eval_source = "holdout_match_split"
    else:
        model = lgb.LGBMClassifier(**params)
        model.fit(X_train, y_train, categorical_feature=cat_indices)
        if (
            fold_metrics
            and oof_train is not None
            and oof_mask is not None
            and oof_mask.any()
        ):
            # 没有 holdout 时，用 OOF 预测评估（仍按场次切分）。
            # 单类 fold 被跳过，不能把未预测行的 0.0 当成预测值。
            model_metrics = _binary_metrics(y_train[oof_mask], oof_train[oof_mask])
            eval_source = "group_cv_oof"
        else:
            model_metrics = _binary_metrics(y_train, model.predict_proba(X_train)[:, 1])
            eval_source = "train_only_no_valid"

    # 基线：人数差 + 经济（逻辑回归，标准化）
    baseline_proba = None
    if valid_idx:
        base_train = baseline_matrix(X_train, names, mode)
        base_valid = baseline_matrix(X_valid, names, mode)
        baseline_proba = fit_baseline(base_train, y_train, base_valid)
        if baseline_proba is not None:
            baseline_metrics = _binary_metrics(y_valid, baseline_proba)
        else:
            baseline_metrics = {
                "auc": 0.5,
                "accuracy": 0.5,
                "logloss": 0.6931,
                "brier": 0.25,
            }
    elif fold_metrics:
        base_X = baseline_matrix(X_train, names, mode)
        oof = np.zeros(len(y_train), dtype=float)
        mask = np.zeros(len(y_train), dtype=bool)
        for tr, va in _group_folds(groups_all, settings.cv_folds):
            proba = fit_baseline(base_X[tr], y_train[tr], base_X[va])
            if proba is not None:
                oof[va] = proba
                mask[va] = True
        baseline_metrics = (
            _binary_metrics(y_train[mask], oof[mask])
            if mask.any()
            else {"auc": 0.5, "accuracy": 0.5, "logloss": 0.6931, "brier": 0.25}
        )
    else:
        baseline_metrics = {
            "auc": 0.5,
            "accuracy": 0.5,
            "logloss": 0.6931,
            "brier": 0.25,
        }

    data_pass = data_audit["rounds"] >= settings.min_rounds
    valid_pass = bool(valid_idx)
    mode_pass = mode == "tick"
    auc_delta = round(float(model_metrics["auc"] - baseline_metrics["auc"]), 4)
    auc_pass = auc_delta >= settings.min_auc_delta
    # 没有按场次留出的 valid 时，AUC 只是训练集自评；round v0 只是兼容路径。
    t1_pass = bool(data_pass and valid_pass and mode_pass and auc_pass)

    meta = {
        "version": 2,
        "generated_at": _now(),
        "mode": mode,
        "data_sources": {
            "data": settings.data,
            "snapshots": settings.snapshots,
            "match_json": settings.match_json,
        },
        "data_audit": data_audit,
        "split": {
            "train_rows": len(train_idx),
            "valid_rows": len(valid_idx),
            "train_matches": len(set(groups_all)),
            "valid_matches": len(set(prepared["groups"][i] for i in valid_idx)),
            "valid_ratio": settings.valid_ratio,
            "seed": settings.seed,
        },
        "feature_names": names,
        "cat_names": cat_names,
        "cat_encoders": encoders,
        "feature_importances": _top_features(model, names),
        "metrics": model_metrics,
        "baseline": {
            "name": "人数差+经济 逻辑回归",
            "features": ["alive_diff", "money_diff", "equip_diff"],
            **baseline_metrics,
        },
        "fold_metrics": fold_metrics,
        "eval_source": eval_source,
        "lightgbm_params": params,
        "data_gate": {
            "rounds": data_audit["rounds"],
            "min_rounds": settings.min_rounds,
            "pass": data_pass,
            "degraded": not data_pass,
        },
        "acceptance": {
            "min_auc_delta": settings.min_auc_delta,
            "auc_delta": auc_delta,
            "auc_pass": auc_pass,
            "valid_pass": valid_pass,
            "mode_pass": mode_pass,
            "t1_pass": t1_pass,
        },
    }

    out_dir = settings.out_dir
    out_dir.mkdir(parents=True, exist_ok=True)
    model.booster_.save_model(str(out_dir / "winrate.txt"))
    (out_dir / "winrate_meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    write_report(out_dir / "report.md", meta)
    print_metrics(meta, out_dir)

    if strict and not t1_pass:
        print("\n✗ --strict：T1 验收未通过", file=sys.stderr)
        return 2
    return 0


def _now() -> str:
    from datetime import datetime, timezone

    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _write_audit(data_audit: Dict[str, Any], path_text: str) -> None:
    path = _resolve(path_text)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(data_audit, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print("数据审计      : {}".format(path))


def _top_features(model: Any, names: Sequence[str]) -> List[Dict[str, Any]]:
    importances = None
    import_kind = "split"
    booster = getattr(model, "booster_", None)
    if booster is not None:
        try:  # gain 口径：不被高基数连续/类别特征虚胖（见 cs2-wpa 对比）
            importances = booster.feature_importance(importance_type="gain")
            import_kind = "gain"
        except Exception:
            importances = None
    if importances is None:
        importances = getattr(model, "feature_importances_", None)
    if importances is None:
        return []
    total = float(sum(importances)) or 1.0
    pairs = sorted(
        zip(names, [100.0 * float(v) / total for v in importances]),
        key=lambda item: item[1],
        reverse=True,
    )
    out = [
        {"name": name, "importance": round(float(value), 2)}
        for name, value in pairs[:15]
    ]
    if out:
        out[0]["importance_kind"] = import_kind
    return out


def print_audit(data_audit: Dict[str, Any], settings: Settings) -> None:
    print(
        "数据模式      : {}（round=回合级 v0 / tick=快照主线）".format(
            data_audit["mode"]
        )
    )
    print(
        "样本          : {} 行（可用 {}，跳过 {}）".format(
            data_audit["rows"], data_audit["usable_rows"], data_audit["skipped_rows"]
        )
    )
    print(
        "回合 / 比赛   : {} 回合 / {} 场".format(
            data_audit["rounds"], data_audit["matches"]
        )
    )
    print(
        "标签          : 输 {} / 赢 {}".format(
            data_audit["label_counts"]["focus_won_0"],
            data_audit["label_counts"]["focus_won_1"],
        )
    )
    print("特征数        : {}".format(data_audit["features"]))
    if data_audit["mode"] == "round":
        print(
            "! round 模式是 v0 兼容路径，不算 v2 的 T1；建议用 --match-json 生成 tick 快照。"
        )
    if data_audit["rounds"] < settings.min_rounds:
        print(
            "! 数据量 {} < {}：按 TRAIN-PLAN §5，标记为降级玩具版，不作为 T1 验收。".format(
                data_audit["rounds"], settings.min_rounds
            )
        )


def print_metrics(meta: Dict[str, Any], out_dir: Path) -> None:
    metrics = meta["metrics"]
    baseline = meta["baseline"]
    print(
        "\n模型指标      : AUC={auc:.4f} logloss={logloss:.4f} acc={accuracy:.4f} Brier={brier:.4f}".format(
            **metrics
        )
    )
    print(
        "基线指标      : AUC={auc:.4f} logloss={logloss:.4f} acc={accuracy:.4f}".format(
            **baseline
        )
    )
    print(
        "AUC 差值      : {:+.4f}（门槛 +{:.2f}）".format(
            meta["acceptance"]["auc_delta"], meta["acceptance"]["min_auc_delta"]
        )
    )
    print(
        "数据门槛      : {}（{} / {} 回合）".format(
            "通过" if meta["data_gate"]["pass"] else "降级",
            meta["data_gate"]["rounds"],
            meta["data_gate"]["min_rounds"],
        )
    )
    print(
        "验证集        : {}".format(
            "有（按 match 留出）"
            if meta["acceptance"]["valid_pass"]
            else "无（只有训练集自评，不能算 T1）"
        )
    )
    print(
        "数据模式验收  : {}".format(
            "tick（v2 主线）"
            if meta["acceptance"]["mode_pass"]
            else "round（v0 兼容，不算 T1）"
        )
    )
    print(
        "T1 验收       : {}".format(
            "通过" if meta["acceptance"]["t1_pass"] else "未通过"
        )
    )
    print("模型          : {}".format(out_dir / "winrate.txt"))
    print("元数据        : {}".format(out_dir / "winrate_meta.json"))
    print("报告          : {}".format(out_dir / "report.md"))
    if meta["feature_importances"]:
        print("\nTop features:")
        for item in meta["feature_importances"][:10]:
            print("  {:<32} {:>10.2f}".format(item["name"], item["importance"]))


def write_report(path: Path, meta: Dict[str, Any]) -> None:
    metrics = meta["metrics"]
    baseline = meta["baseline"]
    audit = meta["data_audit"]
    split = meta["split"]
    lines = [
        "# 胜率模型训练报告（TRAIN-PLAN v2）",
        "",
        "- 生成时间：{}".format(meta["generated_at"]),
        "- 数据模式：`{}`（round=回合级 v0 / tick=快照主线）".format(meta["mode"]),
        "- 评估来源：`{}`".format(meta["eval_source"]),
        "",
        "## 数据",
        "",
        "| 项 | 值 |",
        "| --- | --- |",
        "| 样本行 | {} |".format(audit["rows"]),
        "| 可用行 | {} |".format(audit["usable_rows"]),
        "| 回合数 | {} |".format(audit["rounds"]),
        "| 比赛数 | {} |".format(audit["matches"]),
        "| 标签 | 输 {} / 赢 {} |".format(
            audit["label_counts"]["focus_won_0"], audit["label_counts"]["focus_won_1"]
        ),
        "| 数据门槛 | {}（{} / {} 回合） |".format(
            "通过" if meta["data_gate"]["pass"] else "降级",
            meta["data_gate"]["rounds"],
            meta["data_gate"]["min_rounds"],
        ),
        "",
        "## 切分（按比赛分组）",
        "",
        "| 项 | 值 |",
        "| --- | --- |",
        "| train | {} 行 / {} 场 |".format(split["train_rows"], split["train_matches"]),
        "| valid | {} 行 / {} 场 |".format(split["valid_rows"], split["valid_matches"]),
        "",
        "## 指标",
        "",
        "| 模型 | AUC | logloss | accuracy | Brier |",
        "| --- | --- | --- | --- | --- |",
        "| LightGBM | {auc:.4f} | {logloss:.4f} | {accuracy:.4f} | {brier:.4f} |".format(
            **metrics
        ),
        "| 基线（人数差+经济） | {auc:.4f} | {logloss:.4f} | {accuracy:.4f} | {brier:.4f} |".format(
            **baseline
        ),
        "",
        "- AUC 差值：{:+.4f}（门槛 +{:.2f}）".format(
            meta["acceptance"]["auc_delta"], meta["acceptance"]["min_auc_delta"]
        ),
        "- 验证集：{}".format(
            "有（按 match 留出）"
            if meta["acceptance"]["valid_pass"]
            else "无（只有训练集自评，不能算 T1）"
        ),
        "- T1 数据模式：{}".format(
            "tick（v2 主线）"
            if meta["acceptance"]["mode_pass"]
            else "round（v0 兼容，不算 T1）"
        ),
        "- T1 验收：**{}**".format(
            "通过" if meta["acceptance"]["t1_pass"] else "未通过"
        ),
        "",
        "## Top features",
        "",
        "| 特征 | 重要性 |",
        "| --- | --- |",
    ]
    for item in meta["feature_importances"][:15]:
        lines.append("| {} | {:.2f} |".format(item["name"], item["importance"]))
    lines += [
        "",
        "## 说明",
        "",
        "- `round` 模式是 v0 兼容路径，只用到回合开始/开局事实；`tick` 模式才对应 v2 的「局面快照」。",
        "- 数据量 <1000 回合时，本报告是降级玩具版，不能当作 T1 验收。",
        "- 基线只取人数差、现金差、装备差三列；模型 AUC 必须比它高 +0.10 才算 T1 通过。",
        "- 特征里不含 winner/focus_won/end_reason/cause_tags 等 outcome 字段，也不含回合级 planted/defused/exploded；切分按 match，不按回合随机。",
        "",
    ]
    path.write_text("\n".join(lines), encoding="utf-8")


# --------------------------------------------------------------------------
# 预测 / 掉点
# --------------------------------------------------------------------------


def _load_model(out_dir: Path) -> Tuple[Any, Dict[str, Any]]:
    import lightgbm as lgb

    meta_path = out_dir / "winrate_meta.json"
    model_path = out_dir / "winrate.txt"
    if not meta_path.exists() or not model_path.exists():
        raise FileNotFoundError("模型不存在：{} / {}".format(model_path, meta_path))
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    model = lgb.Booster(model_file=str(model_path))
    return model, meta


def predict_one(model: Any, meta: Dict[str, Any], row: Dict[str, Any]) -> float:
    import numpy as np

    values = encode_single(row, meta["mode"], meta)
    proba = model.predict(np.asarray([values], dtype=float))[0]
    return max(0.0, min(1.0, float(proba)))


def run_predict(settings: Settings, payload: str) -> int:
    missing = missing_deps()
    if missing:
        print(INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
        return 2
    try:
        row = json.loads(payload)
    except json.JSONDecodeError as exc:
        print("--predict 需要 JSON object：{}".format(exc), file=sys.stderr)
        return 2
    try:
        model, meta = _load_model(settings.out_dir)
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 2
    if meta.get("mode") == "tick" and "tick" not in row:
        print(
            "这个模型是 tick 模式，--predict 的 JSON 需要 tick 字段（以及 focus_alive 等局面字段）",
            file=sys.stderr,
        )
        return 2
    if meta.get("mode") == "round" and "round_number" not in row:
        print(
            "这个模型是 round 模式，--predict 的 JSON 需要 round_number 等回合字段",
            file=sys.stderr,
        )
        return 2
    proba = predict_one(model, meta, row)
    print(
        json.dumps(
            {
                "pro_win_prob": round(proba, 4),
                "mode": meta.get("mode"),
                "data_gate": meta.get("data_gate"),
                "acceptance": meta.get("acceptance"),
            },
            ensure_ascii=False,
            indent=2,
        )
    )
    return 0


def run_drop_points(settings: Settings, top: int, out_path: str = "") -> int:
    missing = missing_deps()
    if missing:
        print(INSTALL_HINT.format(venv=HERE / ".venv"), file=sys.stderr)
        return 2
    try:
        rows, mode = load_rows(
            data=settings.data,
            snapshots=settings.snapshots,
            match_json=settings.match_json,
            mode=settings.mode,
        )
    except (OSError, ValueError) as exc:
        print("读数据失败：{}".format(exc), file=sys.stderr)
        return 2
    if mode != "tick":
        print(
            "--drop-points 需要 tick 级快照（--match-json 或快照 JSONL）；回合级数据没有掉点。",
            file=sys.stderr,
        )
        return 2
    try:
        model, meta = _load_model(settings.out_dir)
    except (OSError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 2
    if meta.get("mode") != "tick":
        print(
            "--drop-points 需要 tick 模式模型；当前模型是 round 模式。", file=sys.stderr
        )
        return 2

    grouped: Dict[Tuple[str, int], List[Dict[str, Any]]] = {}
    for row in rows:
        key = (
            str(row.get("match_id") or row.get("source_file") or "unknown"),
            int(row.get("round_number") or 0),
        )
        grouped.setdefault(key, []).append(row)

    candidates: List[Dict[str, Any]] = []
    for (match_id, round_no), items in grouped.items():
        items = sorted(items, key=lambda r: int(r.get("tick") or 0))
        if len(items) < 2:
            continue
        probs = [predict_one(model, meta, row) for row in items]
        for i in range(1, len(items)):
            drop = probs[i - 1] - probs[i]
            if drop <= 0:
                continue
            row = items[i]
            previous = items[i - 1]
            candidates.append(
                {
                    "match_id": match_id,
                    "round": round_no,
                    "from_tick": int(previous.get("tick") or 0),
                    "from_round_time": round(
                        float(previous.get("round_time") or 0.0), 1
                    ),
                    "tick": int(row.get("tick") or 0),
                    "round_time": round(float(row.get("round_time") or 0.0), 1),
                    "focus_side": row.get("focus_side"),
                    "from_prob": round(probs[i - 1], 4),
                    "to_prob": round(probs[i], 4),
                    "drop": round(drop, 4),
                    "event": row.get("event_type") or "tick",
                    "event_desc": row.get("event_desc") or "",
                }
            )
    candidates.sort(key=lambda item: item["drop"], reverse=True)
    candidates = candidates[: max(1, top)]
    if not candidates:
        print("没有找到胜率下降点。")
        return 0
    if out_path:
        path = _resolve(out_path)
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("w", encoding="utf-8") as handle:
            for item in candidates:
                handle.write(json.dumps(item, ensure_ascii=False) + "\n")
        print("已写 {} 条掉点 → {}".format(len(candidates), path))
    else:
        for item in candidates:
            print(json.dumps(item, ensure_ascii=False))
    return 0


def _as_agree(value: Any) -> Optional[bool]:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        if value > 0:
            return True
        if value == 0:
            return False
    text = str(value or "").strip().lower()
    if text in {"1", "true", "yes", "agree", "accept", "useful", "认同", "有用"}:
        return True
    if text in {"0", "false", "no", "disagree", "reject", "useless", "不认同", "没用"}:
        return False
    return None


def _drop_key(row: Dict[str, Any]) -> Tuple[str, int, int]:
    return (
        str(row.get("match_id") or row.get("source_file") or "unknown"),
        int(row.get("round") or row.get("round_number") or 0),
        int(row.get("tick") or 0),
    )


def score_drops(
    drops: Sequence[Dict[str, Any]],
    judgments: Sequence[Dict[str, Any]],
) -> Dict[str, Any]:
    """T2 盲测：把人工「认同/不认同」对齐到 --drop-points 输出。"""
    by_key = {_drop_key(row): row for row in drops}
    agree = 0
    disagree = 0
    invalid = 0
    used = 0
    for judgment in judgments:
        target = None
        index = judgment.get("index", judgment.get("id"))
        if isinstance(index, float) and index.is_integer():
            index = int(index)
        if isinstance(index, int) and 1 <= index <= len(drops):
            target = drops[index - 1]
        elif (
            isinstance(index, str) and index.isdigit() and 1 <= int(index) <= len(drops)
        ):
            target = drops[int(index) - 1]
        else:
            target = by_key.get(_drop_key(judgment))
        if target is None:
            invalid += 1
            continue
        verdict = _as_agree(
            judgment.get(
                "agree",
                judgment.get("accepted", judgment.get("useful", judgment.get("认同"))),
            )
        )
        if verdict is None:
            invalid += 1
            continue
        used += 1
        if verdict:
            agree += 1
        else:
            disagree += 1
    total = agree + disagree
    rate = (agree / total) if total else 0.0
    return {
        "drops": len(drops),
        "judgments": len(judgments),
        "used": used,
        "agree": agree,
        "disagree": disagree,
        "invalid": invalid,
        "agreement_rate": round(rate, 4),
    }


def run_score_drops(
    drops_path: str,
    judgments_path: str,
    min_agreement: float = 0.60,
    min_samples: int = 20,
    report_path: str = "",
) -> int:
    try:
        drops = [row for _, row in read_jsonl(_resolve(drops_path))]
        judgments = [row for _, row in read_jsonl(_resolve(judgments_path))]
    except (OSError, ValueError) as exc:
        print("读掉点/判定文件失败：{}".format(exc), file=sys.stderr)
        return 2
    result = score_drops(drops, judgments)
    result["min_agreement"] = min_agreement
    result["min_samples"] = min_samples
    result["pass"] = bool(
        result["used"] > 0
        and result["agree"] + result["disagree"] >= min_samples
        and result["agreement_rate"] >= min_agreement
    )
    print(json.dumps(result, ensure_ascii=False, indent=2))
    if report_path:
        path = _resolve(report_path)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            "\n".join(
                [
                    "# T2 胜率掉点盲测报告",
                    "",
                    "- 掉点候选：{}".format(result["drops"]),
                    "- 判定条数：{}（有效 {} / 无效 {}）".format(
                        result["judgments"], result["used"], result["invalid"]
                    ),
                    "- 认同：{} / 不认同：{}".format(
                        result["agree"], result["disagree"]
                    ),
                    "- 认同率：{:.1%}（门槛 {:.0%}）".format(
                        result["agreement_rate"], min_agreement
                    ),
                    "- 样本门槛：{}（有效判定 {}）".format(min_samples, result["used"]),
                    "- 结论：**{}**".format("通过" if result["pass"] else "未通过"),
                    "",
                    "判定口径：掉点 = 该 tick 前后模型胜率下降；人工判断「这个时刻是不是该改的决策点」。",
                    "",
                ]
            ),
            encoding="utf-8",
        )
        print("报告：{}".format(path))
    return 0 if result["pass"] else 2


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="TRAIN-PLAN v2 主线：胜率/价值模型训练（LightGBM）",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument("--config", default=str(DEFAULT_CONFIG), help="训练配置文件")
    parser.add_argument(
        "--data", action="append", default=[], help="回合级 samples.jsonl（可多次）"
    )
    parser.add_argument(
        "--snapshots", action="append", default=[], help="tick 快照 JSONL（可多次）"
    )
    parser.add_argument(
        "--match-json", action="append", default=[], help="match.json 或目录（可多次）"
    )
    parser.add_argument("--out-dir", default="", help="模型输出目录")
    parser.add_argument(
        "--mode", choices=("auto", "round", "tick"), default="", help="强制数据模式"
    )
    parser.add_argument(
        "--min-rounds", type=int, default=-1, help="T1 数据门槛（回合数）"
    )
    parser.add_argument(
        "--min-auc-delta", type=float, default=-1.0, help="T1 AUC 相对基线门槛"
    )
    parser.add_argument(
        "--valid-ratio", type=float, default=-1.0, help="按比赛切分的验证集比例"
    )
    parser.add_argument("--cv", type=int, default=-1, help="GroupKFold 折数")
    parser.add_argument("--seed", type=int, default=None)
    parser.add_argument(
        "--keep-draws",
        action="store_true",
        help="回合级 v0：保留 draw（按 0 处理；tick 模式仍在快照阶段跳过 draw）",
    )
    parser.add_argument(
        "--set", action="append", default=[], metavar="KEY=VALUE", help="覆盖配置项"
    )
    parser.add_argument("--check", action="store_true", help="只做数据审计，不训练")
    parser.add_argument(
        "--audit-out", default="", help="把数据审计写成 JSON（T0 微探针用）"
    )
    parser.add_argument("--dry-run", action="store_true", help="打印配置/审计，不训练")
    parser.add_argument("--strict", action="store_true", help="T1 验收不通过时退出 2")
    parser.add_argument("--predict", default="", help="单条 JSON 预测")
    parser.add_argument(
        "--drop-points", action="store_true", help="输出 T2 胜率掉点候选"
    )
    parser.add_argument("--top", type=int, default=20, help="--drop-points 输出条数")
    parser.add_argument("--drop-out", default="", help="--drop-points 输出 JSONL 路径")
    parser.add_argument(
        "--score-drops", default="", help="T2 盲测：--drop-points 产出的 JSONL"
    )
    parser.add_argument("--judgments", default="", help="T2 盲测：人工判定 JSONL")
    parser.add_argument(
        "--min-agreement", type=float, default=0.60, help="T2 认同率门槛"
    )
    parser.add_argument("--min-samples", type=int, default=20, help="T2 最少有效判定数")
    parser.add_argument("--drops-report", default="", help="T2 盲测报告输出路径")
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    cfg = load_config(Path(args.config).expanduser())

    for item in args.set:
        if "=" not in item:
            print("--set 需要 KEY=VALUE，收到 {!r}".format(item), file=sys.stderr)
            return 2
        key, value = item.split("=", 1)
        put(cfg, key.strip(), coerce(value))

    settings = settings_from_config(cfg)
    if args.data or args.snapshots or args.match_json:
        # CLI 指定数据源时覆盖配置里的默认源，避免 round/tick 混在一起
        settings.data = []
        settings.snapshots = []
        settings.match_json = []
        if args.data:
            settings.data = list(args.data)
        if args.snapshots:
            settings.snapshots = list(args.snapshots)
        if args.match_json:
            settings.match_json = list(args.match_json)
    if args.out_dir:
        settings.out_dir = _resolve(args.out_dir)
    if args.mode:
        settings.mode = args.mode
    if args.min_rounds >= 0:
        settings.min_rounds = args.min_rounds
    if args.min_auc_delta >= 0:
        settings.min_auc_delta = args.min_auc_delta
    if args.valid_ratio >= 0:
        settings.valid_ratio = args.valid_ratio
    if args.cv >= 0:
        settings.cv_folds = args.cv
    if args.seed is not None:
        settings.seed = args.seed
    if args.keep_draws:
        settings.drop_draws = False

    if args.predict:
        return run_predict(settings, args.predict)
    if args.drop_points:
        return run_drop_points(settings, args.top, args.drop_out)
    if args.score_drops:
        if not args.judgments:
            print(
                "--score-drops 需要同时给 --judgments <人工判定.jsonl>", file=sys.stderr
            )
            return 2
        return run_score_drops(
            args.score_drops,
            args.judgments,
            min_agreement=args.min_agreement,
            min_samples=args.min_samples,
            report_path=args.drops_report,
        )

    print("平台          : {} {}".format(platform.system(), platform.machine()))
    print("配置          : {}".format(Path(args.config)))
    print("模型输出      : {}".format(settings.out_dir))

    if not (args.check or args.dry_run):
        # 训练路径只让 train() 读一次数据，避免 match.json 被加载两遍
        return train(settings, strict=args.strict, audit_out=args.audit_out)

    try:
        rows, mode = load_rows(
            data=settings.data,
            snapshots=settings.snapshots,
            match_json=settings.match_json,
            mode=settings.mode,
        )
    except (OSError, ValueError) as exc:
        print("读数据失败：{}".format(exc), file=sys.stderr)
        print(
            "给 --data <samples.jsonl> 或 --match-json <match.json|目录>",
            file=sys.stderr,
        )
        return 2

    prepared = prepare_rows(rows, mode, drop_draws=settings.drop_draws)
    data_audit = audit(rows, mode, prepared)
    if args.audit_out:
        _write_audit(data_audit, args.audit_out)

    if args.check:
        print_audit(data_audit, settings)
        ok = bool(prepared["labels"]) and len(set(prepared["labels"])) > 1
        if not ok:
            print("\n✗ 数据不足或标签只有一类", file=sys.stderr)
        else:
            print("\n✓ 数据可训练")
        return 0 if ok else 2

    print_audit(data_audit, settings)
    train_idx, valid_idx = split_by_match(
        prepared["groups"], settings.valid_ratio, settings.seed
    )
    print(
        "\n计划切分      : train {} 行 / valid {} 行（按 match）".format(
            len(train_idx), len(valid_idx)
        )
    )
    print("特征列        : {}".format(", ".join(feature_names(mode))))
    print("\n（dry-run：没有训练）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
