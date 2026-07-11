#!/usr/bin/env python
"""Test Antigravity Content role handling without routing through CLIProxyAPI.

Example:
    python scripts/direct_antigravity_role_test.py \
        --credential /path/to/antigravity-auth.json \
        --capacity-retries 5
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any
from urllib import error, parse, request

DAILY_BASE_URL = "https://daily-cloudcode-pa.googleapis.com"
TOKEN_URL = "https://oauth2.googleapis.com/token"
CLIENT_ID = os.getenv(
    "ANTIGRAVITY_CLIENT_ID",
    "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
)
CLIENT_SECRET = os.getenv(
    "ANTIGRAVITY_CLIENT_SECRET",
    "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",
)
DEFAULT_USER_AGENT = "antigravity/hub/2.2.1 darwin/arm64"
DEFAULT_MODELS = ("gemini-pro-agent", "claude-opus-4-6-thinking")


def test_cases() -> dict[str, list[dict[str, Any]]]:
    reminder_one = "<system-reminder>\nString mid-conversation rule\n</system-reminder>"
    reminder_two = "<system-reminder>\nArray mid-conversation rule\n</system-reminder>"
    return {
        "single_user_baseline": [
            {
                "role": "user",
                "parts": [{"text": "Output exactly OK and nothing else."}],
            }
        ],
        "consecutive_user_unmerged": [
            {"role": "user", "parts": [{"text": "Hello"}]},
            {"role": "user", "parts": [{"text": reminder_one}]},
            {
                "role": "user",
                "parts": [
                    {"text": reminder_two},
                    {"text": "Output exactly OK and nothing else."},
                ],
            },
        ],
        "consecutive_user_merged_parts": [
            {
                "role": "user",
                "parts": [
                    {"text": "Hello"},
                    {"text": reminder_one},
                    {"text": reminder_two},
                    {"text": "Output exactly OK and nothing else."},
                ],
            }
        ],
        "alternating_roles_control": [
            {"role": "user", "parts": [{"text": "Say READY."}]},
            {"role": "model", "parts": [{"text": "READY"}]},
            {
                "role": "user",
                "parts": [{"text": "Output exactly OK and nothing else."}],
            },
        ],
        "consecutive_model_unmerged": [
            {"role": "user", "parts": [{"text": "Say READY."}]},
            {"role": "model", "parts": [{"text": "READY"}]},
            {"role": "model", "parts": [{"text": "STILL READY"}]},
            {
                "role": "user",
                "parts": [{"text": "Output exactly OK and nothing else."}],
            },
        ],
    }


def load_credential(path: Path) -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8-sig"))
    if not isinstance(data, dict):
        raise ValueError("credential JSON must be an object")
    metadata = data.get("metadata")
    if isinstance(metadata, dict):
        merged = dict(data)
        merged.update(metadata)
        return merged
    return data


def post(
    url: str,
    body: bytes,
    headers: dict[str, str],
    timeout: float,
) -> tuple[int, bytes]:
    req = request.Request(url, data=body, headers=headers, method="POST")
    try:
        with request.urlopen(req, timeout=timeout) as response:
            return response.status, response.read()
    except error.HTTPError as exc:
        return exc.code, exc.read()


def safe_body(raw: bytes, limit: int = 4000) -> str:
    text = raw.decode("utf-8", errors="replace").strip()
    if len(text) > limit:
        return text[:limit] + "...<truncated>"
    return text


def refresh_access_token(refresh_token: str, timeout: float) -> str:
    form = parse.urlencode(
        {
            "client_id": CLIENT_ID,
            "client_secret": CLIENT_SECRET,
            "refresh_token": refresh_token,
            "grant_type": "refresh_token",
        }
    ).encode("ascii")
    status, raw = post(
        TOKEN_URL,
        form,
        {
            "Content-Type": "application/x-www-form-urlencoded",
            "User-Agent": "Go-http-client/2.0",
            "Connection": "close",
        },
        timeout,
    )
    if status < 200 or status >= 300:
        raise RuntimeError(f"OAuth refresh failed with HTTP {status}: {safe_body(raw)}")
    payload = json.loads(raw)
    token = str(payload.get("access_token", "")).strip()
    if not token:
        raise RuntimeError("OAuth refresh response did not contain access_token")
    return token


def error_reasons(error_payload: dict[str, Any]) -> list[str]:
    reasons: list[str] = []
    details = error_payload.get("details")
    if not isinstance(details, list):
        return reasons
    for detail in details:
        if not isinstance(detail, dict):
            continue
        reason = detail.get("reason")
        if isinstance(reason, str) and reason:
            reasons.append(reason)
    return reasons


def response_summary(status: int, raw: bytes) -> dict[str, Any]:
    summary: dict[str, Any] = {"http_status": status}
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        summary["body"] = safe_body(raw)
        return summary

    if not isinstance(payload, dict):
        summary["body"] = payload
        return summary

    error_payload = payload.get("error")
    if isinstance(error_payload, dict):
        summary["error_status"] = error_payload.get("status")
        summary["error_message"] = error_payload.get("message")
        reasons = error_reasons(error_payload)
        if reasons:
            summary["error_reasons"] = reasons
        return summary

    response_payload = payload.get("response")
    if not isinstance(response_payload, dict):
        response_payload = payload

    usage = response_payload.get("usageMetadata")
    if isinstance(usage, dict):
        summary["usage"] = {
            key: usage.get(key)
            for key in (
                "promptTokenCount",
                "cachedContentTokenCount",
                "candidatesTokenCount",
                "thoughtsTokenCount",
                "totalTokenCount",
            )
            if key in usage
        }

    candidates = response_payload.get("candidates")
    if isinstance(candidates, list) and candidates:
        first = candidates[0]
        if isinstance(first, dict):
            summary["finish_reason"] = first.get("finishReason")
            content = first.get("content")
            if isinstance(content, dict):
                parts = content.get("parts")
                texts: list[str] = []
                if isinstance(parts, list):
                    for part in parts:
                        if isinstance(part, dict) and isinstance(part.get("text"), str):
                            texts.append(part["text"])
                if texts:
                    summary["text"] = "".join(texts)[:1000]
    return summary


def build_payload(model: str, project_id: str, contents: list[dict[str, Any]]) -> bytes:
    generation_config: dict[str, Any] = {"temperature": 0}
    if "claude" in model.lower():
        generation_config["maxOutputTokens"] = 256

    payload = {
        "project": project_id,
        "model": model,
        "userAgent": "antigravity",
        "requestType": "agent",
        "requestId": "agent-" + str(uuid.uuid4()),
        "request": {
            "sessionId": "-"
            + str(
                uuid.uuid4().int % 9_000_000_000_000_000_000
                + 1_000_000_000_000_000_000
            ),
            "systemInstruction": {
                "parts": [
                    {
                        "text": "You are validating message protocol behavior. "
                        "Follow the latest instruction."
                    }
                ]
            },
            "contents": contents,
            "generationConfig": generation_config,
        },
    }
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def run_case(
    base_url: str,
    model: str,
    case_name: str,
    contents: list[dict[str, Any]],
    project_id: str,
    token: str,
    refresh_token: str,
    user_agent: str,
    timeout: float,
) -> tuple[dict[str, Any], str]:
    url = base_url.rstrip("/") + "/v1internal:generateContent"
    body = build_payload(model, project_id, contents)
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "User-Agent": user_agent,
        "Connection": "close",
    }
    status, raw = post(url, body, headers, timeout)
    if status == 401 and refresh_token:
        token = refresh_access_token(refresh_token, timeout)
        headers["Authorization"] = "Bearer " + token
        status, raw = post(url, body, headers, timeout)

    result = {
        "model": model,
        "case": case_name,
        **response_summary(status, raw),
    }
    return result, token


def is_capacity_exhausted(result: dict[str, Any]) -> bool:
    return (
        result.get("http_status") == 503
        and "MODEL_CAPACITY_EXHAUSTED" in result.get("error_reasons", [])
    )


def parse_args(cases: dict[str, list[dict[str, Any]]]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Test Antigravity same-role Content handling without CLIProxyAPI"
    )
    parser.add_argument("--credential", required=True, type=Path)
    parser.add_argument("--base-url", default="")
    parser.add_argument("--models", nargs="+", default=list(DEFAULT_MODELS))
    parser.add_argument("--cases", nargs="+", choices=sorted(cases), default=list(cases))
    parser.add_argument("--capacity-retries", type=int, default=0)
    parser.add_argument("--timeout", type=float, default=120.0)
    parser.add_argument("--delay", type=float, default=1.0)
    return parser.parse_args()


def main() -> int:
    cases = test_cases()
    args = parse_args(cases)
    credential = load_credential(args.credential)
    access_token = str(credential.get("access_token", "")).strip()
    refresh_token = str(credential.get("refresh_token", "")).strip()
    project_id = str(
        credential.get("project_id")
        or credential.get("projectId")
        or credential.get("project")
        or ""
    ).strip()
    if not project_id:
        raise ValueError("credential JSON does not contain project_id")
    if not access_token:
        if not refresh_token:
            raise ValueError("credential JSON contains neither access_token nor refresh_token")
        access_token = refresh_access_token(refresh_token, args.timeout)

    configured_base_url = str(credential.get("base_url", "")).strip()
    base_url = args.base_url.strip() or configured_base_url or DAILY_BASE_URL
    user_agent = str(credential.get("user_agent", "")).strip() or DEFAULT_USER_AGENT

    results: list[dict[str, Any]] = []
    max_attempts = max(args.capacity_retries, 0) + 1
    for model in args.models:
        for case_name in args.cases:
            for attempt in range(1, max_attempts + 1):
                result, access_token = run_case(
                    base_url,
                    model,
                    case_name,
                    cases[case_name],
                    project_id,
                    access_token,
                    refresh_token,
                    user_agent,
                    args.timeout,
                )
                result["attempt"] = attempt
                results.append(result)
                print(json.dumps(result, ensure_ascii=False), flush=True)
                if not is_capacity_exhausted(result):
                    break
                time.sleep(max(args.delay, 0))
            time.sleep(max(args.delay, 0))

    print("\nSummary:")
    print(json.dumps(results, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
