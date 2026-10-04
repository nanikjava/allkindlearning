"""
PII detection sidecar for the Go gateway, using GLiNER2-PII on CPU.

    uv sync                                    # install deps from pyproject.toml
    uv run python pii_service.py demo          # run the example on the command line
    uv run python pii_service.py server        # run as an HTTP service on :3001
    uv run uvicorn pii_service:app --port 3001 # same, via the uvicorn CLI

POST /detect {"text": "..."} returns findings with character AND UTF-8 byte
offsets, so the Go gateway can slice its strings directly.
"""
import logging
import os
import sys
import time
from collections import Counter

from gliner2 import GLiNER2

# LOG_LEVEL=DEBUG also logs every finding (label, score, offsets; never the text).
logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s.%(msecs)03d %(levelname)-5s %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("pii_service")

MODEL_ID = "fastino/gliner2-privacy-filter-PII-multi"

# Labels we ask the model for, mapped to the finding types the gateway's
# policy already understands. Asking for fewer labels is faster.
LABELS = {
    "person": "pii.name",
    "email": "pii.email",
    "phone_number": "pii.phone",
    "street_address": "pii.address",
    "national_id_number": "pii.national_id",
    "passport_number": "pii.national_id",
    "iban": "pii.bank_account",
    "bank_account": "pii.bank_account",
    "card_number": "pci.card_number",
    "api_key": "secret.api_key",
    "password": "secret.password",
}

# Per-label minimum confidence. Names are noisier, secrets are costly to miss.
THRESHOLDS = {"person": 0.6, "api_key": 0.4, "password": 0.4}
DEFAULT_THRESHOLD = 0.5

# Load once at startup (downloads ~0.8 GB the first time, then cached).
log.info("loading model %s ...", MODEL_ID)
_t = time.perf_counter()
model = GLiNER2.from_pretrained(MODEL_ID)
log.info("model loaded in %.1fs", time.perf_counter() - _t)


CHUNK_BYTES = 2 * 1024


