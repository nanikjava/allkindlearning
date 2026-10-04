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
uv run python main.py demo           # run the example prompt on the command line
uv run python main.py server         # start the HTTP service on 127.0.0.1:3002
uv run uvicorn main:create_app --factory --port 3002  # same thing, using the uvicorn CLI
```

Environment variables:

| Variable      | Default          | Purpose                                                           |
| ------------- | ---------------- | ----------------------------------------------------------------- |
| `SPACY_MODEL` | `en_core_web_lg` | spaCy model Presidio uses for names and locations                 |
| `LOG_LEVEL`   | `INFO`           | `DEBUG` logs every finding (type, score, offsets; never the text) |
| `HOST`        | `127.0.0.1`      | Address `server` listens on; `0.0.0.0` to accept remote callers   |
| `PORT`        | `3002`           | Port `server` listens on                                          |

## Files

| File                       | What it is                                                         |
| -------------------------- | ------------------------------------------------------------------ |
| `main.py`                  | Entry point: reads settings, sets up logging, FastAPI app and CLI  |
| `config.py`                | `Settings` and `SpacyConfig`: env vars, labels, thresholds         |
| `detector.py`              | `PresidioDetector`, plus `to_finding` and `drop_overlaps`          |
| `recognizers.py`           | Recognizers added on top of Presidio's defaults                    |
| `au_licence_recognizer.py` | Custom Australian driver licence recognizer                        |
| `redaction.py`             | `redact()`: placeholders and vault; no Presidio dependency         |
| `load_testing.py`          | Sends many requests at once to `/detect` and reports latency       |
| `pyproject.toml`           | Dependencies, including the spaCy model wheel                      |

## How the code is laid out

1. **`config.py`**: `Settings` (host, port, log level, and a `SpacyConfig`)
   and `Settings.from_env()`, which reads the environment variables.
   `SpacyConfig` holds the model name and the detection settings below; their
   defaults are the `_LABELS`, `_THRESHOLDS` and `_IGNORED_SPACY_LABELS` lists.
   - `labels` maps Presidio entity types such as `PERSON` and `IBAN_CODE` to the
     finding types the gateway policy understands, such as `pii.name` and
     `pii.bank_account`. Presidio only looks for the entities listed here.
   - `thresholds` and `default_threshold` set the lowest confidence score kept
     for each entity type.
   - `ignored_labels` lists spaCy labels that don't map to a finding type.
2. **`recognizers.py`**: `extra_recognizers()` returns the recognizers added to
   Presidio: a custom API-key recognizer (OpenAI-style `sk-…` and AWS `AKIA…`
   keys, the same patterns as `detect.go`), the Australian TFN and Medicare
   recognizers (Presidio includes them but doesn't load them by default), and
   our Australian driver licence recognizer.
3. **`detector.py`**: everything that uses Presidio.
   - `PresidioDetector.__init__` loads the spaCy engine, builds the
     `AnalyzerEngine` and registers `extra_recognizers()`.
   - `detect(text)` runs the analyzer and drops low-score results. It returns
     each finding with both **character** and **UTF-8 byte** offsets (built by
     `to_finding`), because the Go gateway works with byte offsets.
   - `drop_overlaps(findings)`: when two findings overlap, keeps the one with
     the higher score (the longer one if the scores are equal).
4. **`redaction.py`**: `redact(text, findings)` replaces each value with a
   numbered placeholder such as `[NAME_1]`. The same value always gets the same
   placeholder. It also returns a vault (`{placeholder: original}`) so the
   original values can be put back later.
5. **`main.py`**: `main()` reads `Settings.from_env()`, sets up logging and
   creates one `PresidioDetector` from `settings.spacy`; `create_app(detector)`
   builds the FastAPI app around it. Importing the module loads nothing. It serves the endpoints below, and has the `demo` and `server` commands.

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
add a recognizer to `extra_recognizers()` in `recognizers.py`.

## References

- spaCy models (`en_core_web_lg`): https://spacy.io/models/en
- FastAPI: https://fastapi.tiangolo.com/
- Presidio new home - https://presidio.dataprivacystack.org/
