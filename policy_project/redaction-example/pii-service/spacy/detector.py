"""PresidioDetector: loads Presidio once and turns its results into findings."""
import logging
import time
from collections import Counter

from presidio_analyzer import AnalyzerEngine
from presidio_analyzer.nlp_engine import NlpEngineProvider

from config import SpacyConfig
from recognizers import extra_recognizers

log = logging.getLogger("pii_service")


class PIIDetector:
    """Wraps the Presidio analyzer: loads the engine once, then detects PII."""

    def __init__(self, spacy: SpacyConfig):
        self.spacy = spacy
        self.spacy_model = spacy.model
        log.info("loading presidio with spaCy model %s ...", spacy.model)
        t = time.perf_counter()
        nlp_engine = NlpEngineProvider(nlp_configuration={
            "nlp_engine_name": "spacy",
            "models": [{"lang_code": "en", "model_name": spacy.model}],
            "ner_model_configuration": {"labels_to_ignore": spacy.ignored_labels},
        }).create_engine()
        self.analyzer = AnalyzerEngine(nlp_engine=nlp_engine, supported_languages=["en"])
        for recognizer in extra_recognizers():
            self.analyzer.registry.add_recognizer(recognizer)
        log.info("presidio loaded in %.1fs", time.perf_counter() - t)

    def detect(self, text: str) -> list[dict]:
        """Run Presidio over the text and return non-overlapping findings."""
        t = time.perf_counter()
        results = self.analyzer.analyze(text=text, language="en", entities=list(self.spacy.labels))
        findings = [
            to_finding(text, self.spacy.labels[r.entity_type], r.entity_type, r.score, r.start, r.end)
            for r in results
            if r.score >= self.spacy.threshold(r.entity_type)
        ]
        kept = drop_overlaps(findings)
        log.info("  detect: %d raw, %d above threshold -> %d findings %s in %.0fms",
                 len(results), len(findings), len(kept),
                 dict(Counter(f["type"] for f in kept)), (time.perf_counter() - t) * 1000)
        for f in kept:
            log.debug("    %-18s %-20s score=%.3f chars=%d-%d bytes=%d-%d", f["type"], f["label"],
                      f["score"], f["start"], f["end"], f["byte_start"], f["byte_end"])
        return kept


def to_finding(text: str, finding_type: str, label: str, score: float, start: int, end: int) -> dict:
    """Build a finding with character AND UTF-8 byte offsets (the Go gateway uses bytes)."""
    return {
        "type": finding_type,
        "label": label,
        "score": round(score, 3),
        "start": start,
        "end": end,
        "byte_start": len(text[:start].encode("utf-8")),
        "byte_end": len(text[:end].encode("utf-8")),
    }


def drop_overlaps(findings: list[dict]) -> list[dict]:
    """Keep the highest-scoring finding when two spans overlap (longest on ties)."""
    kept: list[dict] = []
    for f in sorted(findings, key=lambda f: (-f["score"], f["start"] - f["end"])):
        if all(f["end"] <= k["start"] or f["start"] >= k["end"] for k in kept):
            kept.append(f)
    return sorted(kept, key=lambda f: f["start"])