def split_chunks(text: str, max_bytes: int = CHUNK_BYTES) -> list[tuple[int, str]]:
    """Split text into (char_offset, chunk) pieces of at most max_bytes UTF-8 bytes,
    breaking on whitespace where possible so words/entities aren't cut in half."""
    chunks = []
    start = 0
    while start < len(text):
        end = min(len(text), start + max_bytes)
        while len(text[start:end].encode("utf-8")) > max_bytes:
            end -= max(1, (len(text[start:end].encode("utf-8")) - max_bytes) // 4)
        if end < len(text):
            cut = max(text.rfind(c, start, end) for c in " \n\t")
            if cut > start + (end - start) // 2:
                end = cut + 1
        chunks.append((start, text[start:end]))
        start = end
    return chunks


def detect(text: str) -> list[dict]:
    """Split text into 5 KB chunks, run the model on each, and return findings
    with offsets relative to the full text."""
    chunks = split_chunks(text)
    log.info("  detect: %d chars / %d bytes split into %d chunk(s)",
             len(text), len(text.encode("utf-8")), len(chunks))
    findings = []
    for i, (offset, chunk) in enumerate(chunks):
        log.info("  chunk %d/%d at char %d", i + 1, len(chunks), offset)
        byte_offset = len(text[:offset].encode("utf-8"))
        for f in detect_chunk(chunk):
            f["start"] += offset
            f["end"] += offset
            f["byte_start"] += byte_offset
            f["byte_end"] += byte_offset
            findings.append(f)
    return findings


def detect_chunk(text: str) -> list[dict]:
    """Run the model on one chunk and return a flat, sorted list of findings."""
    log.info("  detect: input %d chars / %d bytes, %d labels",
             len(text), len(text.encode("utf-8")), len(LABELS))
    t = time.perf_counter()
    result = model.extract_entities(
        text,
        list(LABELS),
        threshold=min([DEFAULT_THRESHOLD, *THRESHOLDS.values()]),
        include_confidence=True,
        include_spans=True,
    )
    # Result shape:
    # {"entities": {"person": [{"text": "Jane Doe", "confidence": 0.93,
    #                           "start": 9, "end": 17}], ...}}
    entities = result.get("entities", {})
    raw = {label: len(hits) for label, hits in entities.items() if hits}
    log.info("  detect: model inference %.0fms, %d raw hits %s",
             (time.perf_counter() - t) * 1000, sum(raw.values()), raw)

    t = time.perf_counter()
    findings = []
    below, refound, lost = 0, 0, 0
    for label, hits in entities.items():
        for h in hits:
            if h["confidence"] < THRESHOLDS.get(label, DEFAULT_THRESHOLD):
                below += 1
                continue
            start, end = h["start"], h["end"]
            # Guard against off-by-one spans: trust the text, re-find it if needed.
            if text[start:end] != h["text"]:
                start = text.find(h["text"])
                if start == -1:
                    lost += 1
                    continue
                refound += 1
                end = start + len(h["text"])
            findings.append({
                "type": LABELS[label],
                "label": label,
                "score": round(h["confidence"], 3),
                "start": start,  # character offsets (Python)
                "end": end,
                # Byte offsets for Go, which indexes strings by UTF-8 bytes.
                "byte_start": len(text[:start].encode("utf-8")),
                "byte_end": len(text[:end].encode("utf-8")),
            })
    findings.sort(key=lambda f: f["start"])
    kept = drop_overlaps(findings)
    log.info("  detect: post-process %.1fms: %d below threshold, %d spans re-found, "
             "%d spans lost, %d overlaps dropped -> %d findings %s",
             (time.perf_counter() - t) * 1000, below, refound, lost,
             len(findings) - len(kept), len(kept), dict(Counter(f["type"] for f in kept)))
    for f in kept:
        log.debug("    %-18s %-20s score=%.3f chars=%d-%d bytes=%d-%d", f["type"], f["label"],
                  f["score"], f["start"], f["end"], f["byte_start"], f["byte_end"])
    return kept


def drop_overlaps(findings: list[dict]) -> list[dict]:
    """Keep the highest-scoring finding when two spans overlap."""
    kept: list[dict] = []
    for f in sorted(findings, key=lambda f: -f["score"]):
        if all(f["end"] <= k["start"] or f["start"] >= k["end"] for k in kept):
            kept.append(f)
    return sorted(kept, key=lambda f: f["start"])


def redact(text: str, findings: list[dict]) -> tuple[str, dict]:
    """Replace findings with stable placeholders like [NAME_1].

    Returns the redacted text and a vault {placeholder: original} that the
    caller keeps in memory to restore the model's reply if policy allows.
    """
    t = time.perf_counter()
    vault: dict[str, str] = {}
    seen: dict[str, str] = {}
    counters: dict[str, int] = {}
    # Assign numbers left to right so [NAME_1] is the first name in the text.
    for f in findings:
        original = text[f["start"]:f["end"]]
        if original not in seen:
            tag = f["type"].split(".")[-1].upper()
            counters[tag] = counters.get(tag, 0) + 1
            seen[original] = f"[{tag}_{counters[tag]}]"
            vault[seen[original]] = original
    # Replace right to left so earlier offsets stay valid.
    out = text
    for f in sorted(findings, key=lambda f: -f["start"]):
        out = out[:f["start"]] + seen[text[f["start"]:f["end"]]] + out[f["end"]:]
    # Catch repeats the model missed: once a value is known to be PII,
    # redact every other occurrence of it too (longest first).
    after_spans = out
    for original in sorted(seen, key=len, reverse=True):
        out = out.replace(original, seen[original])
    extra = sum(after_spans.count(o) for o in seen)
    log.info("  redact: %d findings -> %d unique values %s, %d extra repeats replaced, "
             "%d -> %d chars in %.1fms", len(findings), len(vault), dict(counters), extra,
             len(text), len(out), (time.perf_counter() - t) * 1000)
    return out, vault


# ---- HTTP service (used by the Go gateway) -------------------------------
try:
    from fastapi import FastAPI
    from pydantic import BaseModel

    app = FastAPI()

    class DetectRequest(BaseModel):
        text: str

    from datetime import datetime

    def _now() -> str:
        return datetime.now().isoformat(timespec="milliseconds")

    @app.post("/detect")
    def detect_endpoint(req: DetectRequest):
        start, t = _now(), time.perf_counter()
        log.info("/detect received %d bytes", len(req.text.encode("utf-8")))
        findings = detect(req.text)
        ms = (time.perf_counter() - t) * 1000
        log.info("/detect start=%s end=%s detect=%.0fms bytes=%d findings=%d",
                 start, _now(), ms, len(req.text.encode("utf-8")), len(findings))
        return {"findings": findings}

    @app.post("/redact")
    def redact_endpoint(req: DetectRequest):
        start, t0 = _now(), time.perf_counter()
        log.info("/redact received %d bytes", len(req.text.encode("utf-8")))
        findings = detect(req.text)
        t1 = time.perf_counter()
        redacted, _vault = redact(req.text, findings)  # vault stays server-side
        t2 = time.perf_counter()
        log.info("/redact start=%s end=%s detect=%.0fms redact=%.0fms total=%.0fms bytes=%d findings=%d",
                 start, _now(), (t1 - t0) * 1000, (t2 - t1) * 1000, (t2 - t0) * 1000,
                 len(req.text.encode("utf-8")), len(findings))
        return {"text": redacted, "findings": findings}
except ImportError:
    app = None


# ---- Command-line demo ---------------------------------------------------
if __name__ == "__main__" and sys.argv[1:] == ["demo"]:
    import json

    prompt = (
        "Hi, I'm Jane Doe, customer since 2019. My IBAN is DE89 3704 0044 0532 0130 00 "
        "and you can reach me at jane.doe@example.com or +44 20 7946 0958. "
        "I live at 221B Baker Street, London. Our staging key is sk-live-9f8a7b6c5d4e3f2a1b0c."
    )
    detect(prompt)  # warm-up; the first call is slower
    t = time.perf_counter()
    findings = detect(prompt)
    ms = (time.perf_counter() - t) * 1000

    print(json.dumps(findings, indent=2))
    redacted, vault = redact(prompt, findings)
    print("\nRedacted:", redacted)
    print("Vault:   ", vault)
    print(f"\nDetection took {ms:.0f} ms on CPU")


# ---- HTTP server ---------------------------------------------------------
if __name__ == "__main__" and sys.argv[1:] == ["server"]:
    import uvicorn

    log.info("starting server on http://127.0.0.1:3001")

    # Reuse the already-loaded model instead of letting uvicorn re-import us.
    uvicorn.run(app, host="127.0.0.1", port=3001)
