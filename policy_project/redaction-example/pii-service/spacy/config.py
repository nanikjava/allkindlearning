"""
Settings for the PII service: which Presidio entities we look for, how
confident a finding must be, and the environment variables main.py reads.
"""
import os
from dataclasses import dataclass, field


# Presidio entity types we ask for, mapped to the finding types the gateway's
# policy already understands. Asking for fewer entities is faster.
_LABELS = {
    "PERSON": "pii.name",
    "EMAIL_ADDRESS": "pii.email",
    "PHONE_NUMBER": "pii.phone",
    "LOCATION": "pii.address",
    "US_SSN": "pii.national_id",
    "US_PASSPORT": "pii.national_id",
    "US_DRIVER_LICENSE": "pii.national_id",
    "US_ITIN": "pii.national_id",
    "US_MBI": "pii.national_id",
    "US_NPI": "pii.national_id",
    "UK_NHS": "pii.national_id",
    "UK_NINO": "pii.national_id",
    "UK_PASSPORT": "pii.national_id",
    "UK_DRIVING_LICENCE": "pii.national_id",
    "UK_VEHICLE_REGISTRATION": "pii.national_id",
    "UK_POSTCODE": "pii.address",
    "AU_TFN": "pii.national_id",
    "AU_MEDICARE": "pii.national_id",
    "AU_DRIVER_LICENCE": "pii.national_id",
    "AU_ABN": "pii.national_id",
    "AU_ACN": "pii.national_id",
    "IBAN_CODE": "pii.bank_account",
    "US_BANK_NUMBER": "pii.bank_account",
    "ABA_ROUTING_NUMBER": "pii.bank_account",
    "CREDIT_CARD": "pci.card_number",
    "API_KEY": "secret.api_key",
}

# Per-entity minimum confidence. Names are noisier, secrets are costly to miss.
_THRESHOLDS = {"PERSON": 0.6, "LOCATION": 0.6, "PHONE_NUMBER": 0.4, "API_KEY": 0.4}

# spaCy labels we don't map to a finding type (FAC, ORG, dates, numbers...).
_IGNORED_SPACY_LABELS = [
    "FAC", "ORG", "ORGANIZATION", "NORP", "DATE", "TIME", "CARDINAL", "ORDINAL",
    "QUANTITY", "MONEY", "PERCENT", "PRODUCT", "EVENT", "WORK_OF_ART", "LAW", "LANGUAGE",
]



@dataclass(frozen=True)
class SpacyConfig:
    """Which spaCy model Presidio loads, which entities we ask for and how confident they must be."""
    model: str = "en_core_web_lg"  # or en_core_web_md: smaller, faster, less accurate
    labels: dict[str, str] = field(default_factory=lambda: dict(_LABELS))
    thresholds: dict[str, float] = field(default_factory=lambda: dict(_THRESHOLDS))
    default_threshold: float = 0.5
    ignored_labels: list[str] = field(default_factory=lambda: list(_IGNORED_SPACY_LABELS))

    def threshold(self, entity: str) -> float:
        return self.thresholds.get(entity, self.default_threshold)


@dataclass(frozen=True)
class Settings:
    """Runtime settings, read from environment variables."""
    spacy: SpacyConfig
    # Use HOST=0.0.0.0 to accept connections from other machines (e.g. behind a load balancer).
    host: str
    port: int
    log_level: str

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            spacy=SpacyConfig(model=os.environ.get("SPACY_MODEL", "en_core_web_lg")),
            host=os.environ.get("HOST", "127.0.0.1"),
            port=int(os.environ.get("PORT", "3002")),
            log_level=os.environ.get("LOG_LEVEL", "INFO").upper(),
        )
