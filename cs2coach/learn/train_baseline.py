#!/usr/bin/env python3
"""train_baseline.py — Train LightGBM pro-player baseline model.

Usage:
    python train_baseline.py --data samples.jsonl [--out learn/models] [--cv 5]
"""

import argparse
import json
import sys
from pathlib import Path

import lightgbm as lgb
import numpy as np
from sklearn.metrics import accuracy_score, classification_report, roc_auc_score
from sklearn.model_selection import StratifiedKFold
from sklearn.preprocessing import StandardScaler

from features import load_features_for_baseline


def train(data_path: str, out_dir: str, cv_folds: int = 5) -> dict:
    out_path = Path(out_dir)
    out_path.mkdir(parents=True, exist_ok=True)

    X, y, feature_names, cat_encoders, cat_indices = load_features_for_baseline(data_path)
    n = len(y)
    print(f"Loaded {n} samples, {X.shape[1]} features ({len(cat_indices)} categorical)")

    scaler = StandardScaler()
    X_scaled = scaler.fit_transform(X)

    params = {
        "objective": "binary",
        "metric": "auc",
        "verbosity": -1,
        "n_estimators": 200,
        "learning_rate": 0.05,
        "max_depth": 5,
        "num_leaves": 31,
        "min_child_samples": 5,
        "subsample": 0.8,
        "colsample_bytree": 0.8,
        "reg_alpha": 0.1,
        "reg_lambda": 0.1,
        "random_state": 42,
        "n_jobs": -1,
    }

    skf = StratifiedKFold(n_splits=cv_folds, shuffle=True, random_state=42)
    cv_aucs, cv_accs = [], []

    for fold, (train_idx, val_idx) in enumerate(skf.split(X_scaled, y), 1):
        X_tr, X_val = X_scaled[train_idx], X_scaled[val_idx]
        y_tr, y_val = y[train_idx], y[val_idx]

        model = lgb.LGBMClassifier(**params)
        model.fit(X_tr, y_tr, eval_set=[(X_val, y_val)],
                  callbacks=[lgb.log_evaluation(period=0)],
                  categorical_feature=cat_indices)

        y_proba = model.predict_proba(X_val)[:, 1]
        y_pred = (y_proba >= 0.5).astype(int)

        fold_auc = roc_auc_score(y_val, y_proba)
        fold_acc = accuracy_score(y_val, y_pred)
        cv_aucs.append(fold_auc)
        cv_accs.append(fold_acc)
        print(f"  Fold {fold}: AUC={fold_auc:.4f}  Acc={fold_acc:.4f}")

    mean_auc = float(np.mean(cv_aucs))
    mean_acc = float(np.mean(cv_accs))
    print(f"  Mean CV AUC: {mean_auc:.4f}  Mean CV Acc: {mean_acc:.4f}")

    # Save model + metadata
    model_path = out_path / "baseline.txt"
    final_model = lgb.LGBMClassifier(**params)
    final_model.fit(X_scaled, y, categorical_feature=cat_indices)
    final_model.booster_.save_model(str(model_path))
    print(f"Model saved to {model_path}")

    # Compute feature importance (LightGBM tree splits)
    importances = final_model.feature_importances_
    importance_pairs = sorted(
        zip(feature_names, importances.tolist()), key=lambda x: x[1], reverse=True
    )

    meta = {
        "n_samples": n,
        "n_features": X.shape[1],
        "feature_names": feature_names,
        "cat_indices": cat_indices,
        "cat_encoders": cat_encoders,
        "cv_folds": cv_folds,
        "cv_auc_mean": round(mean_auc, 4),
        "cv_auc_std": round(float(np.std(cv_aucs)), 4),
        "cv_acc_mean": round(mean_acc, 4),
        "class_distribution": {"focus_won_0": int(np.sum(y == 0)), "focus_won_1": int(np.sum(y == 1))},
        "top_features": [{"name": nm, "importance": round(float(imp), 4)} for nm, imp in importance_pairs[:10]],
    }
    meta_path = out_path / "baseline_meta.json"
    with open(meta_path, "w") as f:
        json.dump(meta, f, indent=2, ensure_ascii=False)
    print(f"Metadata saved to {meta_path}")

    print("\nTop 10 features:")
    print(f"{'Feature':<30} {'Importance':>10}")
    print("-" * 42)
    for nm, imp in importance_pairs[:10]:
        print(f"{nm:<30} {imp:>10}")

    y_full_pred = (final_model.predict_proba(X_scaled)[:, 1] >= 0.5).astype(int)
    print(f"\nFull-data accuracy: {accuracy_score(y, y_full_pred):.4f}")
    print(classification_report(y, y_full_pred, zero_division=0))

    return meta


def main():
    parser = argparse.ArgumentParser(description="Train pro-player baseline")
    parser.add_argument("--data", required=True, help="Path to samples.jsonl")
    parser.add_argument("--out", default="learn/models", help="Output directory")
    parser.add_argument("--cv", type=int, default=5, help="CV folds")
    args = parser.parse_args()

    meta = train(args.data, args.out, args.cv)
    print(f"\nBaseline trained: {meta['n_samples']} samples, AUC={meta['cv_auc_mean']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
