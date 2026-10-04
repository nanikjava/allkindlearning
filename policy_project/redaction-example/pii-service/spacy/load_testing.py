"""
Load test for the Presidio PII service using the prompts in ../test_prompt.

    uv run python load_testing.py --url http://127.0.0.1:3002/detect --requests 20 --users 5

Each user sends --requests requests one after another, cycling through the
prompt files; all users run concurrently.
"""
import argparse
import json
import statistics
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

PROMPT_DIR = Path(__file__).parent.parent / "test_prompt"
MAX_USERS = 10


def send(url: str, body: bytes) -> tuple[float, str | None, str]:
    """POST one request; returns (latency_ms, error, response_body)."""
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    t = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            text = resp.read().decode("utf-8")
        return (time.perf_counter() - t) * 1000, None, text
    except Exception as e:
        return (time.perf_counter() - t) * 1000, str(e), ""


def pretty(text: str) -> str:
    """Indent a JSON response; fall back to the raw text if it isn't JSON."""
    try:
        return json.dumps(json.loads(text), indent=2, ensure_ascii=False)
    except ValueError:
        return text


def user(uid: int, url: str, bodies: list[bytes], n: int) -> list[tuple[float, str | None]]:
    results = []
    for i in range(n):
        lat, err, text = send(url, bodies[(uid * n + i) % len(bodies)])
        results.append((lat, err))
        line = f"user {uid + 1} req {i + 1}/{n}: " + (f"ERROR {err}" if err else f"{lat:.0f} ms")
        # One print per request so output from concurrent users doesn't interleave.
        print(line if err else f"{line}\n{pretty(text)}", flush=True)
    return results


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--url", default="http://127.0.0.1:3002/detect", help="endpoint URL")
    p.add_argument("--requests", type=int, default=10, help="requests per user")
    p.add_argument("--users", type=int, default=1, help=f"concurrent users (1-{MAX_USERS})")
    args = p.parse_args()
    if not 1 <= args.users <= MAX_USERS:
        p.error(f"--users must be between 1 and {MAX_USERS}")
    if args.requests < 1:
        p.error("--requests must be at least 1")

    paths = sorted(PROMPT_DIR.glob("*.json"))
    if not paths:
        raise SystemExit(f"No prompts in {PROMPT_DIR}.")
    bodies = [json.dumps({"text": f.read_text()}).encode() for f in paths]

    print(f"target {args.url}: {args.users} user(s) x {args.requests} request(s), "
          f"{len(paths)} prompt files")
    t = time.perf_counter()
    with ThreadPoolExecutor(max_workers=args.users) as pool:
        futures = [pool.submit(user, u, args.url, bodies, args.requests) for u in range(args.users)]
        results = [r for f in futures for r in f.result()]
    wall = time.perf_counter() - t

    ok = sorted(lat for lat, err in results if err is None)
    print(f"\n{len(results)} requests, {len(ok)} ok, {len(results) - len(ok)} failed "
          f"in {wall:.1f}s ({len(results) / wall:.1f} req/s)")
    if ok:
        p95 = ok[min(len(ok) - 1, int(len(ok) * 0.95))]
        print(f"latency ms: avg {statistics.mean(ok):.0f}, p50 {statistics.median(ok):.0f}, "
              f"p95 {p95:.0f}, min {ok[0]:.0f}, max {ok[-1]:.0f}")


if __name__ == "__main__":
    main()
