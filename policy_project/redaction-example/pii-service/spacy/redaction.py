"""Replaces findings with placeholders. Doesn't use Presidio, so any engine can reuse it."""


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
