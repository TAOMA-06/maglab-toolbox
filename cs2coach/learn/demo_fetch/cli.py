"""Command line interface for demo_fetch."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Optional

from . import __version__
from .errors import SourceError
from .manifest import Manifest
from .pipeline import Job, local_jobs, run_jobs, safe_job_url
from .sources import fivee, hltv, pwa, renown, steam, urls
from .util import ensure_dir, read_netscape_cookies

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUT = {
    "hltv": REPO_ROOT / "learn" / "data" / "hltv_demos",
    "5e": REPO_ROOT / "learn" / "data" / "5e_demos",
    "renown": REPO_ROOT / "learn" / "data" / "renown_demos",
    "pwa": REPO_ROOT / "learn" / "data" / "pwa_demos",
    "steam": REPO_ROOT / "learn" / "data" / "steam_demos",
    "url": REPO_ROOT / "learn" / "data" / "url_demos",
    "import": REPO_ROOT / "learn" / "data" / "imported",
}


def _add_common(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--out", help="输出目录（默认按 source 放到 learn/data/<source>_demos）"
    )
    parser.add_argument(
        "--limit", type=int, default=None, help="最多处理多少场/多少个 demo"
    )
    parser.add_argument("--dry-run", action="store_true", help="只列出计划，不下载")
    parser.add_argument("--force", action="store_true", help="覆盖已存在/已记录的文件")
    parser.add_argument("--keep-archive", action="store_true", help="保留下载的压缩包")
    parser.add_argument("--proxy", help="HTTP(S) 代理，例如 http://127.0.0.1:7890")
    parser.add_argument("--cookie", help="Cookie 字符串（HLTV 登录态 / 5E 需要时）")
    parser.add_argument("--cookie-file", help="Netscape 格式 Cookie 文件（curl -b）")
    parser.add_argument(
        "--timeout", type=float, default=180.0, help="单文件下载超时秒数（默认 180）"
    )
    parser.add_argument(
        "--api-timeout", type=float, default=30.0, help="API 请求超时秒数（默认 30）"
    )
    parser.add_argument(
        "--delay",
        type=float,
        default=None,
        help="请求间隔秒数（HLTV 默认 5，其他默认 1）",
    )
    parser.add_argument("--fail-fast", action="store_true", help="遇到第一个错误就停止")
    parser.add_argument("--quiet", action="store_true", help="减少输出")
    parser.add_argument("--json", action="store_true", help="只输出 JSON 摘要")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python3 -m learn.demo_fetch",
        description="拉取 CS2 demo：HLTV 职业比赛、5E / Renown / PWA 天梯、手动 URL。",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "示例:\n"
            "  python3 -m learn.demo_fetch hltv --limit 10 --since 2026-10-01\n"
            "  python3 -m learn.demo_fetch 5e --user 11814738gjdwn7 --days 14 --limit 20\n"
            "  python3 -m learn.demo_fetch renown --leaderboard 10 --limit 5\n"
            "  python3 -m learn.demo_fetch url --file urls.txt\n"
            "  python3 -m learn.demo_fetch import ~/Downloads --out learn/data/imported\n"
        ),
    )
    parser.add_argument(
        "--version", action="version", version=f"demo_fetch {__version__}"
    )
    sub = parser.add_subparsers(dest="command", required=True)

    common = argparse.ArgumentParser(add_help=False)
    _add_common(common)

    hltv_parser = sub.add_parser(
        "hltv",
        parents=[common],
        help="HLTV 职业比赛 demo（需要代理/Cookie 才能下载）",
    )
    hltv_parser.add_argument(
        "--match-url", action="append", default=[], help="HLTV 比赛页 URL（可重复）"
    )
    hltv_parser.add_argument(
        "--event-url", action="append", default=[], help="HLTV 事件页 URL（可重复）"
    )
    hltv_parser.add_argument(
        "--demo-id", action="append", default=[], help="HLTV demo ID（可重复）"
    )
    hltv_parser.add_argument(
        "--pages", type=int, default=1, help="最近结果页数（默认 1，每页约 100 场）"
    )
    hltv_parser.add_argument(
        "--stars", type=int, default=None, help="只要 N 星及以上比赛，如 5"
    )
    hltv_parser.add_argument("--since", help="最早比赛日期，如 2026-10-01")
    hltv_parser.add_argument(
        "--no-jina", action="store_true", help="不通过 r.jina.ai 兜底抓页面"
    )

    five_parser = sub.add_parser(
        "5e",
        aliases=["5eplay", "fivee"],
        parents=[common],
        help="5EPlay 天梯 demo（国内，demo 约保存 15 天）",
    )
    five_parser.add_argument(
        "--user",
        action="append",
        default=[],
        help="5E 用户 ID 或个人主页 URL（可重复）",
    )
    five_parser.add_argument(
        "--match-id", action="append", default=[], help="5E 比赛 ID（可重复）"
    )
    five_parser.add_argument("--days", type=int, default=14, help="回溯天数（默认 14）")
    five_parser.add_argument(
        "--map", action="append", default=[], help="只要指定地图，如 mirage（可重复）"
    )

    renown_parser = sub.add_parser(
        "renown",
        parents=[common],
        help="Renown 天梯 demo（国际，开放 API）",
    )
    renown_parser.add_argument(
        "--steamid", action="append", default=[], help="SteamID64（可重复）"
    )
    renown_parser.add_argument(
        "--leaderboard", type=int, default=0, help="拉取排行榜前 N 名玩家"
    )
    renown_parser.add_argument(
        "--map", action="append", default=[], help="只要指定地图，如 mirage（可重复）"
    )
    renown_parser.add_argument("--since", help="最早结束日期，如 2026-10-01")

    pwa_parser = sub.add_parser(
        "pwa",
        parents=[common],
        help="完美世界 PWA 天梯 demo（需要 cs-demo-downloader + token）",
    )
    pwa_parser.add_argument(
        "--steamid", action="append", default=[], help="目标玩家 SteamID64（可重复）"
    )
    pwa_parser.add_argument(
        "--token", help="PWA access token（也可用 PWA_ACCESS_TOKEN）"
    )

    steam_parser = sub.add_parser(
        "steam",
        parents=[common],
        help="Steam 官匹 demo（share code + 外部 GC resolver）",
    )
    steam_parser.add_argument(
        "--share-code",
        action="append",
        default=[],
        help="CSGO-xxxxx-... 分享码（可重复）",
    )
    steam_parser.add_argument(
        "--resolver-cmd", help="接收 {code} 并打印 demo URL 的命令"
    )

    url_parser = sub.add_parser(
        "url", parents=[common], help="手动 URL 列表（直链 .dem/.zip/.rar/.gz）"
    )
    url_parser.add_argument("urls", nargs="*", help="一个或多个直链 URL")
    url_parser.add_argument("--file", help="URL 列表文件，一行一个，忽略空行和 # 注释")
    url_parser.add_argument(
        "--prefix", default="url", help="输出文件名前缀（默认 url）"
    )

    import_parser = sub.add_parser(
        "import", parents=[common], help="导入本地 demo/压缩包"
    )
    import_parser.add_argument("paths", nargs="+", help="本地文件或目录")
    import_parser.add_argument("--no-recursive", action="store_true", help="目录不递归")

    manifest_parser = sub.add_parser(
        "manifest", parents=[common], help="查看输出目录的拉取清单"
    )
    manifest_parser.add_argument(
        "--recent", type=int, default=20, help="显示最近 N 条记录"
    )

    return parser


def _job_dict(job: Job) -> dict[str, Any]:
    return {
        "source": job.source,
        "match_id": job.match_id,
        "demo_id": job.demo_id,
        "map": job.map_name,
        "date": job.date,
        "url": safe_job_url(job),
        "local_path": job.local_path,
    }


def _source_jobs(args: argparse.Namespace) -> list[Job]:
    command = args.command
    limit = args.limit
    if command == "hltv":
        return hltv.jobs(
            match_urls=args.match_url,
            event_urls=args.event_url,
            demo_ids=args.demo_id,
            limit=limit or 10,
            pages=args.pages,
            stars=args.stars,
            since=args.since,
            use_jina=not args.no_jina,
            cookie=args.cookie,
            cookie_file=args.cookie_file,
            proxy=args.proxy,
            timeout=args.api_timeout,
            quiet=args.quiet or args.json,
        )
    if command in ("5e", "5eplay", "fivee"):
        if not args.user and not args.match_id:
            raise SourceError("5E 需要 --user 或 --match-id")
        return fivee.jobs(
            users=args.user,
            match_ids=args.match_id,
            days=args.days,
            limit=limit or 20,
            maps=args.map,
            timeout=args.api_timeout,
            proxy=args.proxy,
            delay=1.0 if args.delay is None else args.delay,
            quiet=args.quiet or args.json,
        )
    if command == "renown":
        if not args.steamid and not args.leaderboard:
            raise SourceError("Renown 需要 --steamid 或 --leaderboard N")
        jobs = renown.jobs(
            steamids=args.steamid,
            leaderboard_count=args.leaderboard,
            limit=limit or 10,
            maps=args.map,
            since=args.since,
            timeout=args.api_timeout,
            proxy=args.proxy,
            delay=1.0 if args.delay is None else args.delay,
            quiet=args.quiet or args.json,
        )
        return jobs
    if command == "pwa":
        return pwa.jobs(
            steamids=args.steamid,
            token=args.token,
            limit=limit or 20,
            quiet=args.quiet or args.json,
        )
    if command == "steam":
        return steam.jobs(
            share_codes=args.share_code,
            resolver_cmd=args.resolver_cmd,
            limit=limit,
            timeout=args.api_timeout,
        )
    if command == "url":
        return urls.jobs(urls=args.urls, url_file=args.file, name_prefix=args.prefix)
    if command == "import":
        return local_jobs(args.paths, recursive=not args.no_recursive)
    raise SourceError(f"未知命令: {command}")


def main(argv: Optional[list[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)

    # argparse keeps the alias that the user typed; normalise before using it as
    # a dictionary key / source name.
    aliases = {"5eplay": "5e", "fivee": "5e"}
    args.command = aliases.get(args.command, args.command)

    # Page requests need the cookie as a header; downloads can use curl -b.
    # Loading the Netscape file here keeps --cookie-file useful for both.
    if getattr(args, "cookie_file", None) and not getattr(args, "cookie", None):
        try:
            args.cookie = read_netscape_cookies(args.cookie_file)
        except FileNotFoundError as error:
            print(f"错误: {error}", file=sys.stderr)
            return 2

    if args.command == "manifest":
        if not args.out:
            parser.error("manifest 需要 --out")
        manifest = Manifest(args.out)
        summary = manifest.summary()
        if args.json:
            print(json.dumps(summary, ensure_ascii=False, indent=2))
        else:
            print(f"目录: {summary['manifest']}")
            print(f"demo: {summary['demos']} 个，合计 {summary['bytes']} 字节")
            print(f"按来源: {summary['by_source']}")
            if summary["by_map"]:
                print(f"按地图: {summary['by_map']}")
            recent = manifest.recent(args.recent)
            if recent:
                print("\n最近记录:")
                for record in recent:
                    print(
                        f"  {record.get('date') or '?'}  {record.get('source')}  "
                        f"{record.get('map') or '?'}  {record.get('path')}"
                    )
        return 0

    out_dir = Path(args.out).expanduser() if args.out else DEFAULT_OUT[args.command]
    quiet = bool(args.quiet or args.json)

    try:
        jobs = _source_jobs(args)
    except (SourceError, ValueError, FileNotFoundError) as error:
        print(f"错误: {error}", file=sys.stderr)
        return 2

    if args.command in ("url", "import") and args.limit:
        jobs = jobs[: args.limit]

    if not jobs:
        if args.json:
            print(
                json.dumps(
                    {
                        "planned": 0,
                        "downloaded": 0,
                        "skipped": 0,
                        "failed": 0,
                        "demos": 0,
                        "message": "没有可下载的 demo",
                    },
                    ensure_ascii=False,
                )
            )
        else:
            print(
                "没有可下载的 demo（可能都已过期、没有 demo，或过滤条件太严）。",
                file=sys.stderr,
            )
        return 0

    if args.dry_run:
        if args.json:
            print(
                json.dumps(
                    {"planned": len(jobs), "jobs": [_job_dict(job) for job in jobs]},
                    ensure_ascii=False,
                    indent=2,
                )
            )
        else:
            print(f"计划下载 {len(jobs)} 个 demo -> {out_dir}")
            for index, job in enumerate(jobs, start=1):
                print(
                    f"  {index:3d}. {job.source:6s} {job.map_name or '?':12s} "
                    f"{job.date or '?':10s} {safe_job_url(job) if job.url else job.local_path}"
                )
        return 0

    out_dir = ensure_dir(out_dir)
    manifest = Manifest(out_dir)

    delay = args.delay
    if delay is None:
        delay = 5.0 if args.command == "hltv" else 1.0

    try:
        summary = run_jobs(
            jobs,
            out_dir=out_dir,
            manifest=manifest,
            force=args.force,
            keep_archive=args.keep_archive,
            cookie=args.cookie,
            cookie_file=args.cookie_file,
            proxy=args.proxy,
            timeout=args.timeout,
            delay=delay,
            fail_fast=args.fail_fast,
            quiet=quiet,
        )
    except KeyboardInterrupt:
        print("\n已中断", file=sys.stderr)
        return 130
    except Exception as error:  # --fail-fast re-raises the first job failure
        print(f"错误: {error}", file=sys.stderr)
        return 1

    if args.json:
        print(json.dumps(summary.to_dict(), ensure_ascii=False, indent=2))
    else:
        print(
            f"\n完成: 新增 {summary.downloaded}，跳过 {summary.skipped}，失败 {summary.failed}，"
            f"合计 {summary.bytes} 字节"
        )
        print(f"清单: {manifest.path}")
    return 0 if summary.failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
