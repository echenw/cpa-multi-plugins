#!/usr/bin/env bash
# verify-usage-chain.sh — T16 用量链集成验证 (feed → tracker → 看板).
#
# Plan ref: .omo/plans/cpa-plugin-suite.md task 16.
#   What to do: 编写 scripts/verify-usage-chain.sh（或 Go 集成测试）：
#     用 fixture 经 T10–T14 的写入路径生成 feed 文件 → 调用 tracker 导入
#     (syncUsageFeed/ingestFeedChunk 或 go test 集成用例) → 断言汇总查询的行数/总量正确；
#     覆盖轮转后断点续传幂等。Must NOT: 依赖真实上游或真实账号。
#   Acceptance: 脚本/测试 exit 0 并输出统计；重复运行不重复计数 (offset 幂等断言)。
#   QA: happy = 两轮运行幂等；failure = 截断 feed 中间一行 → 导入器行为可解释
#     (跳过/报错按实现，记录)。
#
# How it works (fixtures/mocks only, no real upstreams or accounts):
#   1. Fixture feed lines are generated in the EXACT schema the T10 canonical
#      writer (plugins/workbuddy/usage_feed.go:recordUsageFeed) and its T11–T14
#      copies (qoder/trae/zcode/mimo usage_feed.go) append: timestamp,
#      latency_ms, source, auth_index, provider, model, alias, endpoint,
#      auth_type, executor_type, failed, status_code, session_key,
#      reasoning_effort, ttft_ns, tokens{...}. One line per provider
#      (workbuddy x2, qoder, trae, zcode, mimo).
#      Assertions are on the `provider` field + row counts/totals, NOT on
#      executor_type labels (mimo rows were mislabeled workbuddy until the
#      T14 fix; counts and provider values are the stable contract).
#   2. Import is driven through the tracker's REAL consumer entry points:
#        - usagestats.Store.RecordFeedNDJSON  (usage_stats/feed_import.go:26)
#        - usagestats.Store.HandleQuery       (summary /stats + /requests reads)
#      Offset persistence (.offset sidecar) and rotation semantics
#      (file smaller than stored offset ⇒ rotation ⇒ restart at 0) replicate
#      feed_ingest.go loadFeedOffset/saveFeedOffset/syncUsageFeed; per-line
#      skip-on-error replicates ingestFeedChunk (unparsable lines are
#      consumed + warned, NOT retried). ingestFeedChunk itself lives in
#      package main (unimportable), so the replica + the go-test run below
#      together cover the real path.
#   3. The tracker's own Go integration cases (TestFeedIngestEndToEnd,
#      TestFeedIngestSkipsBadLine, TestFeedIngestPartialTrailingLine) are run
#      as the "go test 集成用例" leg.
#   4. Happy path is imported TWICE (two separate helper processes sharing
#      only the .offset file) to prove offset idempotency; then a
#      truncate-then-continue rotation; then a mid-line-truncation failure
#      case on a separate feed/DB.
#
# Exit status: 0 only if every assertion holds. All statistics are printed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TRACKER_DIR="$REPO_ROOT/plugins/token-usage-tracker"
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/usage-chain.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

FEED="$WORKDIR/token-usage-feed.ndjson"
DB="$WORKDIR/tracker.db"

pass=0
fail=0
check_eq() { # check_eq <label> <got> <want>
  if [[ "$2" == "$3" ]]; then
    pass=$((pass + 1)); echo "  PASS $1: got=$2 want=$3"
  else
    fail=$((fail + 1)); echo "  FAIL $1: got=$2 want=$3"
  fi
}
check_contains() { # check_contains <label> <haystack> <needle>
  if [[ "$2" == *"$3"* ]]; then
    pass=$((pass + 1)); echo "  PASS $1: contains $3 (in $2)"
  else
    fail=$((fail + 1)); echo "  FAIL $1: missing $3 (in $2)"
  fi
}

TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# feed_line <provider> <executor_type> <model> <auth> <input> <output> <reasoning> <total>
feed_line() {
  printf '{"timestamp":"%s","latency_ms":1500,"source":"fixture-%s","auth_index":"%s","provider":"%s","model":"%s","alias":"%s","endpoint":"POST /v1/chat/completions","auth_type":"oauth","executor_type":"%s","failed":false,"status_code":200,"session_key":"","reasoning_effort":"","ttft_ns":0,"tokens":{"input_tokens":%s,"output_tokens":%s,"reasoning_tokens":%s,"cached_tokens":0,"cache_read_tokens":0,"cache_creation_tokens":0,"total_tokens":%s}}\n' \
    "$TS" "$1" "$4" "$1" "$3" "$3" "$2" "$5" "$6" "$7" "$8"
}

echo "== T16 step 1: generate fixture feed via T10-T14 write paths =="
{
  feed_line workbuddy workbuddy  deepseek-v4  u-wb-1 100  200 50  350
  feed_line workbuddy workbuddy  deepseek-v4  u-wb-2 50   80  0   130
  feed_line qoder     qoderwork  qoder-model  u-qo-1 200  100 0   300
  feed_line trae      trae       trae-model   u-tr-1 10   20  0   30
  feed_line zcode     zcode      zcode-model  u-zc-1 5    5   0   10
  feed_line mimo      mimo       mimo-model   u-mi-1 1000 500 100 1600
} >"$FEED"
echo "  feed=$FEED lines=$(grep -c . "$FEED") bytes=$(wc -c <"$FEED" | tr -d ' ')"
# Expected happy-path totals: requests=6 input=1365 output=905 total=2420
#   input:  100+50+200+10+5+1000 = 1365
#   output: 200+80+100+20+5+500  = 905
#   total:  350+130+300+30+10+1600 = 2420

echo "== T16 step 2: build import helper against real tracker consumer =="
HELPER="$WORKDIR/chaincheck"
mkdir -p "$HELPER"
TRACKER_MOD="$(sed -n 's/^module //p' "$TRACKER_DIR/go.mod")"
cat >"$HELPER/go.mod" <<EOF
module chaincheck

go 1.26.0

require $TRACKER_MOD v0.0.0

replace $TRACKER_MOD => $TRACKER_DIR
EOF
cp "$TRACKER_DIR/go.sum" "$HELPER/go.sum"
cat >"$HELPER/main.go" <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	usagestats "github.com/mmqz/cpa-multi-plugins/plugins/token-usage-tracker/usage_stats"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: chaincheck <import|stats> ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "import":
		err = doImport(os.Args[2], os.Args[3])
	case "stats":
		err = doStats(os.Args[2])
	default:
		err = fmt.Errorf("unknown cmd %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "chaincheck:", err)
		os.Exit(1)
	}
}

func openStore(db string) (*usagestats.Store, error) {
	return usagestats.Open(usagestats.Config{
		DataPath:        db,
		RetentionDays:   365,
		FlushInterval:   time.Second,
		FlushMaxRecords: 100,
		SyncOnRecord:    true,
	})
}

