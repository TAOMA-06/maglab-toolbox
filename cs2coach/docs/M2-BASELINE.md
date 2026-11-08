# M2b 指标分布基线（2026-11-01）

> 目的：给「预瞄角度差 / 开火速度」阈值提供分布依据，避免拍脑袋。
> **这不是用户自己的 3 场 demo**——仓库里只有公开回归 demo。用户拿自己的 3 场 POV demo
> 重跑 `cs2coach metrics` 后，应覆盖本文件并重新校准常量（只改 `internal/tactics/m2.go`）。

## 复现方式

```bash
go build -o cs2coach ./cmd/cs2coach
./cs2coach run testdata/demos/s2/pov.dem --out-dir out/pov
./cs2coach run testdata/demos/s2/arsham_infernocs2_1stSept23.dem --out-dir out/arsham --player f1aizy
./cs2coach run testdata/demos/s2/s2.dem --out-dir out/s2 --player Appl3Thr33
./cs2coach metrics out/pov/analysis.json out/arsham/analysis.json out/s2/analysis.json
```

## 基线结果

| demo | 类型 | 对枪 | 可评估 | 预瞄 P50 | P75 | P90 | 速度 P50 | P75 | P90 | 预瞄 >15° | 移动中开枪 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| pov.dem | POV | 23 | 22 | 1.7° | 8.5° | 103.1° | 14.6 | 35.6 | 81.6 | 3 | 2 |
| arsham_infernocs2 | POV | 46 | 46 | 0.7° | 2.1° | 4.1° | 0.0 | 0.0 | 29.3 | 0 | 1 |
| s2.dem | GOTV | 20 | 17 | 1.5° | 3.4° | 27.7° | 0.0 | 0.0 | 25.5 | 2 | 0 |

合计（对枪快照口径）：预瞄 P50 1.1° / P75 3.2° / P90 8.5°（85 样本）；
开火速度 P50 0.0 / P75 21.3 / P90 64.5 u/s（67 样本）。

> 速度分布只统计枪械开火（手枪/SMG/重武器/步枪）。刀挥与投掷物出手也会触发
> `WeaponFire`，已排除，否则 P90 会被抬到 ~170 u/s，阈值必然失真。

## 阈值结论

| 常量 | 取值 | 依据 |
| --- | --- | --- |
| `PreAimBadDeg` | **15°** | 合计 P90 仅 8.5°；15° 只命中约 5%，是「保守、只抓明显没瞄」档位，避免把正常拉枪误判。 |
| `MovingFireSpeedUPS` | **100 u/s** | 枪械射击 P75≈36 / P90≈82；100 是保守档位（各 demo <10% 命中），只抓明显跑动开枪。 |

两项都集中定义、可调；报告会原样写出阈值，误判时只改常量不动算法。

## 数据质量结论（影响 M2a 验收口径）

1. **GOTV demo 可能没有 `PlayerHurt` 游戏事件。** `testdata/demos/s2/s2.dem` 只含 7 类游戏事件
   （`player_death` / `round_*` / `player_connect*`），没有 `player_hurt` / `weapon_fire` /
   道具事件。因此：
   - 对枪只能由击杀事件降级创建（`DuelFact.Contact="kill"`），预瞄快照是击杀瞬间而不是接敌瞬间；
   - 命中数只能从 `PlayerHurt` 聚合，GOTV 下为 0（不伪造）。
   - **POV demo（`pov.dem`）有 436 次 PlayerHurt，接触快照完整。**
   → 与 PLAN 风险项一致：**CP-B 优先用 POV 自存 demo 验收；GOTV 结果必须标注降级。**
2. **速度零值不是缺失。** 位置差分有效率为 100%（pov 5919/5919，arsham 2125/2125，s2 2758/2758，
   均已排除刀/投掷物）；zero 占比 24% / 81% / 80%，对应「急停后开枪」的正常行为，不是采样失败。
   速度口径：最近 4 个不同 tick 的位置环，取「当前 tick − 2」做水平差分（64 tick ≈ 31ms），
   避开 POV 同 tick 多帧导致的 dt=0。
3. **预瞄偏差 >90° 的样本是「被侧身/背身」，不是拉枪失败。** 报告保留数字，但用
   「对手先手命中 / 焦点未开火」区分角色；不要把所有大角度都当同一类问题解读。

## 仍待人工完成

- M2a：用 3 场 demo 人工抽查 5 次对枪，与回放核对（视角/速度/接敌）。
- CP-A / CP-B：用户判定「至少 1 条下把能用」。
- 用户自己的 3 场 demo 覆盖本基线后，重新确认 15° / 100 u/s。
