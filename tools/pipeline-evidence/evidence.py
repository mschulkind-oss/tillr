"""Pipeline evidence records: the shared, versioned shape and its one validator.

A **pipeline evidence record** is one line of JSON describing one step or event in
the development pipeline -- a gate check, a landing stage, a dispatch, a verdict --
in a shape every component of that pipeline can write and any component can read.
`docs/pi/pipeline-evidence.md` is the format's prose; `schema/evidence-v1.json`
is the machine-readable contract; this module is the validator and the reader the
tests and the report share.

The validator is a JSON Schema evaluator for the keyword subset this repository's
schema uses, and nothing more. That is deliberate: the schema file is the single
source of truth, so a rule lives in the schema and is enforced here, rather than
being written twice and drifting. A schema that grows a keyword this module does
not implement fails loudly (`UnsupportedKeyword`) instead of silently ignoring it.
"""

from __future__ import annotations

import json
import re
from pathlib import Path

SCHEMA_VERSION = 1
HERE = Path(__file__).resolve().parent
SCHEMA_PATH = HERE / "schema" / "evidence-v1.json"


class UnsupportedKeyword(Exception):
    """The schema uses a JSON Schema keyword this validator does not implement."""


class MalformedLine(Exception):
    """A line in an evidence file is not a JSON object."""

    def __init__(self, path: Path, line: int, reason: str):
        self.path = path
        self.line = line
        self.reason = reason
        super().__init__(f"{path}:{line}: not an evidence record: {reason}")


class UnknownVersion(Exception):
    """A record declares a format version this consumer does not know.

    Refusing is the point of versioning: guessing at a shape a newer producer
    writes would report numbers that are silently wrong.
    """

    def __init__(self, path: Path, line: int, version: object, known: int = SCHEMA_VERSION):
        self.path = path
        self.line = line
        self.version = version
        self.known = known
        super().__init__(
            f"{path}:{line}: evidence record version {version!r} is not version {known}; "
            f"refusing rather than guessing (this consumer reads version {known} only)")


class InvalidRecord(Exception):
    """A record does not conform to the schema."""

    def __init__(self, findings: list[str], where: str = ""):
        self.findings = findings
        prefix = f"{where}: " if where else ""
        super().__init__(prefix + "; ".join(findings))


_SCHEMA_CACHE: dict[Path, dict] = {}


def load_schema(path: Path | None = None) -> dict:
    resolved = Path(path or SCHEMA_PATH)
    if resolved not in _SCHEMA_CACHE:
        _SCHEMA_CACHE[resolved] = json.loads(resolved.read_text(encoding="utf-8"))
    return _SCHEMA_CACHE[resolved]


# ── the validator ──────────────────────────────────────────────────────────────
# Keyword subset, exactly the keywords evidence-v1.json uses. An unknown keyword is
# an error rather than ignored: a rule nobody enforces is worse than no rule, because
# the schema then reads as though it were checked.

_TYPES = {
    "object": lambda value: isinstance(value, dict),
    "array": lambda value: isinstance(value, list),
    "string": lambda value: isinstance(value, str),
    "integer": lambda value: isinstance(value, int) and not isinstance(value, bool),
    "number": lambda value: isinstance(value, (int, float)) and not isinstance(value, bool),
    "boolean": lambda value: isinstance(value, bool),
    "null": lambda value: value is None,
}

_ANNOTATIONS = frozenset({"$schema", "$id", "title", "description"})
_KEYWORDS = frozenset({
    "type", "const", "enum", "required", "properties", "additionalProperties",
    "pattern", "minLength", "maxLength", "minimum", "maximum", "items",
    "allOf", "anyOf",
}) | _ANNOTATIONS


def _type_ok(value, expected) -> bool:
    names = expected if isinstance(expected, list) else [expected]
    return any(_TYPES[name](value) for name in names)


def _describe(value) -> str:
    for name in ("null", "boolean", "integer", "string", "array", "object"):
        if _TYPES[name](value):
            return name
    return type(value).__name__


