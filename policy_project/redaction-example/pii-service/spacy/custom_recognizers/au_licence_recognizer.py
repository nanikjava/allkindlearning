from typing import List, Optional

from presidio_analyzer import Pattern, PatternRecognizer


class AuDriverLicenceRecognizer(PatternRecognizer):
    """
    Recognizes Australian driver licence numbers using regex and context words.

    Each state issues its own format and none has a checksum:
    NSW/VIC/QLD/WA/NT/ACT are 6-10 digits, SA and TAS may start with
    one or two letters (e.g. S12345, A12345). The newer card numbers on
    the licence are also up to 10 alphanumeric characters.

    Because a bare 6-10 digit number could be almost anything, the base
    scores are low; Presidio's context enhancer only lifts a match above the
    service threshold when a word like "licence" or "driver" appears nearby.

    :param patterns: List of patterns to be used by this recognizer
    :param context: List of context words to increase confidence in detection
    :param supported_language: Language this recognizer supports
    :param supported_entity: The entity this recognizer can detect
    """

    COUNTRY_CODE = "au"

    PATTERNS = [
        Pattern(
            "Australian Driver Licence (state prefix)",
            r"\b(?:NSW|VIC|QLD|WA|SA|TAS|NT|ACT)\s?[A-Z]{0,2}\d{5,10}\b",
            0.4,
        ),
        Pattern(
            "Australian Driver Licence (letter prefix)",
            r"\b[A-Z]{1,2}\d{5,9}\b",
            0.2,
        ),
        Pattern(
            "Australian Driver Licence (digits)",
            r"\b\d{6,10}\b",
            0.2,
        ),
    ]

    CONTEXT = [
        "licence",
        "license",
        "driver",
        "drivers",
        "driving",
        "dl",
    ]

    def __init__(
        self,
        patterns: Optional[List[Pattern]] = None,
        context: Optional[List[str]] = None,
        supported_language: str = "en",
        supported_entity: str = "AU_DRIVER_LICENCE",
        name: Optional[str] = None,
    ):
        patterns = patterns if patterns else self.PATTERNS
        context = context if context else self.CONTEXT
        super().__init__(
            supported_entity=supported_entity,
            patterns=patterns,
            context=context,
            supported_language=supported_language,
            name=name,
        )
