"""Transport raw check observations to the installed classifier, never a tree binary."""
import json
from subprocess import run as run_command


class ClassificationError(ValueError):
    """The installed face did not supply a usable classification."""


def classify(observations):
    try:
        answered = run_command(["tillr-gate", "classify"], input=json.dumps(observations),
                               capture_output=True, encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ClassificationError(f"tillr-gate classify: {error}") from error
    if answered.returncode:
        raise ClassificationError(f"tillr-gate classify exited {answered.returncode}: {answered.stderr.strip()}")
    try:
        results = json.loads(answered.stdout)
    except ValueError as error:
        raise ClassificationError(f"tillr-gate classify returned invalid JSON: {error}") from error
    if not isinstance(results, list) or len(results) != len(observations):
        raise ClassificationError("tillr-gate classify did not return one answer per observation")
    for index, (observation, result) in enumerate(zip(observations, results)):
        # The wire order is positional. The face echoes the raw word/detail when
        # present, so check those too; exit-only observations have no echo.
        if (not isinstance(result, dict)
                or result.get("outcome") not in ("pass", "fail", "unknown", "unavailable", "disabled")
                or not isinstance(result.get("detail"), str) or not result["detail"]
                or result.get("raw_word", "") != observation.get("word", "")
                or result.get("raw_detail", "") != observation.get("detail", "")):
            raise ClassificationError(f"tillr-gate classify returned an invalid answer at index {index}")
    return results
