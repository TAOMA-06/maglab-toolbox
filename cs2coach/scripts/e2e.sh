#!/usr/bin/env bash
# e2e 冒烟：对 testdata/demos 里的公开 demo 跑通 parse → analyze → report → ask。
# 用法：./scripts/e2e.sh [demo路径] [焦点玩家名]
# 缺省用 s2/pov.dem（POV 录制，无需 --player）；传第二个参数则透传 --player。
set -euo pipefail
cd "$(dirname "$0")/.."

DEMO="${1:-testdata/demos/s2/pov.dem}"
PLAYER="${2:-}"
OUT="testdata/e2e-out"
BIN="./cs2coach"

if [[ ! -x "$BIN" ]]; then
  echo "构建 cs2coach …"
  go build -o cs2coach ./cmd/cs2coach
fi

if [[ ! -f "$DEMO" ]]; then
  echo "找不到 demo：$DEMO" >&2
  exit 1
fi

echo "=== parse: $DEMO ${PLAYER:+(焦点 $PLAYER)} ==="
if [[ -n "$PLAYER" ]]; then
  PARSE_OK=1
  $BIN parse "$DEMO" -o "$OUT/match.json" --player "$PLAYER" || PARSE_OK=0
else
  PARSE_OK=1
  $BIN parse "$DEMO" -o "$OUT/match.json" || PARSE_OK=0
fi
if [[ "$PARSE_OK" != "1" ]]; then
  echo "提示：非 POV demo 需要 --player。先不带 player 跑 parse 看名单，或直接传：./scripts/e2e.sh $DEMO <名字|SteamID64>" >&2
  exit 1
fi
echo
echo "=== players ==="
$BIN players "$OUT/match.json" || true
echo
echo "=== analyze ==="
$BIN analyze "$OUT/match.json" -o "$OUT/analysis.json"
echo
echo "=== report (纯事实版) ==="
$BIN report "$OUT/analysis.json" --out-dir "$OUT"
echo
echo "=== prompt-only + --interpret 回填链 ==="
$BIN report "$OUT/analysis.json" --out-dir "$OUT" --prompt-only
cp "$OUT/prompt.txt" "$OUT/fake_interpret.txt"
$BIN report "$OUT/analysis.json" --out-dir "$OUT" --interpret "$OUT/fake_interpret.txt" || true
echo
echo "=== ask (纯事实回答) ==="
$BIN ask "$OUT/analysis.json" "残局问题" || true
echo
echo "=== 非 POV demo（需 --player）==="
if [[ -z "$PLAYER" ]]; then
  $BIN parse testdata/demos/s2/s2.dem -o "$OUT/match2.json" --player Appl3Thr33
  $BIN analyze "$OUT/match2.json" -o "$OUT/analysis2.json"
  $BIN report "$OUT/analysis2.json" --out-dir "$OUT/report2"
fi
echo
echo "=== 产物 ==="
ls -la "$OUT"
echo "e2e 完成：$OUT/report.md"
