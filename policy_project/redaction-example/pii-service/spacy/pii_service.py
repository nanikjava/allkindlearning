"""
PII detection sidecar for the Go gateway, using Microsoft Presidio on CPU.

    uv sync                                    # install deps from pyproject.toml
    uv run python pii_service.py demo          # run the example on the command line
    uv run python pii_service.py demo --file prompt_au_licence.json  # use a test_prompt file
    uv run python pii_service.py server        # run as an HTTP service on :3002
    uv run uvicorn pii_service:app --port 3002 # same, via the uvicorn CLI

Same API as gliner2/pii_service.py: POST /detect {"text": "..."} returns
findings with character AND UTF-8 byte offsets, POST /redact also returns the
redacted text.
"""
import logging
import os
import sys
import time
from collections import Counter

from presidio_analyzer import AnalyzerEngine, Pattern, PatternRecognizer
from presidio_analyzer.nlp_engine import NlpEngineProvider
from presidio_analyzer.predefined_recognizers import AuMedicareRecognizer, AuTfnRecognizer

from au_licence_recognizer import AuDriverLicenceRecognizer

# LOG_LEVEL=DEBUG also logs every finding (label, score, offsets; never the text).
logging.basicConfig(
    level=os.environ.get("LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s.%(msecs)03d %(levelname)-5s %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger("pii_service")
# Presidio warns at startup about every non-English recognizer it skips; keep
# those out of the log unless LOG_LEVEL=DEBUG.
if log.getEffectiveLevel() > logging.DEBUG:
    logging.getLogger("presidio-analyzer").setLevel(logging.ERROR)

SPACY_MODEL = os.environ.get("SPACY_MODEL", "en_core_web_lg")
# SPACY_MODEL = os.environ.get("SPACY_MODEL", "en_core_web_md")


# Presidio entity types we ask for, mapped to the finding types the gateway's
# policy already understands. Asking for fewer entities is faster.
LABELS = {
    "PERSON": "pii.name",
    "EMAIL_ADDRESS": "pii.email",
    "PHONE_NUMBER": "pii.phone",
    "LOCATION": "pii.address",
    "US_SSN": "pii.national_id",
    "US_PASSPORT": "pii.national_id",
    "UK_NHS": "pii.national_id",
    "AU_TFN": "pii.national_id",
    "AU_MEDICARE": "pii.national_id",
    "AU_DRIVER_LICENCE": "pii.national_id",
    "IBAN_CODE": "pii.bank_account",
    "US_BANK_NUMBER": "pii.bank_account",
    "CREDIT_CARD": "pci.card_number",
    "API_KEY": "secret.api_key",
}

# Per-entity minimum confidence. Names are noisier, secrets are costly to miss.
THRESHOLDS = {"PERSON": 0.6, "LOCATION": 0.6, "PHONE_NUMBER": 0.4, "API_KEY": 0.4}
DEFAULT_THRESHOLD = 0.5

class PresidioDetector:
    """Wraps the Presidio analyzer: loads the engine once, then detects PII."""

    def __init__(self, spacy_model: str = SPACY_MODEL):
        self.spacy_model = spacy_model
        log.info("loading presidio with spaCy model %s ...", spacy_model)
        t = time.perf_counter()
        nlp_engine = NlpEngineProvider(nlp_configuration={
            "nlp_engine_name": "spacy",
            "models": [{"lang_code": "en", "model_name": spacy_model}],
            # spaCy labels we don't map to a finding type (FAC, ORG, dates, numbers...).
            "ner_model_configuration": {
                "labels_to_ignore": ["FAC", "ORG", "ORGANIZATION", "NORP", "DATE", "TIME",
                                     "CARDINAL", "ORDINAL", "QUANTITY", "MONEY", "PERCENT",
                                     "PRODUCT", "EVENT", "WORK_OF_ART", "LAW", "LANGUAGE"],
            },
        }).create_engine()
        self.analyzer = AnalyzerEngine(nlp_engine=nlp_engine, supported_languages=["en"])
        self.analyzer.registry.add_recognizer(self._api_key_recognizer())
        # Presidio ships the Australian recognizers but doesn't load them by default.
        self.analyzer.registry.add_recognizer(AuTfnRecognizer())
        self.analyzer.registry.add_recognizer(AuMedicareRecognizer())
        self.analyzer.registry.add_recognizer(AuDriverLicenceRecognizer())  # ours, not Presidio's
        log.info("presidio loaded in %.1fs", time.perf_counter() - t)

    @staticmethod
    def _api_key_recognizer() -> PatternRecognizer:
        # Presidio has no built-in API key recognizer; same patterns as detect.go.
        return PatternRecognizer(
            supported_entity="API_KEY",
            patterns=[
                Pattern("openai_style", r"\bsk-[A-Za-z0-9_-]{20,}\b", 0.8),
                Pattern("aws_access_key", r"\bAKIA[0-9A-Z]{16}\b", 0.9),
            ],
        )

    def detect(self, text: str) -> list[dict]:
        """Run Presidio over the text and return non-overlapping findings."""
        t = time.perf_counter()
        results = self.analyzer.analyze(text=text, language="en", entities=list(LABELS))
        findings = []
        for r in results:
            if r.score < THRESHOLDS.get(r.entity_type, DEFAULT_THRESHOLD):
                continue
            findings.append({
                "type": LABELS[r.entity_type],
                "label": r.entity_type,
                "score": round(r.score, 3),
                "start": r.start,
                "end": r.end,
                "byte_start": len(text[:r.start].encode("utf-8")),
                "byte_end": len(text[:r.end].encode("utf-8")),
            })
        kept = drop_overlaps(findings)
        log.info("  detect: %d raw, %d above threshold -> %d findings %s in %.0fms",
                 len(results), len(findings), len(kept),
                 dict(Counter(f["type"] for f in kept)), (time.perf_counter() - t) * 1000)
        for f in kept:
            log.debug("    %-18s %-20s score=%.3f chars=%d-%d bytes=%d-%d", f["type"], f["label"],
                      f["score"], f["start"], f["end"], f["byte_start"], f["byte_end"])
        return kept


def drop_overlaps(findings: list[dict]) -> list[dict]:
    """Keep the highest-scoring finding when two spans overlap (longest on ties)."""
    kept: list[dict] = []
    for f in sorted(findings, key=lambda f: (-f["score"], f["start"] - f["end"])):
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
    # Redact other occurrences of a known value that detection missed.
    for original in sorted(seen, key=len, reverse=True):
        out = out.replace(original, seen[original])
    return out, vault


# ---- HTTP service (used by the Go gateway) -------------------------------
from fastapi import FastAPI
from pydantic import BaseModel

# Load once at startup.
detector = PresidioDetector()
app = FastAPI()


class DetectRequest(BaseModel):
    text: str


@app.get("/health")
def health():
    return {"status": "ok", "engine": "presidio", "model": detector.spacy_model}


@app.post("/detect")
def detect_endpoint(req: DetectRequest):
    t = time.perf_counter()
    findings = detector.detect(req.text)
    log.info("/detect %.0fms bytes=%d findings=%d", (time.perf_counter() - t) * 1000,
             len(req.text.encode("utf-8")), len(findings))
    return {"findings": findings}


@app.post("/redact")
def redact_endpoint(req: DetectRequest):
    t = time.perf_counter()
    findings = detector.detect(req.text)
    redacted, _vault = redact(req.text, findings)  # vault stays server-side
    log.info("/redact %.0fms bytes=%d findings=%d", (time.perf_counter() - t) * 1000,
             len(req.text.encode("utf-8")), len(findings))
    return {"text": redacted, "findings": findings}


# ---- Command-line demo ---------------------------------------------------
if __name__ == "__main__" and sys.argv[1:2] == ["demo"]:
    import argparse
    import json
    from pathlib import Path

    p = argparse.ArgumentParser(prog="pii_service.py demo")
    p.add_argument("--file", help="one file name in test_prompt/ (default: built-in example)")
    args = p.parse_args(sys.argv[2:])

    prompt = (
        "Hi, I'm Jane Doe, customer since 2019. My IBAN is DE89 3704 0044 0532 0130 00 "
        "and you can reach me at jane.doe@example.com or +44 20 7946 0958. "
        "I live at 221B Baker Street, London. Our staging key is sk-live-9f8a7b6c5d4e3f2a1b0c."
    )
    if args.file:
        prompt = (Path(__file__).parent.parent / "test_prompt" / args.file).read_text()
    detector.detect(prompt)  # warm-up; the first call is slower
    t = time.perf_counter()
    findings = detector.detect(prompt)
    ms = (time.perf_counter() - t) * 1000

    print(json.dumps(findings, indent=2))
    redacted, vault = redact(prompt, findings)
    print("\nRedacted:", redacted)
    print("Vault:   ", vault)
    print(f"\nDetection took {ms:.0f} ms on CPU")


# ---- HTTP server ---------------------------------------------------------
if __name__ == "__main__" and sys.argv[1:] == ["server"]:
    import uvicorn

    log.info("starting server on http://127.0.0.1:3002")
    uvicorn.run(app, host="127.0.0.1", port=3002)
