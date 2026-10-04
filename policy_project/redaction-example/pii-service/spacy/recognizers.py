"""
Recognizers we add on top of Presidio's defaults. To detect a new entity,
add its recognizer to extra_recognizers() and its label to config._LABELS.
"""
from presidio_analyzer import EntityRecognizer, Pattern, PatternRecognizer
from presidio_analyzer.predefined_recognizers import (
    AbaRoutingRecognizer,
    AuAbnRecognizer,
    AuAcnRecognizer,
    AuMedicareRecognizer,
    AuTfnRecognizer,
    UkDrivingLicenceRecognizer,
    UkNinoRecognizer,
    UkPassportRecognizer,
    UkPostcodeRecognizer,
    UkVehicleRegistrationRecognizer,
    UsMbiRecognizer,
    UsNpiRecognizer,
)

from au_licence_recognizer import AuDriverLicenceRecognizer


def api_key_recognizer() -> PatternRecognizer:
    # Presidio has no built-in API key recognizer; same patterns as detect.go.
    return PatternRecognizer(
        supported_entity="API_KEY",
        patterns=[
            Pattern("openai_style", r"\bsk-[A-Za-z0-9_-]{20,}\b", 0.8),
            Pattern("aws_access_key", r"\bAKIA[0-9A-Z]{16}\b", 0.9),
        ],
    )


def extra_recognizers() -> list[EntityRecognizer]:
    return [
        api_key_recognizer(),
        # Presidio ships these country recognizers but doesn't load them by default.
        AuTfnRecognizer(),
        AuMedicareRecognizer(),
        AuAbnRecognizer(),
        AuAcnRecognizer(),
        AuDriverLicenceRecognizer(),  # ours, not Presidio's
        UkNinoRecognizer(),
        UkPassportRecognizer(),
        UkDrivingLicenceRecognizer(),
        UkPostcodeRecognizer(),
        UkVehicleRegistrationRecognizer(),
        AbaRoutingRecognizer(),
        UsMbiRecognizer(),
        UsNpiRecognizer(),
    ]
