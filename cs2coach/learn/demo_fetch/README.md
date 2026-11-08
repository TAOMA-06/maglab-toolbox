# demo_fetch — 拉取 CS2 demo

一个零第三方依赖（PWA 除外）的 demo 采集器，给 `cs2coach` 复盘和 `learn/` 胜率模型攒数据。

拉取范围：

| 来源 | 内容 | 国内外 | 说明 |
| --- | --- | --- | --- |
| `hltv` | 职业比赛 GOTV demo | 国际 | 比赛页找 `/download/demo/<id>`；HLTV 有 Cloudflare，下载通常需要代理或浏览器 Cookie |
| `5e` | 5EPlay 天梯 demo | 国内 | 公开 Web API；demo 约保存 15 天 |
| `renown` | Renown 天梯 demo | 国际 | 开放 JSON API，demo 直链 `.dem.gz`；支持排行榜前 N 名 |
| `pwa` | 完美世界 PWA 天梯 demo | 国内 | 可选依赖 `cs-demo-downloader`（含 PWA 签名器），需要 access token |
| `steam` | Steam 官匹 demo | 国际/国内 | share code + 外部 Game Coordinator resolver（自带解析器不在本工具内） |
| `url` | 任意直链 | 任意 | `.dem` / `.dem.gz` / `.zip` / `.rar` / `.7z` / `.tar(.gz/.xz/.bz2/.zst)` |
| `import` | 本地已下载文件 | 任意 | 自动解压、去重、写入 manifest |

## 快速开始

```bash
cd ~/code/cs2-demo-review

# 两种入口等价：显式 python -m，或仓库里的 wrapper
./scripts/fetch-demos.sh --help
PYTHON=learn/.venv/bin/python ./scripts/fetch-demos.sh --help

learn/.venv/bin/python -m learn.demo_fetch --help

# 先看计划，不下载
learn/.venv/bin/python -m learn.demo_fetch renown --leaderboard 10 --limit 5 --dry-run

# 国际天梯：Renown 排行榜前 10，每人最近 5 场
learn/.venv/bin/python -m learn.demo_fetch renown --leaderboard 10 --limit 5

# 国内天梯：5E 用户（个人主页 URL 最后一段）
learn/.venv/bin/python -m learn.demo_fetch 5e --user 11814738gjdwn7 --days 14 --limit 20

# 职业比赛：HLTV 最近结果里取 10 场有 demo 的
learn/.venv/bin/python -m learn.demo_fetch hltv --limit 10 --since 2026-10-01

# 手动直链
learn/.venv/bin/python -m learn.demo_fetch url --file urls.txt

# 导入本地下载目录
learn/.venv/bin/python -m learn.demo_fetch import ~/Downloads --out learn/data/imported
```

默认输出目录（可用 `--out` 覆盖）：

```
learn/data/hltv_demos/
learn/data/5e_demos/
learn/data/renown_demos/
learn/data/pwa_demos/
learn/data/steam_demos/
learn/data/url_demos/
learn/data/imported/
```

每个目录里有：

* `manifest.jsonl`：成功记录（source / match_id / map / date / path / sha256 / url / meta）；
* `failed.jsonl`：失败记录（含错误原因，方便重试）；
* `*.dem`：解压后的 demo。

重复运行是幂等的：同一 `source + match_id + demo_id` 已记录就跳过；不同文件名但内容相同的 demo 会按 SHA-256 去重。`--force` 可强制重下/覆盖。

## 各来源细节

### HLTV（职业比赛）

HLTV 页面和 `r2-demos.hltv.org` 都有 Cloudflare。工具会：

1. 直连 HLTV；
2. 直连失败时用 `r.jina.ai` 读取页面（只读 HTML/markdown，用于找 demo id）；
3. 下载时用 `curl`；如果 CDN 403，需要你提供代理或登录后的 Cookie。

```bash
# 常用：最近 5 星比赛，最近 7 天
learn/.venv/bin/python -m learn.demo_fetch hltv --limit 10 --stars 5 --since 2026-10-25

# 指定比赛 / 事件 / demo id
learn/.venv/bin/python -m learn.demo_fetch hltv \
  --match-url https://www.hltv.org/matches/2399724/legacy-vs-nemiga-pgl-masters-bucharest-2026
learn/.venv/bin/python -m learn.demo_fetch hltv \
  --event-url https://www.hltv.org/events/8261/esports-world-cup-2026
learn/.venv/bin/python -m learn.demo_fetch hltv --demo-id 113158

# 下载 403 时：代理 / 浏览器 Cookie
learn/.venv/bin/python -m learn.demo_fetch hltv --limit 5 --proxy http://127.0.0.1:7890
learn/.venv/bin/python -m learn.demo_fetch hltv --limit 5 \
  --cookie 'cf_clearance=...; __cf_bm=...; autologin=...'
learn/.venv/bin/python -m learn.demo_fetch hltv --limit 5 --cookie-file ~/hltv-cookies.txt
```

说明：

* 只读页面时 `r.jina.ai` 可能被限流；失败会重试。仍然失败时请用 `--proxy`。
* HLTV 的 demo 下载链接可能要求登录；在浏览器登录后导出 Cookie 即可。
* HLTV 的 `.rar` 可能包含多张地图，工具会把每个 `.dem` 都解压出来。
* 如果下载失败，`failed.jsonl` 会记录原始 URL 和错误；可稍后重试。

### 5EPlay（国内天梯）

```bash
learn/.venv/bin/python -m learn.demo_fetch 5e \
  --user 11814738gjdwn7 \
  --user https://arena.5eplay.com/data/player/11814738gjdwn7 \
  --days 14 --limit 20 --map mirage
```

