#!/usr/bin/env python3
"""Offline tests for learn/demo_fetch (no network required).

Run:
  learn/.venv/bin/python -m unittest discover -s learn/tests -v
"""

from __future__ import annotations

import contextlib
import gzip
import io
import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest import mock

HERE = Path(__file__).resolve().parent
LEARN = HERE.parent
REPO = LEARN.parent
sys.path.insert(0, str(REPO))

from learn.demo_fetch import cli, net  # noqa: E402
from learn.demo_fetch.archive import extract_demos  # noqa: E402
from learn.demo_fetch.errors import SourceError  # noqa: E402
from learn.demo_fetch.manifest import Manifest, demo_key  # noqa: E402
from learn.demo_fetch.pipeline import Job, local_jobs, run_jobs  # noqa: E402
from learn.demo_fetch.sources import fivee, hltv, renown, steam, urls  # noqa: E402
from learn.demo_fetch.util import safe_name  # noqa: E402


class UtilTests(unittest.TestCase):
    def test_safe_name(self):
        self.assertEqual(safe_name("Spirit vs FUT/2026"), "Spirit vs FUT_2026")
        self.assertEqual(safe_name(""), "demo")
        self.assertLessEqual(len(safe_name("x" * 200, max_len=20)), 20)
        self.assertEqual(safe_name("CON"), "_CON")

    def test_share_code_decode(self):
        decoded = steam.decode_share_code("CSGO-LkCOc-UwdN3-dkQBV-F9Sxz-KeS8N")
        self.assertIsInstance(decoded["matchid"], int)
        self.assertIsInstance(decoded["outcomeid"], int)
        self.assertEqual(decoded["matchid"], 3746909372878422222)
        self.assertEqual(decoded["outcomeid"], 3746913113794936940)
        self.assertEqual(decoded["token"], 49235)

    def test_netscape_cookies(self):
        from learn.demo_fetch.util import read_netscape_cookies

        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "cookies.txt"
            path.write_text(
                "# Netscape HTTP Cookie File\n"
                ".hltv.org\tTRUE\t/\tTRUE\t0\tcf_clearance\tabc\n"
                "#HttpOnly_.hltv.org\tTRUE\t/\tTRUE\t0\tautologin\tdef\n",
                encoding="utf-8",
            )
            self.assertEqual(
                read_netscape_cookies(path),
                "cf_clearance=abc; autologin=def",
            )

    def test_invalid_share_code(self):
        with self.assertRaises(SourceError):
            steam.decode_share_code("not-a-code")


class HltvParseTests(unittest.TestCase):
    def test_page_fetch_passes_cookie_file(self):
        with mock.patch.object(hltv, "request_text", return_value="ok") as mocked:
            self.assertEqual(
                hltv.fetch_hltv_text(
                    "https://www.hltv.org/results", cookie_file="/tmp/cookies.txt"
                ),
                "ok",
            )
        self.assertEqual(mocked.call_args.kwargs["cookie_file"], "/tmp/cookies.txt")

    def test_match_page_demo_link(self):
        page = """
        [Demo download](https://www.hltv.org/matches/2399724/x-y)
        [Click here](https://www.hltv.org/download/demo/113158)
        """
        self.assertEqual(hltv.parse_demo_ids(page), ["113158"])

    def test_match_page_html_demo_link(self):
        page = '<a href="/download/demo/113158">Download</a>'
        self.assertEqual(hltv.parse_demo_ids(page), ["113158"])

    def test_results_dates(self):
        page = """
        Results for November 1st 2026
        [A vs B bo3](https://www.hltv.org/matches/2399986/a-vs-b-event)
        [C vs D bo5](https://www.hltv.org/matches/2399985/c-vs-d-event)

        Results for October 31st 2026
        [E vs F bo3](https://www.hltv.org/matches/2399901/e-vs-f-event)
        """
        matches = hltv.parse_results_page(page)
        self.assertEqual(len(matches), 3)
        self.assertEqual(matches[0].date, "2026-11-01")
        self.assertEqual(matches[2].date, "2026-10-31")
        self.assertEqual(matches[0].match_id, "2399986")

    def test_relative_links(self):
        page = '<div class="result-con"><a href="/matches/2399759/nemiga-vs-furia">x</a></div>'
        matches = hltv.parse_match_links(page)
        self.assertEqual(len(matches), 1)
        self.assertEqual(matches[0].match_id, "2399759")

    def test_jobs_from_match_url(self):
        from unittest import mock

        page = "[Click](https://www.hltv.org/download/demo/113158)"
        with mock.patch.object(hltv, "fetch_hltv_text", return_value=page):
            jobs = hltv.jobs(
                match_urls=["https://www.hltv.org/matches/2399724/legacy-vs-nemiga"],
                limit=5,
            )
        self.assertEqual(len(jobs), 1)
        self.assertEqual(jobs[0].source, "hltv")
        self.assertEqual(jobs[0].match_id, "2399724")
        self.assertEqual(jobs[0].demo_id, "113158")
        self.assertTrue(jobs[0].url.endswith("/download/demo/113158"))
        self.assertIsNotNone(jobs[0].fallback_url_resolver)

    def test_extract_cdn_url_from_jina_challenge(self):
        text = (
            "Title: Attention Required!\n"
            "Final URL: https://r2-demos.hltv.org/demos/132423/"
            "pgl-masters-bucharest-2026-legacy-vs-nemiga-bo3-abc.rar\n"
        )
        self.assertEqual(
            hltv.extract_cdn_url(text),
            "https://r2-demos.hltv.org/demos/132423/"
            "pgl-masters-bucharest-2026-legacy-vs-nemiga-bo3-abc.rar",
        )

    def test_results_ignore_sidebar_and_assign_featured_date(self):
        page = """
        [Featured A vs B bo3](https://www.hltv.org/matches/2399986/a-vs-b-event)
        Results for November 1st 2026
        [C vs D bo3](https://www.hltv.org/matches/2399985/c-vs-d-event)
        [sidebar E vs F 19](https://www.hltv.org/matches/2399901/e-vs-f-event)
        """
        matches = hltv.parse_results_page(page)
        self.assertEqual([match.match_id for match in matches], ["2399986", "2399985"])
        self.assertEqual(matches[0].date, "2026-11-01")


