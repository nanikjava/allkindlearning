"""
PII detection sidecar for the Go gateway, using GLiNER2-PII on CPU.

    pip install gliner2 fastapi uvicorn
    python pii_service.py demo          # run the example on the command line
    uvicorn pii_service:app --port 3001 # run as an HTTP service

POST /detect {"text": "..."} returns findings with character AND UTF-8 byte
offsets, so the Go gateway can slice its strings directly.
"""
import sys

from gliner2 import GLiNER2

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
model = GLiNER2.from_pretrained(MODEL_ID)


def detect(text: str) -> list[dict]:
    """Run the model and return a flat, sorted list of findings."""
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
    findings = []
    for label, hits in result.get("entities", {}).items():
        for h in hits:
            if h["confidence"] < THRESHOLDS.get(label, DEFAULT_THRESHOLD):
                continue
            start, end = h["start"], h["end"]
            # Guard against off-by-one spans: trust the text, re-find it if needed.
            if text[start:end] != h["text"]:
                start = text.find(h["text"])
                if start == -1:
                    continue
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
    return drop_overlaps(findings)


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
    for original in sorted(seen, key=len, reverse=True):
        out = out.replace(original, seen[original])
    return out, vault


# ---- HTTP service (used by the Go gateway) -------------------------------
try:
    from fastapi import FastAPI
    from pydantic import BaseModel

    app = FastAPI()

    class DetectRequest(BaseModel):
        text: str

    @app.post("/detect")
    def detect_endpoint(req: DetectRequest):
        return {"findings": detect(req.text)}

    @app.post("/redact")
    def redact_endpoint(req: DetectRequest):
        findings = detect(req.text)
        redacted, _vault = redact(req.text, findings)  # vault stays server-side
        return {"text": redacted, "findings": findings}
except ImportError:
    app = None


# ---- Command-line demo ---------------------------------------------------
if __name__ == "__main__" and sys.argv[1:] == ["demo"]:
    import json
    import time

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