* `--user` 接受 5E 个人主页 URL 或最后一段 id。
* 5E demo 约保留 15 天；超过后 API 返回空 `demo_url`，工具会跳过并提示。
* 5E 接口偶尔变更；如果 `demo_url` 全为空，先用 `--days 1` 测试最近一场，或在 5E 网页确认该场仍可下载。

### Renown（国际天梯）

Renown 的 API 是开放的，demo 是 `.dem.gz` 直链。

```bash
# 指定玩家
learn/.venv/bin/python -m learn.demo_fetch renown \
  --steamid 76561198399730278 --limit 10 --since 2026-10-01

# 排行榜前 10 名，每人 5 场（适合快速攒高质量天梯样本）
learn/.venv/bin/python -m learn.demo_fetch renown --leaderboard 10 --limit 5
```

SteamID64 可以从 Steam 个人资料 URL 或 `steamid.io` 获取。

### PWA（完美世界，可选）

PWA 的 demo 链接需要 native 签名器。直接复用 [`cs-demo-downloader`](https://github.com/WangChuDi/CS-Demo-Downloader)：

```bash
# 注意：签名器 wheel 覆盖 Python 3.9-3.12；learn/.venv 是 3.12
uv pip install --python learn/.venv/bin/python cs-demo-downloader
```

获取 token（详见 cs-demo-downloader 文档）：

1. 打开 `https://partner.wmpvp.com/#/login` 并登录；
2. 手机号登录：从浏览器 Cookie 读 `access_token`；
3. Steam 登录：从地址栏 `...?token=...` 复制 token。

```bash
export PWA_ACCESS_TOKEN='...'
learn/.venv/bin/python -m learn.demo_fetch pwa \
  --steamid 76561198159976336 --limit 20
```

token 会过期，失效后重新获取。

### 未直接支持的平台

* **FACEIT**：demo 下载已收紧到需要单独申请的私有 Downloads API，普通 Data API
  key 只能拿到 `demo_url` 资源地址，不能直接换签名下载。需要的话在浏览器下载后用
  `import` 导入，或自行接入有权限的 Downloads API。
* **Esportal / FASTCUP**：网页有 “Download demo”，但没有稳定的公开批量 API；
  可把直链贴进 `urls.txt`，或下载后用 `import` 导入。
* **B5 / 其他国内平台**：同理，优先用 `url` / `import`，避免为每个平台维护脆弱爬虫。

### Steam 官匹（share code）

Valve 只通过 Game Coordinator 给出真实 replay URL；本工具不内置 GC 客户端，而是接受一个 resolver 命令：

```bash
learn/.venv/bin/python -m learn.demo_fetch steam \
  --share-code CSGO-xxxxx-xxxxx-xxxxx-xxxxx-xxxxx \
  --resolver-cmd '/path/to/resolver {code}'
```

resolver 只要把 demo URL 打印到 stdout 即可（工具会从输出里提取
`http(s)://...dem[.bz2|.gz|.zst]`）。可包装 `cs2-demo-fetcher`、`cs-demo-manager`
或自己的 GC 脚本。

## 手动 URL / 本地导入

```bash
# urls.txt 一行一个，空行和 # 注释会被忽略
learn/.venv/bin/python -m learn.demo_fetch url --file urls.txt --out learn/data/url_demos

# 导入目录（递归）
learn/.venv/bin/python -m learn.demo_fetch import ~/Downloads \
  --out learn/data/imported

# 只看清单
learn/.venv/bin/python -m learn.demo_fetch manifest --out learn/data/renown_demos
```

支持 `.dem`、`.dem.gz`、`.dem.bz2`、`.dem.zst`、`.zip`、`.rar`、`.7z`、
`.tar`、`.tar.gz`、`.tar.xz`、`.tar.bz2`、`.tar.zst`、`.tgz`。
`.rar`/`.7z` 需要 `7z` 或 `bsdtar`（macOS：`brew install sevenzip`）；
`.tar.zst` 需要 `zstd`（macOS：`brew install zstd`）。

## 训练数据衔接

拉完 demo 后，直接跑现有的解析/导出：

```bash
for d in learn/data/renown_demos/*.dem; do
  ./cs2coach run "$d" --out-dir "out/$(basename "$d" .dem)" --player <名字>
done

# 或者导出回合级样本
./cs2coach export learn/data/renown_demos -o out/samples.jsonl --recursive
```

`learn/train_winrate.py --match-json out/` 会按 `source_file` 分组切分，避免同一场比赛泄漏到验证集。

## 合规与礼貌

* 只下载你有权使用/分析的 demo；遵守各平台服务条款。
* 默认对 HLTV 每次下载间隔 5 秒，其他来源 1 秒；不要并发轰炸。
* HLTV / 5E / PWA 的 demo 可能有时效（约 15 天到 1 个月），有价值的比赛请及时拉取。
* 不要提交 Cookie、token、代理密码到仓库；`learn/data/` 已被 `.gitignore` 忽略。

## 故障排查

| 现象 | 处理 |
| --- | --- |
| HLTV 403 / `Attention Required` | 用 `--proxy`，或浏览器登录后 `--cookie` / `--cookie-file` |
| `r.jina.ai` SSL/超时 | 网络问题或 Jina 限流；换代理，或直接给 `--match-url` / `--demo-id` |
| 5E 全部 `demo_url` 为空 | demo 过期（>15 天）或 5E 接口变更；先用 `--days 1` 验证 |
| PWA 报缺少 `cs_demo_pwa_signer` | 用 Python 3.9-3.12 安装 `cs-demo-downloader` |
| 解压 `.rar` 失败 | `brew install sevenzip`，或安装 `bsdtar` |
| 重复下载 | 默认已幂等；检查 `manifest.jsonl`，需要重下加 `--force` |