class FiveETests(unittest.TestCase):
    def test_parse_user_id(self):
        self.assertEqual(
            fivee.parse_user_id("https://arena.5eplay.com/data/player/11814738gjdwn7"),
            "11814738gjdwn7",
        )
        self.assertEqual(fivee.parse_user_id("11814738gjdwn7"), "11814738gjdwn7")

    def test_match_from_detail(self):
        detail = {
            "main": {
                "demo_url": "//oss.example/demo.dem",
                "map": "de_mirage",
                "start_time": "1791294180",
                "end_time": "1791296469",
            }
        }
        match = fivee.match_from_detail("g161-1", detail)
        self.assertIsNotNone(match)
        self.assertEqual(match.demo_url, "https://oss.example/demo.dem")
        self.assertEqual(match.map_label, "mirage")
        self.assertEqual(match.date, "2026-10-06")


class RenownTests(unittest.TestCase):
    def test_match_from_row(self):
        row = {
            "match_id": 18373,
            "demo_url": "https://demos.renown.gg/renown-match-18373.dem.gz",
            "map": {"name": "de_mirage"},
            "finished_at": "2026-01-22T22:36:03.000Z",
        }
        match = renown.match_from_row(row)
        self.assertIsNotNone(match)
        self.assertEqual(match.map_label, "mirage")
        self.assertEqual(match.date, "2026-01-22")
        self.assertTrue(match.demo_url.endswith(".dem.gz"))


class SteamTests(unittest.TestCase):
    def test_resolver_command(self):
        code = "CSGO-LkCOc-UwdN3-dkQBV-F9Sxz-KeS8N"
        command = f"{sys.executable} -c \"print('http://replay403.valve.net/730/demo.dem.bz2')\""
        url = steam.resolve_with_command(code, command)
        self.assertEqual(url, "http://replay403.valve.net/730/demo.dem.bz2")