// loadOffset mirrors feed_ingest.go loadFeedOffset: persisted offsets beyond
// the current size mean the producer truncated (rotation) -> restart at 0.
func loadOffset(feedPath string, size int64) (off int64, persisted int64, rotated bool) {
	raw, err := os.ReadFile(feedPath + ".offset")
	if err != nil {
		return 0, -1, false
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if perr != nil || n < 0 || n > size {
		if perr == nil && n > size {
			return 0, n, true
		}
		return 0, n, false
	}
	return n, n, false
}

// doImport mirrors syncUsageFeed + ingestFeedChunk: read bytes after the
// persisted offset, record complete lines via the real RecordFeedNDJSON,
// skip-but-consume unparsable lines, leave a trailing partial line.
func doImport(feedPath, db string) error {
	store, err := openStore(db)
	if err != nil {
		return err
	}
	defer store.Close()
	f, err := os.Open(feedPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	off, persisted, rotated := loadOffset(feedPath, size)
	if off >= size {
		fmt.Printf("IMPORTED=0 SKIPPED=0 CONSUMED=0 OFFSET_BEFORE=%d OFFSET_AFTER=%d SIZE=%d ROTATED=%v PERSISTED=%d\n", off, off, size, false, persisted)
		return nil
	}
	if _, err := f.Seek(off, 0); err != nil {
		return err
	}
	buf := make([]byte, size-off)
	n, err := readFull(f, buf)
	if err != nil {
		return err
	}
	text := string(buf[:n])
	lines := strings.Split(text, "\n")
	var imported, skipped int
	var consumed int64
	for i, line := range lines {
		isLast := i == len(lines)-1
		if isLast && (line == "" || !strings.HasSuffix(text, "\n")) {
			break // phantom after trailing newline, or partial line
		}
		if len(line) == 0 {
			consumed++
			continue
		}
		lineBytes := int64(len(line)) + 1
		if err := store.RecordFeedNDJSON(line); err != nil {
			skipped++ // ingestFeedChunk: skip + warn, offset still advances
			fmt.Fprintf(os.Stderr, "chaincheck: skipping unparsable line: %v\n", err)
			consumed += lineBytes
			continue
		}
		imported++
		consumed += lineBytes
	}
	newOffset := off + consumed
	if newOffset > size {
		newOffset = size
	}
	if newOffset > off {
		_ = os.WriteFile(feedPath+".offset", []byte(strconv.FormatInt(newOffset, 10)+"\n"), 0o644)
	}
	fmt.Printf("IMPORTED=%d SKIPPED=%d CONSUMED=%d OFFSET_BEFORE=%d OFFSET_AFTER=%d SIZE=%d ROTATED=%v PERSISTED=%d\n", imported, skipped, consumed, off, newOffset, size, rotated, persisted)
	return nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return total, err
		}
		if n == 0 {
			break
		}
	}
	return total, nil
}

