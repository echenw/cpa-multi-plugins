// 宿主配置热重载窗口的丢记录回归：验证 store 关闭期间 feed 偏移必须停在未写入的行之前，
// 句柄恢复后同一批数据既不丢也不重复，以及存储配置未变时不再更换 store 句柄。
package main

import (
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	usagestats "github.com/mmqz/cpa-multi-plugins/plugins/token-usage-tracker/usage_stats"
)

// 最近修改时间：2026-10-11；改动原因：锁定"关闭句柄不得推进偏移"这条防丢数据契约。
func TestIngestFeedChunkStopsOffsetOnClosedStore(t *testing.T) {
	resetFeedState()
	defer resetFeedState()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")
	store, err := usagestats.Open(usagestats.Config{DataPath: dbPath, RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// 1. 先写一条健康记录，确认 store 本身可用，再关闭它模拟换句柄窗口。
	now := time.Now().UTC()
	warm := validFeedLine(now.Format(time.RFC3339Nano), "warm", "u-0", 1, 1, 2) + "\n"
	if consumed := ingestFeedChunk(store, []byte(warm)); consumed != int64(len(warm)) {
		t.Fatalf("warm consumed = %d, want %d", consumed, len(warm))
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 2. 关闭后喂三行：一行都不许被算作已消费，否则偏移推进就把这批数据永久跳过。
	chunk := ""
	for i := 1; i <= 3; i++ {
		chunk += validFeedLine(now.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), "m", "u-"+string(rune('a'+i)), 10, 20, 30) + "\n"
	}
	if consumed := ingestFeedChunk(store, []byte(chunk)); consumed != 0 {
		t.Fatalf("closed-store consumed = %d, want 0（未落库的行不得推进偏移）", consumed)
	}
}

// 最近修改时间：2026-10-11；改动原因：证明上一轮的"停在原地"在重开后既不丢也不重复。
func TestIngestFeedChunkResumesAfterReopen(t *testing.T) {
	resetFeedState()
	defer resetFeedState()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")
	now := time.Now().UTC()
	chunk := ""
	for i := 1; i <= 3; i++ {
		chunk += validFeedLine(now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339Nano), "m", "src-"+time.Duration(i).String(), 10, 20, 30) + "\n"
	}

	// 1. 第一次用关闭的句柄消费：偏移为 0，库里没有任何这批数据。
	store, err := usagestats.Open(usagestats.Config{DataPath: dbPath, RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if consumed := ingestFeedChunk(store, []byte(chunk)); consumed != 0 {
		t.Fatalf("closed consumed = %d, want 0", consumed)
	}

	// 2. 重开同一库后重新消费整块：三行必须全部入库且 consumed 等于整块长度。
	reopened, err := usagestats.Open(usagestats.Config{DataPath: dbPath, RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if consumed := ingestFeedChunk(reopened, []byte(chunk)); consumed != int64(len(chunk)) {
		t.Fatalf("reopened consumed = %d, want %d", consumed, len(chunk))
	}
	result := reopened.HandleQuery(http.MethodGet, "/stats", url.Values{"range": []string{"24h"}}, nil, nil)
	if got := jsonRequestsTotal(result.Body); got != 3 {
		t.Fatalf("total_requests = %d, want 3（不得丢，也不得因重试重复）", got)
	}
}

// 最近修改时间：2026-10-11；改动原因：无关配置重载不得换句柄，从源头消除丢记录窗口。
func TestReopenStoreKeepsHandleWhenConfigUnchanged(t *testing.T) {
	resetFeedState()
	defer resetFeedState()

	dir := t.TempDir()
	cfg := trackerConfig{
		FeedEnabled:   true,
		FeedPath:      filepath.Join(dir, "feed.ndjson"),
		DBPath:        filepath.Join(dir, "s.db"),
		RetentionDays: 365,
		FlushInterval: time.Second,
	}

	// 1. 首次打开建立句柄。
	reopenStore(cfg)
	storeMu.RLock()
	first := usageStore
	storeMu.RUnlock()
	if first == nil {
		t.Fatalf("first reopen should open a store")
	}
	defer first.Close()

	// 2. 同样的配置再推一次（宿主每次配置重载都会推）：必须复用句柄，不产生关闭窗口。
	reopenStore(cfg)
	storeMu.RLock()
	second := usageStore
	storeMu.RUnlock()
	if second != first {
		t.Fatalf("unchanged config must not swap the store handle")
	}
	if err := first.RecordFeedNDJSON(validFeedLine(time.Now().UTC().Format(time.RFC3339Nano), "m", "u", 1, 1, 2)); err != nil {
		t.Fatalf("record after no-op reopen: %v", err)
	}

	// 3. 存储配置实质变化时才允许换句柄。
	changed := cfg
	changed.DBPath = filepath.Join(dir, "other.db")
	reopenStore(changed)
	storeMu.RLock()
	third := usageStore
	storeMu.RUnlock()
	if third == nil || third == first {
		t.Fatalf("changed config must swap the store handle")
	}
	defer third.Close()
}

// 最近修改时间：2026-10-11；改动原因：覆盖"只有连续关闭才累计退役判定"的重置语义。
func TestImporterOrphanCountRequiresConsecutiveClosedPolls(t *testing.T) {
	cases := []struct {
		name      string
		sequence  []bool
		wantCount int
		wantStop  bool
	}{
		{"连续三轮关闭后判定退役", []bool{true, true, true}, feedImporterOrphanPolls, true},
		{"两轮关闭不足以判定", []bool{true, true}, 2, false},
		{"一次健康轮次必须清零计数", []bool{true, true, false, true, true}, 2, false},
		{"穿插健康的长序列永不退出", []bool{true, true, false, true, true, false, true, true}, 2, false},
		{"全程健康计数恒为零", []bool{false, false, false}, 0, false},
	}
	for _, c := range cases {
		count := 0
		stop := false
		for _, closed := range c.sequence {
			count, stop = importerOrphanCount(closed, count)
		}
		if count != c.wantCount || stop != c.wantStop {
			t.Errorf("%s: count=%d stop=%v, want count=%d stop=%v", c.name, count, stop, c.wantCount, c.wantStop)
		}
	}
}

// 最近修改时间：2026-10-11；改动原因：锁定退役实例的导入协程会自行退出的契约。
func TestFeedImporterLoopExitsWhenStoreClosed(t *testing.T) {
	resetFeedState()
	defer resetFeedState()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")
	store, err := usagestats.Open(usagestats.Config{DataPath: dbPath, RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 1. 模拟宿主热重载后本实例被退役：句柄已关闭且不会再被换新句柄。
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	storeMu.Lock()
	usageStore = store
	appliedStoreKey = "retired-instance"
	storeMu.Unlock()
	trackerCfgMu.Lock()
	trackerCfg = trackerConfig{
		FeedEnabled:   true,
		FeedPath:      filepath.Join(dir, "feed.ndjson"),
		DBPath:        dbPath,
		RetentionDays: 365,
		FlushInterval: time.Second,
		PollInterval:  10 * time.Millisecond,
	}
	trackerCfgMu.Unlock()

	// 2. 连续若干轮关闭后循环必须自己返回，而不是永久 tail 共享 feed。
	done := make(chan struct{})
	go func() {
		feedImporterLoop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("retired instance importer loop did not exit")
	}
	if !currentStoreClosed() {
		t.Fatalf("store should still report closed after importer exit")
	}
}

// 最近修改时间：2026-10-11；改动原因：证明单次换句柄不会误杀活跃实例的导入循环。
func TestFeedImporterLoopSurvivesHandleSwapAndResumes(t *testing.T) {
	resetFeedState()
	defer resetFeedState()

	dir := t.TempDir()
	stale, err := usagestats.Open(usagestats.Config{DataPath: filepath.Join(dir, "stale.db"), RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("open stale: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale: %v", err)
	}

	// 1. 先让循环处于"关闭句柄"状态跑几轮（未到退役判定阈值）。
	storeMu.Lock()
	usageStore = stale
	appliedStoreKey = "swap-window"
	storeMu.Unlock()
	trackerCfgMu.Lock()
	trackerCfg = trackerConfig{
		FeedEnabled:   true,
		FeedPath:      filepath.Join(dir, "feed.ndjson"),
		DBPath:        filepath.Join(dir, "stale.db"),
		RetentionDays: 365,
		FlushInterval: time.Second,
		PollInterval:  50 * time.Millisecond,
	}
	trackerCfgMu.Unlock()

	done := make(chan struct{})
	go func() {
		feedImporterLoop()
		close(done)
	}()
	// 只让它经历 1~2 轮关闭（阈值为 3 轮），留出足够余量避免定时器抖动导致误退出。
	time.Sleep(120 * time.Millisecond)

	// 2. 换入健康句柄（模拟宿主完成换句柄）：计数必须归零，循环不得退出。
	live, err := usagestats.Open(usagestats.Config{DataPath: filepath.Join(dir, "live.db"), RetentionDays: 365, FlushInterval: time.Second, FlushMaxRecords: 100, SyncOnRecord: true})
	if err != nil {
		t.Fatalf("open live: %v", err)
	}
	if err := live.RecordFeedNDJSON(validFeedLine(time.Now().UTC().Format(time.RFC3339Nano), "m", "u-before", 1, 1, 2)); err != nil {
		t.Fatalf("record before swap: %v", err)
	}
	storeMu.Lock()
	usageStore = live
	appliedStoreKey = "live-handle"
	storeMu.Unlock()
	time.Sleep(250 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("importer loop exited although the store became healthy again")
	default:
	}

	// 3. 之后再真正关闭句柄，循环应重新计数并退出（证明计数被健康轮次重置过）。
	if err := live.Close(); err != nil {
		t.Fatalf("close live: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("importer loop did not exit after the store finally closed")
	}
}
