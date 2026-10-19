#!/usr/bin/env bash
#
# End-to-end smoke test for a running taskflow server.
#
# Start the server in another terminal first:
#   make run
#
# Then run:
#   make smoke
#   BASE_URL=http://localhost:9000 ./scripts/smoke.sh
#
# The script exercises all three stores:
#   - PostgreSQL: task create/list/get/update/delete
#   - MongoDB:    note create/list/update/delete
#   - Redis:      overview view counter + cache read-through
#
# Requires curl and basic shell tools. JSON is parsed with grep/sed rather than
# jq so the script works on a minimal development machine. Response shapes are
# stable and IDs always appear first in their object.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"

# Fail early with an actionable message if curl is missing.
if ! command -v curl >/dev/null 2>&1; then
    echo "error: curl is required but was not found in PATH" >&2
    exit 1
fi

# Pretty-print JSON when python3 happens to be available; otherwise show the
# compact response. Output formatting must never fail the test.
pretty() {
    if command -v python3 >/dev/null 2>&1; then
        python3 -m json.tool
    else
        cat
    fi
}

# extract_id prints the first "id" field from a compact JSON response.
extract_id() {
    grep -o '"id":"[^"]*"' | head -n 1 | cut -d '"' -f 4
}

echo "== 1. Health ======================================================="
curl --silent --show-error --fail "${BASE_URL}/healthz" | pretty

echo "== 2. Readiness (PostgreSQL + MongoDB + Redis) ====================="
curl --silent --show-error --fail "${BASE_URL}/readyz" | pretty

echo "== 3. Create a task (writes PostgreSQL, Redis, MongoDB audit) ======"
created="$(
    curl --silent --show-error --fail \
        --request POST \
        --header 'Content-Type: application/json' \
        --data '{
            "title":"Smoke-test the API",
            "description":"Created by scripts/smoke.sh",
            "priority":"high",
            "due_date":"2026-12-31",
            "timezone":"Asia/Karachi"
        }' \
        "${BASE_URL}/api/v1/tasks"
)"
printf '%s\n' "$created" | pretty

task_id="$(printf '%s' "$created" | extract_id)"
if [[ -z "$task_id" ]]; then
    echo "error: create response did not contain a task id" >&2
    exit 1
fi

echo "== 4. List tasks ==================================================="
curl --silent --show-error --fail "${BASE_URL}/api/v1/tasks?limit=5" | pretty

echo "== 5. Create a note in MongoDB ====================================="
note_created="$(
    curl --silent --show-error --fail \
        --request POST \
        --header 'Content-Type: application/json' \
        --data "{
            \"task_id\":\"${task_id}\",
            \"author\":\"smoke-test\",
            \"body\":\"This note lives in MongoDB.\",
            \"tags\":[\"smoke\",\"mongodb\"]
        }" \
        "${BASE_URL}/api/v1/notes"
)"
printf '%s\n' "$note_created" | pretty

note_id="$(printf '%s' "$note_created" | extract_id)"
if [[ -z "$note_id" ]]; then
    echo "error: create response did not contain a note id" >&2
    exit 1
fi

echo "== 6. List notes for the task ======================================"
curl --silent --show-error --fail \
    "${BASE_URL}/api/v1/notes?task_id=${task_id}" | pretty

echo "== 7. Cross-database overview (PostgreSQL + MongoDB + Redis) ======="
overview="$(
    curl --silent --show-error --fail \
        "${BASE_URL}/api/v1/tasks/${task_id}/overview"
)"
printf '%s\n' "$overview" | pretty

# The overview must actually include all three sources.
if [[ "$overview" != *'"sources":["postgresql","mongodb","redis"]'* ]]; then
    echo "error: overview did not report all three sources" >&2
    exit 1
fi

echo "== 8. Update the note =============================================="
note_updated="$(
    curl --silent --show-error --fail \
        --request PATCH \
        --header 'Content-Type: application/json' \
        --data '{"body":"Updated by the smoke test.","tags":["smoke","updated"]}' \
        "${BASE_URL}/api/v1/notes/${note_id}"
)"
printf '%s\n' "$note_updated" | pretty

echo "== 9. Mark the task as done ========================================"
updated="$(
    curl --silent --show-error --fail \
        --request PATCH \
        --header 'Content-Type: application/json' \
        --data '{"status":"done"}' \
        "${BASE_URL}/api/v1/tasks/${task_id}"
)"
printf '%s\n' "$updated" | pretty

if [[ "$updated" != *'"status":"done"'* ]]; then
    echo "error: updated task does not have status=done" >&2
    exit 1
fi

echo "== 10. Delete the note ============================================="
note_delete_status="$(
    curl --silent --show-error \
        --output /dev/null \
        --write-out '%{http_code}' \
        --request DELETE \
        "${BASE_URL}/api/v1/notes/${note_id}"
)"
if [[ "$note_delete_status" != "204" ]]; then
    echo "error: note DELETE returned HTTP ${note_delete_status}, want 204" >&2
    exit 1
fi

echo "== 11. Delete the task ============================================="
delete_status="$(
    curl --silent --show-error \
        --output /dev/null \
        --write-out '%{http_code}' \
        --request DELETE \
        "${BASE_URL}/api/v1/tasks/${task_id}"
)"
if [[ "$delete_status" != "204" ]]; then
    echo "error: task DELETE returned HTTP ${delete_status}, want 204" >&2
    exit 1
fi

echo "== 12. Confirm the deleted task returns 404 ========================"
missing_status="$(
    curl --silent --show-error \
        --output /dev/null \
        --write-out '%{http_code}' \
        "${BASE_URL}/api/v1/tasks/${task_id}"
)"
if [[ "$missing_status" != "404" ]]; then
    echo "error: GET after delete returned HTTP ${missing_status}, want 404" >&2
    exit 1
fi

echo "Smoke test passed: PostgreSQL + MongoDB + Redis all exercised."
