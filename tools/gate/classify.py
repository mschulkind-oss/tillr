"""Transport raw check observations to the installed classifier, never a tree binary."""
import json
from subprocess import run as run_command


OUTCOMES = ("pass", "fail", "unknown", "unavailable", "disabled")


class ClassificationError(ValueError):
    """The installed face did not supply a usable classification."""


def _invoke_classify(argv, observations):
    try:
        answered = run_command(argv, input=json.dumps(observations),
                               capture_output=True, encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise ClassificationError(f"tillr-gate classify: {error}") from error
    if answered.returncode:
        raise ClassificationError(f"tillr-gate classify exited {answered.returncode}: {answered.stderr.strip()}")
    try:
        return json.loads(answered.stdout)
    except ValueError as error:
        raise ClassificationError(f"tillr-gate classify returned invalid JSON: {error}") from error


def _validate_answers(observations, results):
    if not isinstance(results, list) or len(results) != len(observations):
        raise ClassificationError("tillr-gate classify did not return one answer per observation")
    for index, (observation, result) in enumerate(zip(observations, results)):
        # The wire order is positional. The face echoes the raw word/detail when
        # present, so check those too; exit-only observations have no echo.
        if (not isinstance(result, dict)
                or result.get("outcome") not in OUTCOMES
                or not isinstance(result.get("detail"), str) or not result["detail"]
                or result.get("raw_word", "") != observation.get("word", "")
                or result.get("raw_detail", "") != observation.get("detail", "")):
            raise ClassificationError(f"tillr-gate classify returned an invalid answer at index {index}")
    return results


def classify(observations):
    results = _invoke_classify(["tillr-gate", "classify"], observations)
    return _validate_answers(observations, results)


def classify_aggregate(order, observations):
    if not isinstance(order, str) or not order:
        raise ClassificationError("tillr-gate classify: aggregate order must be a non-empty string")
    payload = _invoke_classify(["tillr-gate", "classify", "--aggregate", order], observations)
    if not isinstance(payload, dict):
        raise ClassificationError("tillr-gate classify did not return an aggregate object")
    _validate_answers(observations, payload.get("checks"))
    run = payload.get("run")
    if (not isinstance(run, dict)
            or run.get("outcome") not in OUTCOMES
            or not isinstance(run.get("detail"), str) or not run["detail"]
            or type(run.get("exit_code")) is not int):
        raise ClassificationError("tillr-gate classify returned an invalid run aggregate")
    return payload
