#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASK_DIR="$ROOT_DIR/tasks"
RESULTS_DIR="$ROOT_DIR/results"
mkdir -p "$RESULTS_DIR"

AGENT_URL="${AGENT_URL:-http://127.0.0.1:18789}"
AGENT_TOKEN="${AGENT_TOKEN:-}"
if [[ -z "$AGENT_TOKEN" && -f "$HOME/.openclio/auth.token" ]]; then
  AGENT_TOKEN="$(cat "$HOME/.openclio/auth.token")"
fi

if [[ -z "$AGENT_TOKEN" ]]; then
  echo "error: AGENT_TOKEN not set and ~/.openclio/auth.token not found"
  exit 1
fi

INPUT_PER_MTOK="${SONNET_INPUT_PER_MTOK:-3.00}"
OUTPUT_PER_MTOK="${SONNET_OUTPUT_PER_MTOK:-15.00}"

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUT_JSON="$RESULTS_DIR/agent_${TIMESTAMP}.json"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

python3 - <<'PY' > "$TMP_DIR/results_init.json"
import json
print(json.dumps({
  "benchmark": "phase2-token-efficiency",
  "generated_at": None,
  "agent_url": None,
  "pricing": {},
  "tasks": [],
  "long_session_growth": [],
  "totals": {}
}))
PY

SESSION_ID=""
TOTAL_IN=0
TOTAL_OUT=0

post_chat() {
  local message="$1"
  local session_id="$2"
  local payload
  if [[ -n "$session_id" ]]; then
    payload="$(python3 - <<PY
import json
print(json.dumps({"message": ${message@Q}, "session_id": ${session_id@Q}}))
PY
)"
  else
    payload="$(python3 - <<PY
import json
print(json.dumps({"message": ${message@Q}}))
PY
)"
  fi

  curl -sS \
    -H "Authorization: Bearer $AGENT_TOKEN" \
    -H "Content-Type: application/json" \
    -X POST "$AGENT_URL/api/v1/chat" \
    -d "$payload"
}

record_task() {
  local id="$1"
  local file="$2"
  local session_mode="$3" # fresh|shared
  local response_json

  if [[ "$id" == "06_long_session" ]]; then
    SESSION_ID=""
    local turn=0
    local growth_file="$TMP_DIR/growth.jsonl"
    : > "$growth_file"
    awk 'BEGIN{RS="--- turn ---\n"} {gsub(/^\s+|\s+$/, "", $0); if(length($0)>0) print $0}' "$file" | while IFS= read -r turn_msg; do
      turn=$((turn+1))
      response_json="$(post_chat "$turn_msg" "$SESSION_ID")"
      echo "$response_json" > "$TMP_DIR/${id}_turn${turn}.json"
      SESSION_ID="$(python3 - <<PY
import json
obj=json.loads(open("$TMP_DIR/${id}_turn${turn}.json").read())
print(obj.get("session_id", ""))
PY
)"
      python3 - <<PY >> "$growth_file"
import json
obj=json.loads(open("$TMP_DIR/${id}_turn${turn}.json").read())
usage=obj.get("usage", {})
print(json.dumps({
  "turn": $turn,
  "input_tokens": int(usage.get("input_tokens", 0)),
  "output_tokens": int(usage.get("output_tokens", 0)),
  "llm_calls": int(usage.get("llm_calls", 0))
}))
PY
    done
    return
  fi

  local prompt
  prompt="$(cat "$file")"
  if [[ "$session_mode" == "fresh" ]]; then
    SESSION_ID=""
  fi
  response_json="$(post_chat "$prompt" "$SESSION_ID")"
  echo "$response_json" > "$TMP_DIR/${id}.json"
  SESSION_ID="$(python3 - <<PY
import json
obj=json.loads(open("$TMP_DIR/${id}.json").read())
print(obj.get("session_id", ""))
PY
)"
}

TASK_FILES=(
  "01_explain_function"
  "02_fix_bug"
  "03_write_tests"
  "04_summarize_file"
  "05_recall_preference"
  "06_long_session"
  "07_tool_heavy"
  "08_web_research"
  "09_code_review"
  "10_scheduled_check"
)

for task in "${TASK_FILES[@]}"; do
  file="$TASK_DIR/${task}.md"
  if [[ ! -f "$file" ]]; then
    echo "warning: missing task file $file"
    continue
  fi
  if [[ "$task" == "05_recall_preference" || "$task" == "06_long_session" ]]; then
    record_task "$task" "$file" "shared"
  else
    record_task "$task" "$file" "fresh"
  fi
done

python3 - <<PY > "$OUT_JSON"
import json, os, glob, datetime

root = "$ROOT_DIR"
tmp = "$TMP_DIR"
input_price = float("$INPUT_PER_MTOK")
output_price = float("$OUTPUT_PER_MTOK")

def task_usage(path):
    obj = json.loads(open(path).read())
    usage = obj.get("usage", {})
    inp = int(usage.get("input_tokens", 0))
    out = int(usage.get("output_tokens", 0))
    cost = (inp / 1_000_000) * input_price + (out / 1_000_000) * output_price
    return {
        "task": os.path.basename(path).replace(".json", ""),
        "input_tokens": inp,
        "output_tokens": out,
        "llm_calls": int(usage.get("llm_calls", 0)),
        "duration_ms": int(obj.get("duration_ms", 0)),
        "cost_usd": round(cost, 6),
    }

results = []
for p in sorted(glob.glob(os.path.join(tmp, "0[1-57-9]*.json")) + glob.glob(os.path.join(tmp, "10_*.json"))):
    results.append(task_usage(p))

growth = []
growth_path = os.path.join(tmp, "growth.jsonl")
if os.path.exists(growth_path):
    for line in open(growth_path):
        line=line.strip()
        if not line:
            continue
        growth.append(json.loads(line))

total_in = sum(t["input_tokens"] for t in results) + sum(g["input_tokens"] for g in growth)
total_out = sum(t["output_tokens"] for t in results) + sum(g["output_tokens"] for g in growth)
total_cost = (total_in / 1_000_000) * input_price + (total_out / 1_000_000) * output_price

payload = {
    "benchmark": "phase2-token-efficiency",
    "generated_at": datetime.datetime.utcnow().replace(microsecond=0).isoformat() + "Z",
    "agent_url": "$AGENT_URL",
    "pricing": {
        "input_per_mtok_usd": input_price,
        "output_per_mtok_usd": output_price,
    },
    "tasks": results,
    "long_session_growth": growth,
    "totals": {
        "input_tokens": total_in,
        "output_tokens": total_out,
        "cost_usd": round(total_cost, 6),
        "heartbeat_cost_estimate_usd": round((500 / 1_000_000) * input_price + (150 / 1_000_000) * output_price, 6)
    }
}
print(json.dumps(payload, indent=2))
PY

echo "Benchmark complete: $OUT_JSON"