def _check(value, schema: dict, path: str, findings: list[str]) -> None:
    for keyword in schema:
        if keyword not in _KEYWORDS:
            raise UnsupportedKeyword(f"{path or '<root>'}: schema keyword {keyword!r} is not implemented")

    if "type" in schema and not _type_ok(value, schema["type"]):
        expected = schema["type"]
        expected_text = " or ".join(expected) if isinstance(expected, list) else expected
        findings.append(f"{path or '<root>'}: expected {expected_text}, got {_describe(value)}")
        return

    if "const" in schema and value != schema["const"]:
        findings.append(f"{path or '<root>'}: expected {schema['const']!r}, got {value!r}")

    if "enum" in schema and value not in schema["enum"]:
        findings.append(f"{path or '<root>'}: {value!r} is not one of {schema['enum']}")

    if isinstance(value, str):
        if "minLength" in schema and len(value) < schema["minLength"]:
            findings.append(f"{path}: shorter than {schema['minLength']} character(s)")
        if "maxLength" in schema and len(value) > schema["maxLength"]:
            findings.append(f"{path}: longer than {schema['maxLength']} character(s)")
        if "pattern" in schema:
            if not re.search(schema["pattern"], value):
                findings.append(f"{path}: {value!r} does not match {schema['pattern']}")

    if _TYPES["number"](value):
        if "minimum" in schema and value < schema["minimum"]:
            findings.append(f"{path}: {value} is below the minimum {schema['minimum']}")
        if "maximum" in schema and value > schema["maximum"]:
            findings.append(f"{path}: {value} is above the maximum {schema['maximum']}")

    if isinstance(value, dict):
        for name in schema.get("required", []):
            if name not in value:
                findings.append(f"{path or '<root>'}: missing required key {name!r}")
        properties = schema.get("properties", {})
        for name, subschema in properties.items():
            if name in value:
                _check(value[name], subschema, f"{path}.{name}" if path else name, findings)
        extra = schema.get("additionalProperties", True)
        if extra is False:
            for name in value:
                if name not in properties:
                    findings.append(f"{path or '<root>'}: unknown key {name!r}")
        elif isinstance(extra, dict):
            for name, member in value.items():
                if name not in properties:
                    _check(member, extra, f"{path}.{name}" if path else name, findings)

    if isinstance(value, list) and "items" in schema:
        for index, member in enumerate(value):
            _check(member, schema["items"], f"{path}[{index}]", findings)

    for subschema in schema.get("allOf", []):
        _check(value, subschema, path, findings)

    if "anyOf" in schema:
        failures = []
        for subschema in schema["anyOf"]:
            attempt: list[str] = []
            _check(value, subschema, path, attempt)
            if not attempt:
                break
            failures.append(attempt)
        else:
            findings.append(f"{path or '<root>'}: matches none of the {len(failures)} allowed shapes")


def validate(record, schema: dict | None = None) -> list[str]:
    """Return the record's schema findings; an empty list means it conforms."""
    findings: list[str] = []
    _check(record, schema or load_schema(), "", findings)
    if not findings:
        findings.extend(_cross_field(record))
    return findings


def _cross_field(record: dict) -> list[str]:
    """Rules the schema's shape cannot state: a step cannot end before it starts.

    These are still format rules, not producer sanity: a record whose end precedes
    its start describes a duration nothing can be, and a consumer that summed it
    would report a cost that never happened.
    """
    findings = []
    started, ended = record.get("startedAt"), record.get("endedAt")
    if isinstance(started, str) and isinstance(ended, str) and ended < started:
        findings.append(f"endedAt {ended} is earlier than startedAt {started}")
    return findings


# ── reading and writing ────────────────────────────────────────────────────────


def encode(record: dict) -> str:
    """One record as one line, keys sorted so two runs of a producer diff cleanly."""
    return json.dumps(record, sort_keys=True, separators=(", ", ": "))


def read_records(path: Path, known_version: int = SCHEMA_VERSION) -> list[dict]:
    """Read a JSONL evidence file, refusing anything this consumer cannot read.

    Blank lines are skipped: an append-only file that ends in a newline has one, and
    a reader that refused it would refuse every file it was written.

    A line with no `version` key is refused rather than treated as an old record.
    Version 1 is the format's first version, so there is no earlier shape to
    interpret, and a file with unversioned lines is a file some other tool wrote.
    """
    records: list[dict] = []
    try:
        text = Path(path).read_text(encoding="utf-8", errors="replace")
    except FileNotFoundError:
        return records
    for number, line in enumerate(text.splitlines(), start=1):
        if not line.strip():
            continue
        try:
            record = json.loads(line)
        except ValueError as error:
            raise MalformedLine(Path(path), number, f"invalid JSON ({error})") from None
        if not isinstance(record, dict):
            raise MalformedLine(Path(path), number, f"a JSON {_describe(record)}, not an object")
        version = record.get("version")
        if version != known_version:
            raise UnknownVersion(Path(path), number, version, known_version)
        findings = validate(record)
        if findings:
            raise InvalidRecord(findings, where=f"{path}:{number}")
        records.append(record)
    return records