class StoreTests(unittest.TestCase):
    def test_zip_multi_extract(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "series.zip"
            with zipfile.ZipFile(archive, "w") as handle:
                handle.writestr("a.dem", b"DEMO-A")
                handle.writestr("maps/b.dem", b"DEMO-B")
            demos = extract_demos(archive, root, "hltv_1")
            self.assertEqual(len(demos), 2)
            self.assertEqual(
                {demo.path.name for demo in demos},
                {"hltv_1__a.dem", "hltv_1__maps_b.dem"},
            )
            self.assertFalse(archive.exists())
            self.assertEqual(demos[0].size, 6)

    def test_tar_xz_extract(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "series.tar.xz"
            source = root / "a.dem"
            source.write_bytes(b"DEMO")
            with tarfile.open(archive, "w:xz") as handle:
                handle.add(source, arcname="a.dem")
            source.unlink()
            demos = extract_demos(archive, root, "series")
            self.assertEqual(len(demos), 1)
            self.assertEqual(demos[0].path.name, "series.dem")
            self.assertEqual(demos[0].path.read_bytes(), b"DEMO")

    def test_tar_zst_extract(self):
        if not shutil.which("zstd"):
            self.skipTest("zstd 未安装")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            tar_path = root / "series.tar"
            source = root / "a.dem"
            source.write_bytes(b"DEMO")
            with tarfile.open(tar_path, "w") as handle:
                handle.add(source, arcname="a.dem")
            source.unlink()
            archive = root / "series.tar.zst"
            subprocess.run(
                ["zstd", "-q", "-f", "-o", str(archive), str(tar_path)],
                check=True,
            )
            demos = extract_demos(archive, root, "series")
            self.assertEqual(len(demos), 1)
            self.assertEqual(demos[0].path.read_bytes(), b"DEMO")

    def test_zip_duplicate_basenames(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "series.zip"
            with zipfile.ZipFile(archive, "w") as handle:
                handle.writestr("map1/a.dem", b"ONE")
                handle.writestr("map2/a.dem", b"TWO")
            demos = extract_demos(archive, root, "series")
            self.assertEqual(len(demos), 2)
            self.assertEqual(len({demo.path.name for demo in demos}), 2)
            self.assertEqual(
                {demo.path.read_bytes() for demo in demos}, {b"ONE", b"TWO"}
            )

    def test_gz_extract(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = root / "match.dem.gz"
            with gzip.open(archive, "wb") as handle:
                handle.write(b"DEMO")
            demos = extract_demos(archive, root, "renown_1")
            self.assertEqual(len(demos), 1)
            self.assertEqual(demos[0].path.name, "renown_1.dem")
            self.assertEqual(demos[0].path.read_bytes(), b"DEMO")

    def test_manifest_roundtrip(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest = Manifest(root)
            digest = "a" * 64
            manifest.add(
                {
                    "source": "renown",
                    "match_id": "1",
                    "demo_id": "",
                    "path": "x.dem",
                    "bytes": 10,
                    "sha256": digest,
                }
            )
            self.assertTrue(manifest.has_key(demo_key("renown", "1", "")))
            self.assertTrue(manifest.has_hash(digest))
            reloaded = Manifest(root)
            self.assertEqual(reloaded.summary()["demos"], 1)
            self.assertTrue((root / "manifest.jsonl").exists())

    def test_local_jobs_and_run(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "my_demo.dem"
            source.write_bytes(b"DEMO")
            out = root / "out"
            jobs = local_jobs([source])
            self.assertEqual(len(jobs), 1)
            manifest = Manifest(out)
            summary = run_jobs(
                jobs, out_dir=out, manifest=manifest, quiet=True, delay=0
            )
            self.assertEqual(summary.downloaded, 1)
            self.assertEqual(summary.failed, 0)
            self.assertTrue((out / "import_my_demo.dem").exists())
            # Second run must be idempotent.
            manifest2 = Manifest(out)
            summary2 = run_jobs(
                local_jobs([source]),
                out_dir=out,
                manifest=manifest2,
                quiet=True,
                delay=0,
            )
            self.assertEqual(summary2.downloaded, 0)
            self.assertGreaterEqual(summary2.skipped, 1)

    def test_url_jobs(self):
        jobs = urls.jobs(
            urls=[
                "https://example.com/a/renown-1.dem.gz",
                "https://example.com/b/match.rar",
            ]
        )
        self.assertEqual(len(jobs), 2)
        self.assertEqual(jobs[0].suggested_name, "url_renown-1")
        self.assertEqual(jobs[1].suggested_name, "url_match")

    def test_url_jobs_same_basename_distinct_keys(self):
        jobs = urls.jobs(
            urls=[
                "https://a.example/x/demo.dem",
                "https://b.example/y/demo.dem",
            ]
        )
        self.assertEqual(len(jobs), 2)
        self.assertNotEqual(jobs[0].key, jobs[1].key)
        self.assertNotEqual(jobs[0].filename_base, jobs[1].filename_base)


class CliTests(unittest.TestCase):
    def test_dry_run_url(self):
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / "out"
            buffer = io.StringIO()
            with contextlib.redirect_stdout(buffer):
                code = cli.main(
                    [
                        "url",
                        "--dry-run",
                        "--out",
                        str(out),
                        "https://example.com/demo.dem",
                    ]
                )
            self.assertEqual(code, 0)
            self.assertIn("计划下载 1", buffer.getvalue())
            self.assertFalse(out.exists())

    def test_fail_fast_returns_clean_error(self):
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / "out"
            stderr = io.StringIO()
            with (
                mock.patch.object(cli, "run_jobs", side_effect=RuntimeError("boom")),
                contextlib.redirect_stderr(stderr),
            ):
                code = cli.main(
                    [
                        "url",
                        "--fail-fast",
                        "--out",
                        str(out),
                        "https://example.com/demo.dem",
                    ]
                )
            self.assertEqual(code, 1)
            self.assertIn("错误: boom", stderr.getvalue())

    def test_manifest_command(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest = Manifest(root)
            manifest.add(
                {
                    "source": "5e",
                    "match_id": "m1",
                    "map": "mirage",
                    "path": "m1.dem",
                    "bytes": 4,
                    "sha256": "b" * 64,
                }
            )
            buffer = io.StringIO()
            with contextlib.redirect_stdout(buffer):
                code = cli.main(["manifest", "--out", str(root), "--json"])
            self.assertEqual(code, 0)
            payload = json.loads(buffer.getvalue())
            self.assertEqual(payload["demos"], 1)
            self.assertEqual(payload["by_source"], {"5e": 1})


if __name__ == "__main__":
    unittest.main()
