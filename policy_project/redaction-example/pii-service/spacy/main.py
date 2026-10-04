"""
PII detection sidecar for the Go gateway, using Microsoft Presidio on CPU.

    uv sync                                    # install deps from pyproject.toml
    uv run python main.py demo          # run the example on the command line
    uv run python main.py demo --file prompt_au_licence.json  # use a test_prompt file
    uv run python main.py server        # run as an HTTP service on :3002
    uv run uvicorn main:create_app --factory --port 3002  # same, via the uvicorn CLI

Same API as gliner2/pii_service.py: POST /detect {"text": "..."} returns
findings with character AND UTF-8 byte offsets, POST /redact also returns the
redacted text.

The code is split into config.py (settings), recognizers.py (extra
recognizers), detector.py (Presidio) and redaction.py (placeholders); this
file wires them together: it reads the settings, sets up logging, loads the
detector, and holds the HTTP app and the command line.
"""
import argparse
import json
import logging
import sys
import time
from pathlib import Path

from fastapi import FastAPI
from pydantic import BaseModel

from config import Settings
from detector import PIIDetector
from redaction import redact


def setup_logging(log_level: str) -> logging.Logger:
    """Configure logging once; LOG_LEVEL=DEBUG also logs every finding (never the text)."""
    logging.basicConfig(
        level=log_level,
        format="%(asctime)s.%(msecs)03d %(levelname)-5s %(message)s",
        datefmt="%H:%M:%S",
    )
    log = logging.getLogger("pii_service")
    # Presidio warns at startup about every non-English recognizer it skips; keep
    # those out of the log unless LOG_LEVEL=DEBUG.
    if log.getEffectiveLevel() > logging.DEBUG:
        logging.getLogger("presidio-analyzer").setLevel(logging.ERROR)
    return log


PROMPT_DIR = Path(__file__).parent.parent / "test_prompt"
DEMO_PROMPT = (
    "Hi, I'm Jane Doe, customer since 2019. My IBAN is DE89 3704 0044 0532 0130 00 "
    "and you can reach me at jane.doe@example.com or +44 20 7946 0958. "
    "I live at 221B Baker Street, London. Our staging key is sk-live-9f8a7b6c5d4e3f2a1b0c."
)


# ---- HTTP service (used by the Go gateway) -------------------------------
class DetectRequest(BaseModel):
    text: str


def create_app(pii_detector: PIIDetector | None = None) -> FastAPI:
    """Build the HTTP app. With no detector (uvicorn --factory), load one from the environment."""
    if pii_detector is None:
        settings = Settings.from_env()
        setup_logging(settings.log_level)
        pii_detector = PIIDetector(settings.spacy)
    log = logging.getLogger("pii_service")
    app = FastAPI()

    def timed_detect(endpoint: str, text: str) -> list[dict]:
        t = time.perf_counter()
        findings = pii_detector.detect(text)
        log.info("%s %.0fms bytes=%d findings=%d", endpoint, (time.perf_counter() - t) * 1000,
                 len(text.encode("utf-8")), len(findings))
        return findings

    @app.get("/health")
    def health():
        return {"status": "ok", "engine": "presidio", "model": pii_detector.spacy_model}

    @app.post("/detect")
    def detect_endpoint(req: DetectRequest):
        return {"findings": timed_detect("/detect", req.text)}

    @app.post("/redact")
    def redact_endpoint(req: DetectRequest):
        findings = timed_detect("/redact", req.text)
        redacted, _vault = redact(req.text, findings)  # vault stays server-side
        return {"text": redacted, "findings": findings}

    return app


# ---- Command line --------------------------------------------------------
def run_demo(detector: PIIDetector, file: str | None) -> None:
    prompt = (PROMPT_DIR / file).read_text() if file else DEMO_PROMPT
    detector.detect(prompt)  # warm-up; the first call is slower
    t = time.perf_counter()
    findings = detector.detect(prompt)
    ms = (time.perf_counter() - t) * 1000

    print(json.dumps(findings, indent=2))
    redacted, vault = redact(prompt, findings)
    print("\nRedacted:", redacted)
    print("Vault:   ", vault)
    print(f"\nDetection took {ms:.0f} ms on CPU")


def run_server(settings: Settings, detector: PIIDetector, log: logging.Logger) -> None:
    import uvicorn

    log.info("starting server on http://%s:%d", settings.host, settings.port)
    uvicorn.run(create_app(detector), host=settings.host, port=settings.port)


def main() -> None:
    settings = Settings.from_env()

    p = argparse.ArgumentParser(prog="main.py", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="command", required=True)
    demo = sub.add_parser("demo", help="run detection and redaction on one prompt")
    demo.add_argument("--file", help="one file name in test_prompt/ (default: built-in example)")
    sub.add_parser("server", help=f"run the HTTP service (HOST/PORT, default {settings.host}:{settings.port})")
    args = p.parse_args()

    log = setup_logging(settings.log_level)
    try:
        detector = PIIDetector(settings.spacy)  # load once; slow
    except (Exception, SystemExit):  # spaCy exits (SystemExit) when it cannot download a missing model
        # Usually the spaCy model isn't installed (check SPACY_MODEL) or a recognizer failed to build.
        log.exception("could not load the PII detector with spaCy model %s", settings.spacy.model)
        sys.exit(1)
    if args.command == "demo":
        run_demo(detector, args.file)
    else:
        run_server(settings, detector, log)


if __name__ == "__main__":
    main()
