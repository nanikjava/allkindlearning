# PII service (Presidio)

A small HTTP service that the Go gateway calls to find and redact PII
(personally identifiable information) in prompts. It uses
[Microsoft Presidio](https://microsoft.github.io/presidio/) and a spaCy model,
and runs on CPU only.

It has the same API as the `gliner2` service next to it, so the gateway can use
either one.

## Quick start

```bash
uv sync                                     # install dependencies
uv run python pii_service.py demo           # run the example prompt on the command line
uv run python pii_service.py server         # start the HTTP service on 127.0.0.1:3002
uv run uvicorn pii_service:app --port 3002  # same thing, using the uvicorn CLI
```

Environment variables:

| Variable      | Default          | Purpose                                                           |
| ------------- | ---------------- | ----------------------------------------------------------------- |
| `SPACY_MODEL` | `en_core_web_lg` | spaCy model Presidio uses for names and locations                 |
| `LOG_LEVEL`   | `INFO`           | `DEBUG` logs every finding (type, score, offsets; never the text) |

## Files

| File              | What it is                                                     |
| ----------------- | -------------------------------------------------------------- |
| `pii_service.py`  | The service: detection, redaction, HTTP endpoints and the demo |
| `load_testing.py` | Sends many requests at once to `/detect` and reports latency   |
| `pyproject.toml`  | Dependencies, including the spaCy model wheel                  |

## How `pii_service.py` is laid out

1. **Settings**
   - `LABELS` maps Presidio entity types such as `PERSON` and `IBAN_CODE` to the
     finding types the gateway policy understands, such as `pii.name` and
     `pii.bank_account`. Presidio only looks for the entities listed here.
   - `THRESHOLDS` and `DEFAULT_THRESHOLD` set the lowest confidence score kept
     for each entity type.
2. **`PresidioDetector` class**: everything that uses Presidio.
   - `__init__` loads the spaCy engine and builds the `AnalyzerEngine`. It also
     adds three recognizers: a custom API-key recognizer (OpenAI-style `sk-…`
     and AWS `AKIA…` keys, the same patterns as `detect.go`) and the Australian
     TFN and Medicare recognizers. Presidio includes those two but doesn't load
     them by default.
   - `detect(text)` runs the analyzer and drops low-score results. It returns
     each finding with both **character** and **UTF-8 byte** offsets, because
     the Go gateway works with byte offsets.
3. **Helpers that don't use Presidio**, so another engine could reuse them:
   - `drop_overlaps(findings)`: when two findings overlap, keeps the one with
     the higher score (the longer one if the scores are equal).
   - `redact(text, findings)`: replaces each value with a numbered placeholder
     such as `[NAME_1]`. The same value always gets the same placeholder. It
     also returns a vault (`{placeholder: original}`) so the original values
     can be put back later.
4. **HTTP app (FastAPI)**: creates one `PresidioDetector` when the file loads
   and serves the endpoints below.
5. **Command-line entry points**: `demo` and `server`.

## API

| Method | Path      | Request           | Response                                               |
| ------ | --------- | ----------------- | ------------------------------------------------------ |
| GET    | `/health` | none              | `{"status": "ok", "engine": "presidio", "model": ...}` |
| POST   | `/detect` | `{"text": "..."}` | `{"findings": [...]}`                                  |
| POST   | `/redact` | `{"text": "..."}` | `{"text": "<redacted>", "findings": [...]}`            |

`/redact` never returns the vault; it stays on the server.

Example finding:

```json
{
  "type": "pii.email",
  "label": "EMAIL_ADDRESS",
  "score": 1.0,
  "start": 95,
  "end": 115,
  "byte_start": 95,
  "byte_end": 115
}
```

## How Presidio works here

Presidio's **analyzer** runs a set of _recognizers_ over the text. Each one
returns results with an entity type, a start and end offset, and a score.

- **NLP-based recognizers** use spaCy named-entity recognition to find names
  (`PERSON`) and places (`LOCATION`). spaCy labels with no matching finding
  type (`ORG`, `DATE`, `MONEY`, …) are ignored through
  `ner_model_configuration.labels_to_ignore`.
- **Pattern recognizers** use regular expressions, often with checksum checks
  and context words, to find structured values such as emails, phone numbers,
  IBANs, credit cards, SSNs and the custom `API_KEY`.

This service only uses Presidio's analyzer. Its own `redact` function does the
redaction, so placeholders are stable and the vault can be kept.
`presidio-anonymizer` is listed as a dependency but the code doesn't use it.

To support a new entity, add it to `LABELS` (and to `THRESHOLDS` if it needs
its own score cut-off). If Presidio doesn't recognize it out of the box, also
register a recognizer in `PresidioDetector.__init__`.

## References

- spaCy models (`en_core_web_lg`): https://spacy.io/models/en
- FastAPI: https://fastapi.tiangolo.com/
- Presidio new home - https://presidio.dataprivacystack.org/