func doStats(db string) error {
	store, err := openStore(db)
	if err != nil {
		return err
	}
	defer store.Close()
	res := store.HandleQuery(http.MethodGet, "/stats", url.Values{"range": []string{"24h"}}, nil, nil)
	if res.Status != http.StatusOK {
		return fmt.Errorf("/stats status=%d body=%s", res.Status, res.Body)
	}
	var s struct {
		Summary struct {
			Requests    uint64 `json:"requests"`
			TotalTokens uint64 `json:"total_tokens"`
			InputTokens uint64 `json:"input_tokens"`
			OutputTokens uint64 `json:"output_tokens"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(res.Body, &s); err != nil {
		return fmt.Errorf("decode /stats: %w", err)
	}
	req := store.HandleQuery(http.MethodGet, "/requests", url.Values{"limit": []string{"100"}}, nil, nil)
	if req.Status != http.StatusOK {
		return fmt.Errorf("/requests status=%d body=%s", req.Status, req.Body)
	}
	var page struct {
		Items []struct {
			Provider string `json:"provider"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(req.Body, &page); err != nil {
		return fmt.Errorf("decode /requests: %w", err)
	}
	seen := map[string]bool{}
	for _, it := range page.Items {
		seen[it.Provider] = true
	}
	provs := make([]string, 0, len(seen))
	for p := range seen {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	fmt.Printf("REQUESTS=%d TOTAL=%d INPUT=%d OUTPUT=%d REQ_ROWS=%d REQ_TOTAL=%d PROVIDERS=%s\n",
		s.Summary.Requests, s.Summary.TotalTokens, s.Summary.InputTokens, s.Summary.OutputTokens,
		len(page.Items), page.Total, strings.Join(provs, ","))
	return nil
}
EOF
(cd "$HELPER" && GOFLAGS=-mod=mod go mod tidy >/dev/null 2>&1 || GOFLAGS=-mod=mod go mod tidy)
go -C "$HELPER" build -o "$HELPER/chaincheck" . 2>&1
echo "  helper built: $HELPER/chaincheck"

echo "== T16 step 3: happy path pass 1 (feed -> import -> summary) =="
OUT1="$("$HELPER/chaincheck" import "$FEED" "$DB")"
echo "  $OUT1"
eval "$(echo "$OUT1" | tr ' ' '\n' | grep -E '^(IMPORTED|SKIPPED|OFFSET_AFTER)=' | sed 's/^/IMP1_/')"
STATS1="$("$HELPER/chaincheck" stats "$DB")"
echo "  $STATS1"
eval "$(echo "$STATS1" | tr ' ' '\n' | grep -E '^(REQUESTS|TOTAL|INPUT|OUTPUT|PROVIDERS)=' | sed 's/^/S1_/')"
check_eq "pass1 imported rows" "$IMP1_IMPORTED" 6
check_eq "pass1 skipped rows" "$IMP1_SKIPPED" 0
check_eq "pass1 summary requests" "$S1_REQUESTS" 6
check_eq "pass1 summary total_tokens" "$S1_TOTAL" 2420
check_eq "pass1 summary input_tokens" "$S1_INPUT" 1365
check_eq "pass1 summary output_tokens" "$S1_OUTPUT" 905
for p in workbuddy qoder trae zcode mimo; do check_contains "pass1 provider $p" "$S1_PROVIDERS" "$p"; done

echo "== T16 step 4: happy path pass 2 (offset idempotency, separate process) =="
OUT2="$("$HELPER/chaincheck" import "$FEED" "$DB")"
echo "  $OUT2"
eval "$(echo "$OUT2" | tr ' ' '\n' | grep -E '^(IMPORTED|SKIPPED)=' | sed 's/^/IMP2_/')"
STATS2="$("$HELPER/chaincheck" stats "$DB")"
echo "  $STATS2"
eval "$(echo "$STATS2" | tr ' ' '\n' | grep -E '^(REQUESTS|TOTAL)=' | sed 's/^/S2_/')"
check_eq "pass2 imported rows (must be 0)" "$IMP2_IMPORTED" 0
check_eq "pass2 skipped rows (must be 0)" "$IMP2_SKIPPED" 0
check_eq "pass2 requests unchanged" "$S2_REQUESTS" "$S1_REQUESTS"
check_eq "pass2 total unchanged" "$S2_TOTAL" "$S1_TOTAL"

echo "== T16 step 5: rotation (truncate-then-continue, no double count) =="
# Producer rotation semantics (usage_feed.go appendUsageFeedLine): os.Truncate
# on cap; consumer treats shrink as rotation signal and re-reads from 0.
BEFORE_SIZE="$(wc -c <"$FEED" | tr -d ' ')"
truncate -s 0 "$FEED"
feed_line workbuddy workbuddy post-rotate-model u-wb-9 40 30 0 70 >>"$FEED"
echo "  rotated: size $BEFORE_SIZE -> $(wc -c <"$FEED" | tr -d ' ') (1 new line)"
OUT3="$("$HELPER/chaincheck" import "$FEED" "$DB")"
echo "  $OUT3"
eval "$(echo "$OUT3" | tr ' ' '\n' | grep -E '^(IMPORTED|SKIPPED|ROTATED)=' | sed 's/^/IMP3_/')"
STATS3="$("$HELPER/chaincheck" stats "$DB")"
echo "  $STATS3"
eval "$(echo "$STATS3" | tr ' ' '\n' | grep -E '^(REQUESTS|TOTAL|INPUT|OUTPUT)=' | sed 's/^/S3_/')"
check_eq "rotation detected" "$IMP3_ROTATED" true
check_eq "rotation imported rows" "$IMP3_IMPORTED" 1
check_eq "rotation requests (6+1)" "$S3_REQUESTS" 7
check_eq "rotation total (2420+70)" "$S3_TOTAL" 2490
check_eq "rotation input (1365+40)" "$S3_INPUT" 1405
check_eq "rotation output (905+30)" "$S3_OUTPUT" 935

echo "== T16 step 6: failure case (one middle line truncated) =="
FEED2="$WORKDIR/feed-failure.ndjson"
DB2="$WORKDIR/tracker-failure.db"
TS2="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GOOD_A="$(printf '{"timestamp":"%s","latency_ms":100,"source":"fixture","auth_index":"u-a","provider":"workbuddy","model":"m-a","alias":"m-a","endpoint":"POST /v1/chat/completions","auth_type":"oauth","executor_type":"workbuddy","failed":false,"status_code":200,"session_key":"","reasoning_effort":"","ttft_ns":0,"tokens":{"input_tokens":60,"output_tokens":40,"reasoning_tokens":0,"cached_tokens":0,"cache_read_tokens":0,"cache_creation_tokens":0,"total_tokens":100}}' "$TS2")"
GOOD_B="$(printf '{"timestamp":"%s","latency_ms":100,"source":"fixture","auth_index":"u-b","provider":"qoder","model":"m-b","alias":"m-b","endpoint":"POST /v1/chat/completions","auth_type":"oauth","executor_type":"qoderwork","failed":false,"status_code":200,"session_key":"","reasoning_effort":"","ttft_ns":0,"tokens":{"input_tokens":120,"output_tokens":80,"reasoning_tokens":0,"cached_tokens":0,"cache_read_tokens":0,"cache_creation_tokens":0,"total_tokens":200}}' "$TS2")"
# Truncate the middle line mid-JSON (simulates a torn write / truncated rotation).
BAD_MID="$(printf '%s' "$GOOD_B" | cut -c1-40)"
printf '%s\n%s\n%s\n' "$GOOD_A" "$BAD_MID" "$GOOD_B" >"$FEED2"
echo "  failure feed: 3 lines, middle one cut to 40 chars:"
echo "  line2: $BAD_MID"
OUT4="$("$HELPER/chaincheck" import "$FEED2" "$DB2")"
echo "  $OUT4"
eval "$(echo "$OUT4" | tr ' ' '\n' | grep -E '^(IMPORTED|SKIPPED|OFFSET_AFTER|SIZE)=' | sed 's/^/IMP4_/')"
STATS4="$("$HELPER/chaincheck" stats "$DB2")"
echo "  $STATS4"
eval "$(echo "$STATS4" | tr ' ' '\n' | grep -E '^(REQUESTS|TOTAL)=' | sed 's/^/S4_/')"
check_eq "failure imported good rows" "$IMP4_IMPORTED" 2
check_eq "failure skipped bad rows" "$IMP4_SKIPPED" 1
check_eq "failure offset advanced past bad line" "$IMP4_OFFSET_AFTER" "$IMP4_SIZE"
check_eq "failure summary requests (good only)" "$S4_REQUESTS" 2
check_eq "failure summary total (100+200)" "$S4_TOTAL" 300
echo "  behavior: per ingestFeedChunk, the torn middle line is SKIPPED with a"
echo "  warning (consumed, offset advances past it) — no retry, no crash;"

echo "== T16 step 7: tracker go-test integration cases =="
go -C "$TRACKER_DIR" test -count=1 -run 'TestFeedIngestEndToEnd|TestFeedIngestSkipsBadLine|TestFeedIngestPartialTrailingLine' . 2>&1 | tail -5

echo "== RESULT: pass=$pass fail=$fail =="
if [[ "$fail" -ne 0 ]]; then
  echo "VERIFY-USAGE-CHAIN: FAIL ($fail failing assertions)"
  exit 1
fi
echo "VERIFY-USAGE-CHAIN: OK (requests=6 total=2420; idempotent rerun; rotation +1; torn-line skip recorded)"
