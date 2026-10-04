"""
Load test for the PII service.

    uv run python load_testing.py
    uv run python load_testing.py --endpoint /redact --requests 20

Sends the OpenAI chat-completions requests in test_prompt/ to the service as
{"text": "<request json>"} one request at a time (use --clients N for concurrency).
"""
import argparse
import json
import logging
import statistics
import threading
import time
import urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

PROMPT_DIR = Path(__file__).parent / "test_prompt"

logging.basicConfig(level=logging.INFO, format="%(asctime)s.%(msecs)03d [%(threadName)s] %(message)s",
                    datefmt="%H:%M:%S")
log = logging.getLogger("load_testing")


# ---- Load test -----------------------------------------------------------
def send(url: str, body: bytes) -> tuple[float, int, str | None]:
    """POST one request; returns (latency_ms, findings, error)."""
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
    t = time.perf_counter()
    try:
        log.info("  POST %s (%d bytes) ...", url, len(body))
        with urllib.request.urlopen(req, timeout=300) as resp:
            t_head = time.perf_counter()
            log.info("  HTTP %d after %.0fms (time to first byte)", resp.status, (t_head - t) * 1000)
            raw = resp.read()
        t_body = time.perf_counter()
        data = json.loads(raw)
        findings = data.get("findings", [])
        log.info("  response %d bytes read in %.0fms, parsed in %.1fms: %d findings %s",
                 len(raw), (t_body - t_head) * 1000, (time.perf_counter() - t_body) * 1000,
                 len(findings), dict(Counter(f["type"] for f in findings)))
        if "text" in data:
            log.info("  redacted text: %d chars", len(data["text"]))
        return (time.perf_counter() - t) * 1000, len(findings), None
    except Exception as e:
        log.error("  request failed after %.0fms: %s", (time.perf_counter() - t) * 1000, e)
        return (time.perf_counter() - t) * 1000, 0, str(e)


def client(cid: int, url: str, prompts: list[Path], n: int) -> list[tuple[float, int, str | None]]:
    threading.current_thread().name = f"client-{cid}"
    results = []
    for i in range(n):
        path = prompts[(cid * n + i) % len(prompts)]
        t = time.perf_counter()
        content = path.read_text()
        req = json.loads(content)
        roles = Counter(m["role"] for m in req.get("messages", []))
        body = json.dumps({"text": content}).encode()
        log.info("req %d/%d %s: %d bytes on disk, %d messages %s, %d tools, body %d bytes "
                 "(prepared in %.1fms)", i + 1, n, path.name, len(content.encode("utf-8")),
                 sum(roles.values()), dict(roles), len(req.get("tools", [])), len(body),
                 (time.perf_counter() - t) * 1000)
        results.append(send(url, body))
        lat, found, err = results[-1]
        log.info("req %d/%d %s done: %s", i + 1, n, path.name,
                 f"ERROR {err}" if err else f"{lat:.0f} ms total, {found} findings")
    log.info("finished %d requests", n)
    return results


def run(url: str, clients: int, requests: int) -> None:
    prompts = sorted(PROMPT_DIR.glob("*.json"))
    if not prompts:
        raise SystemExit(f"No prompts in {PROMPT_DIR}.")
    log.info("target %s, %d client(s) x %d request(s), %d prompt files in %s",
             url, clients, requests, len(prompts), PROMPT_DIR)

    t = time.perf_counter()
    with ThreadPoolExecutor(max_workers=clients) as pool:
        futures = [pool.submit(client, c, url, prompts, requests) for c in range(clients)]
        results = [r for f in futures for r in f.result()]
    wall = time.perf_counter() - t

    ok = [r for r in results if r[2] is None]
    log.info("all clients finished")
    print(f"\n{len(results)} requests, {clients} clients, {wall:.1f}s wall, "
          f"{len(results) / wall:.2f} req/s, {len(results) - len(ok)} errors")
    if ok:
        lats = sorted(r[0] for r in ok)
        pct = lambda q: lats[min(len(lats) - 1, int(q * len(lats)))]
        print(f"latency ms  p50={pct(0.5):.0f}  p95={pct(0.95):.0f}  p99={pct(0.99):.0f}  "
              f"max={lats[-1]:.0f}  mean={statistics.mean(lats):.0f}")
        print(f"findings per request: {statistics.mean(r[1] for r in ok):.1f}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--host", default="http://127.0.0.1:3001")
    ap.add_argument("--endpoint", default="/detect", choices=["/detect", "/redact"])
    ap.add_argument("--clients", type=int, default=1, help="concurrent clients (1 = one request at a time)")
    ap.add_argument("--requests", type=int, default=10, help="requests per client")
    args = ap.parse_args()
    run(args.host + args.endpoint, args.clients, args.requests)
