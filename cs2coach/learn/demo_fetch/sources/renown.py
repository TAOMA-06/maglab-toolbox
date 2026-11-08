"""Renown source (international ladder / 天梯).

Renown exposes an open JSON API and demo files at ``demos.renown.gg``.  It is a
good source for non-Chinese ladder demos, including top-ranked players.
"""

from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Any, Iterable, Optional

from ..errors import SourceError
from ..net import HttpError, request_json
from ..pipeline import Job
from ..util import format_date, parse_date_argument, redact_url

API = "https://api.renown.gg/v1"
HEADERS = {
    "User-Agent": "demo_fetch/0.1 (CS2 demo collection)",
    "Accept": "application/json",
}


@dataclass
class RenownMatch:
    match_id: str
    demo_url: str
    map_name: str
    finished_at: str
    detail: dict[str, Any]

    @property
    def date(self) -> str:
        return format_date(self.finished_at)

    @property
    def map_label(self) -> str:
        return self.map_name.replace("de_", "") if self.map_name else ""


def _get(url: str, *, timeout: float, proxy: Optional[str]) -> Any:
    try:
        return request_json(url, headers=HEADERS, timeout=timeout, proxy=proxy)
    except HttpError as error:
        raise SourceError(f"Renown API 请求失败: {error}") from error


def leaderboard(
    *, limit: int = 10, timeout: float = 20.0, proxy: Optional[str] = None
) -> list[dict[str, Any]]:
    data = _get(f"{API}/leaderboard", timeout=timeout, proxy=proxy)
    rows = data.get("leaderboard") if isinstance(data, dict) else None
    if not isinstance(rows, list):
        raise SourceError("Renown leaderboard 响应格式异常")
    return [row for row in rows if isinstance(row, dict)][: max(1, limit)]


def player_matches(
    steamid: str,
    *,
    limit: int = 10,
    timeout: float = 20.0,
    proxy: Optional[str] = None,
) -> list[dict[str, Any]]:
    matches: list[dict[str, Any]] = []
    page = 1
    page_size = 20
    while len(matches) < limit:
        url = (
            f"{API}/player/{steamid}/matches"
            f"?page={page}&limit={page_size}&status=FINISHED"
        )
        data = _get(url, timeout=timeout, proxy=proxy)
        rows = data.get("data") if isinstance(data, dict) else None
        if not isinstance(rows, list) or not rows:
            break
        matches.extend(row for row in rows if isinstance(row, dict))
        if len(rows) < page_size:
            break
        page += 1
    return matches[:limit]


def match_detail(
    match_id: str, *, timeout: float = 20.0, proxy: Optional[str] = None
) -> dict[str, Any]:
    data = _get(f"{API}/match/{match_id}", timeout=timeout, proxy=proxy)
    return data if isinstance(data, dict) else {}


def match_from_row(
    row: dict[str, Any], detail: Optional[dict[str, Any]] = None
) -> Optional[RenownMatch]:
    match_id = str(row.get("match_id") or "").strip()
    if not match_id:
        return None
    source = detail or row
    demo_url = str(source.get("demo_url") or row.get("demo_url") or "").strip()
    map_data = source.get("map") or row.get("map") or {}
    map_name = str(map_data.get("name") or "") if isinstance(map_data, dict) else ""
    finished_at = str(source.get("finished_at") or row.get("finished_at") or "")
    return RenownMatch(
        match_id=match_id,
        demo_url=demo_url,
        map_name=map_name,
        finished_at=finished_at,
        detail={"row": row, "detail": detail or {}},
    )


def jobs(
    *,
    steamids: Iterable[str] = (),
    leaderboard_count: int = 0,
    limit: int = 10,
    maps: Iterable[str] = (),
    since: Optional[str] = None,
    timeout: float = 20.0,
    proxy: Optional[str] = None,
    delay: float = 1.0,
    quiet: bool = False,
) -> list[Job]:
    wanted_maps = {
        str(item).lower().removeprefix("de_") for item in maps if str(item).strip()
    }
    since_date = parse_date_argument(since)
    players: list[tuple[str, str]] = []
    for steamid in steamids:
        steamid = str(steamid).strip()
        if steamid:
            players.append((steamid, ""))
    if leaderboard_count:
        rows = leaderboard(limit=leaderboard_count, timeout=timeout, proxy=proxy)
        for row in rows:
            steamid = str(row.get("steam_id") or "").strip()
            if steamid:
                players.append((steamid, str(row.get("nickname") or "")))

    seen_players: set[str] = set()
    deduped_players: list[tuple[str, str]] = []
    for steamid, nickname in players:
        if steamid in seen_players:
            continue
        seen_players.add(steamid)
        deduped_players.append((steamid, nickname))

    jobs_out: list[Job] = []
    seen_matches: set[str] = set()
    for steamid, nickname in deduped_players:
        rows = player_matches(steamid, limit=limit, timeout=timeout, proxy=proxy)
        if not quiet:
            print(f"  Renown {nickname or steamid}: {len(rows)} 场候选", flush=True)
        for row in rows:
            match_id = str(row.get("match_id") or "").strip()
            if not match_id or match_id in seen_matches:
                continue
            seen_matches.add(match_id)
            row_date = format_date(row.get("finished_at"))
            if since_date and row_date and parse_date_argument(row_date) < since_date:
                continue
            match = match_from_row(row)
            if match is None:
                continue
            if not match.demo_url:
                # Older list responses may omit demo_url; fetch the detail once.
                try:
                    detail = match_detail(match_id, timeout=timeout, proxy=proxy)
                    match = match_from_row(row, detail) or match
                except SourceError:
                    pass
            if not match.demo_url:
                if not quiet:
                    print(f"  - Renown {match_id} 没有 demo URL，跳过", flush=True)
                continue
            if wanted_maps and match.map_label.lower() not in wanted_maps:
                continue
            jobs_out.append(
                Job(
                    source="renown",
                    match_id=match_id,
                    url=match.demo_url,
                    map_name=match.map_label,
                    date=match.date,
                    account=nickname or steamid,
                    suggested_name=f"renown_{match_id}_{match.map_label}_{match.date}",
                    meta={
                        "steamid": steamid,
                        "nickname": nickname,
                        "demo_url": redact_url(match.demo_url),
                    },
                )
            )
            if delay > 0:
                time.sleep(delay)
        if delay > 0:
            time.sleep(delay)
    return jobs_out
