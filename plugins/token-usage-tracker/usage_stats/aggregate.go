package usagestats

import (
	"cmp"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type Dimensions struct {
	Provider         string `json:"provider"`
	ExecutorType     string `json:"executor_type"`
	Model            string `json:"model"`
	Alias            string `json:"alias"`
	Source           string `json:"source"`
	APIKey           string `json:"api_key,omitempty"`
	APIKeyHash       string `json:"api_key_hash,omitempty"`
	APIKeyGeneration uint64 `json:"api_key_generation,omitempty"`
	APIKeyRef        string `json:"api_key_ref,omitempty"`
	APIKeyStatus     string `json:"api_key_status,omitempty"`
	AuthType         string `json:"auth_type"`
	// SessionKey is the same per-conversation key scheduler.pick used to pin
	// the account. Equal SessionKey values across rows mean the same
	// stickiness-bound conversation. The producer currently always writes a
	// value (empty string when no session signal was present) so the dashboard
	// can group / sort requests by conversation.
	SessionKey       string `json:"session_key,omitempty"`
	ReasoningEffort  string `json:"reasoning_effort"`
	Failed           bool   `json:"failed"`
	FailureStatus    int    `json:"failure_status"`
}

// usageFilter scopes every analytics surface to the same persisted dimensions.
// Empty fields intentionally mean no restriction, which preserves legacy callers.
type usageFilter struct {
	Source           string
	APIKeyHash       string
	APIKeyGeneration uint64
	APIKeyRefSet     string
	Model            string
}

func newUsageFilter(source, apiKeyIdentity string) usageFilter {
	if apiKeyIdentity == "" {
		return usageFilter{Source: normalizeDimension(source)}
	}
	return newUsageFilterFromIdentities(source, []string{apiKeyIdentity})
}

func newUsageFilterFromIdentities(source string, identities []string) usageFilter {
	filter := usageFilter{Source: normalizeDimension(source)}
	seen := make(map[string]struct{})
	refs := make([]string, 0, len(identities))
	for _, identity := range identities {
		identity = normalizeDimension(identity)
		if identity == "" {
			continue
		}
		if generation, hash, ok := parseAPIKeyRef(identity); ok {
			ref := apiKeyRef(generation, hash)
			if _, exists := seen[ref]; exists {
				continue
			}
			seen[ref] = struct{}{}
			refs = append(refs, ref)
			continue
		}
		filter.APIKeyHash = identity
	}
	if len(refs) > 0 {
		sort.Strings(refs)
		filter.APIKeyRefSet = strings.Join(refs, "\n")
	}
	return filter
}

func (f usageFilter) matches(dimensions Dimensions) bool {
	if f.Source != "" && dimensions.Source != f.Source {
		return false
	}
	if f.Model != "" && compactModelName(dimensions.Model) != f.Model {
		return false
	}
	if f.APIKeyRefSet != "" {
		ref := apiKeyRef(dimensions.APIKeyGeneration, dimensions.APIKeyHash)
		if ref == "" {
			return false
		}
		for _, candidate := range strings.Split(f.APIKeyRefSet, "\n") {
			if candidate == ref {
				return true
			}
		}
		return false
	}
	return (f.APIKeyHash == "" || dimensions.APIKeyHash == f.APIKeyHash) &&
		(f.APIKeyGeneration == 0 || dimensions.APIKeyGeneration == f.APIKeyGeneration)
}

func (d *Dimensions) Redact() {
	d.APIKey = ""
	d.APIKeyHash = ""
	d.APIKeyGeneration = 0
	d.APIKeyRef = ""
	d.APIKeyStatus = ""
}

func (d *Dimensions) Reveal(decrypt DecryptFunc) {
	if d.APIKeyHash == "" || d.APIKeyGeneration == 0 {
		return
	}
	d.APIKeyRef = apiKeyRef(d.APIKeyGeneration, d.APIKeyHash)
	plaintext, status := decrypt(d.APIKey, d.APIKeyHash, d.APIKeyGeneration)
	d.APIKey = plaintext
	d.APIKeyStatus = status
}

type Counters struct {
	Requests            uint64 `json:"requests"`
	FailedRequests      uint64 `json:"failed_requests"`
	InputTokens         uint64 `json:"input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	ReasoningTokens     uint64 `json:"reasoning_tokens"`
	CachedTokens        uint64 `json:"cached_tokens"`
	CacheReadTokens     uint64 `json:"cache_read_tokens"`
	CacheCreationTokens uint64 `json:"cache_creation_tokens"`
	TotalTokens         uint64 `json:"total_tokens"`
	TotalLatencyNS      uint64 `json:"total_latency_ns"`
	TotalTTFTNS         uint64 `json:"total_ttft_ns"`
	LatencySamples      uint64 `json:"latency_samples"`
	TTFTSamples         uint64 `json:"ttft_samples"`
}

func (c *Counters) add(other Counters) {
	c.Requests = saturatingAdd(c.Requests, other.Requests)
	c.FailedRequests = saturatingAdd(c.FailedRequests, other.FailedRequests)
	c.InputTokens = saturatingAdd(c.InputTokens, other.InputTokens)
	c.OutputTokens = saturatingAdd(c.OutputTokens, other.OutputTokens)
	c.ReasoningTokens = saturatingAdd(c.ReasoningTokens, other.ReasoningTokens)
	c.CachedTokens = saturatingAdd(c.CachedTokens, other.CachedTokens)
	c.CacheReadTokens = saturatingAdd(c.CacheReadTokens, other.CacheReadTokens)
	c.CacheCreationTokens = saturatingAdd(c.CacheCreationTokens, other.CacheCreationTokens)
	c.TotalTokens = saturatingAdd(c.TotalTokens, other.TotalTokens)
	c.TotalLatencyNS = saturatingAdd(c.TotalLatencyNS, other.TotalLatencyNS)
	c.TotalTTFTNS = saturatingAdd(c.TotalTTFTNS, other.TotalTTFTNS)
	c.LatencySamples = saturatingAdd(c.LatencySamples, other.LatencySamples)
	c.TTFTSamples = saturatingAdd(c.TTFTSamples, other.TTFTSamples)
}

func (c Counters) averageLatencyNS() uint64 {
	if c.LatencySamples == 0 {
		return 0
	}
	return c.TotalLatencyNS / c.LatencySamples
}

func (c Counters) averageTTFTNS() uint64 {
	if c.TTFTSamples == 0 {
		return 0
	}
	return c.TotalTTFTNS / c.TTFTSamples
}

func countersForUsage(usage normalizedUsage) Counters {
	result := usage.Counters
	if usage.LatencyNS > 0 {
		result.TotalLatencyNS = usage.LatencyNS
		result.LatencySamples = 1
	}
	if usage.TTFTNS > 0 {
		result.TotalTTFTNS = usage.TTFTNS
		result.TTFTSamples = 1
	}
	return result
}

func saturatingAdd(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}

type aggregateKey struct {
	Hour       int64
	Dimensions Dimensions
}

type GroupStats struct {
	Dimensions
	Counters
	AverageLatencyNS uint64 `json:"average_latency_ns"`
	AverageTTFTNS    uint64 `json:"average_ttft_ns"`
}

// ModelStats is the compact per-model aggregate used by the first dashboard
// response. It intentionally omits the unused dimension fields in GroupStats.
type ModelStats struct {
	Model string `json:"model"`
	Counters
	AverageLatencyNS uint64 `json:"average_latency_ns"`
	AverageTTFTNS    uint64 `json:"average_ttft_ns"`
}

type SeriesPoint struct {
	Hour string `json:"hour"`
	Counters
}

// ModelSeriesPoint preserves the time-bucketed model split needed by the dashboard for
// stacked trends, model drill-down, and cost calculations without retaining
// individual prompt contents.
type ModelSeriesPoint struct {
	Hour  string `json:"hour"`
	Model string `json:"model"`
	Counters
}

type StatsResponse struct {
	SchemaVersion uint32             `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	Range         string             `json:"range"`
	RetainedSince time.Time          `json:"retained_since"`
	LastUsed      time.Time          `json:"last_used"`
	Summary       Counters           `json:"summary"`
	Groups        []GroupStats       `json:"groups"`
	Series        []SeriesPoint      `json:"series"`
	ModelSeries   []ModelSeriesPoint `json:"model_series"`
	Sources       []string           `json:"sources"`
	APIKeys       []APIKeyOption     `json:"api_keys,omitempty"`
}

// InitialStatsResponse excludes dimension rows and per-model time points so the
// dashboard can render its first screen without parsing the largest payloads.
type InitialStatsResponse struct {
	SchemaVersion uint32         `json:"schema_version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Range         string         `json:"range"`
	RetainedSince time.Time      `json:"retained_since"`
	LastUsed      time.Time      `json:"last_used"`
	Summary       Counters       `json:"summary"`
	Models        []ModelStats   `json:"models"`
	Sources       []string       `json:"sources"`
	APIKeys       []APIKeyOption `json:"api_keys,omitempty"`
}

type GroupStatsPage struct {
	SchemaVersion uint32       `json:"schema_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Range         string       `json:"range"`
	Items         []GroupStats `json:"items"`
	Total         int          `json:"total"`
}

type APIKeyOption struct {
	Ref        string `json:"ref"`
	Hash       string `json:"hash"`
	Generation uint64 `json:"generation"`
	Key        string `json:"key,omitempty"`
	Status     string `json:"status,omitempty"`
}

func (s *StatsResponse) Redact() {
	for i := range s.Groups {
		s.Groups[i].Dimensions.Redact()
	}
	s.APIKeys = nil
}

func (s *StatsResponse) Reveal(decrypt DecryptFunc) {
	for i := range s.Groups {
		s.Groups[i].Dimensions.Reveal(decrypt)
	}
	for i := range s.APIKeys {
		plaintext, status := decrypt(s.APIKeys[i].Key, s.APIKeys[i].Hash, s.APIKeys[i].Generation)
		s.APIKeys[i].Key = plaintext
		s.APIKeys[i].Status = status
	}
}

func (s *InitialStatsResponse) Redact() { s.APIKeys = nil }

func (s *InitialStatsResponse) Reveal(decrypt DecryptFunc) {
	for i := range s.APIKeys {
		plaintext, status := decrypt(s.APIKeys[i].Key, s.APIKeys[i].Hash, s.APIKeys[i].Generation)
		s.APIKeys[i].Key = plaintext
		s.APIKeys[i].Status = status
	}
}

func (p *GroupStatsPage) Redact() {
	for i := range p.Items {
		p.Items[i].Dimensions.Redact()
	}
}

func (p *GroupStatsPage) Reveal(decrypt DecryptFunc) {
	for i := range p.Items {
		p.Items[i].Dimensions.Reveal(decrypt)
	}
}

type usageRange struct {
	Name  string
	Start time.Time
	End   time.Time
}

func buildStats(data map[aggregateKey]Counters, since, lastUsed time.Time, requestedRange string, now time.Time) (StatsResponse, error) {
	queryRange, err := presetUsageRange(requestedRange, now)
	if err != nil {
		return StatsResponse{}, err
	}
	return buildStatsForRange(data, since, lastUsed, queryRange, "", now), nil
}

func buildStatsForRange(data map[aggregateKey]Counters, since, lastUsed time.Time, queryRange usageRange, source string, now time.Time) StatsResponse {
	return buildStatsForRangeWithFilter(data, since, lastUsed, queryRange, newUsageFilter(source, ""), now, nil)
}

func buildStatsForRangeWithFilter(data map[aggregateKey]Counters, since, lastUsed time.Time, queryRange usageRange, filter usageFilter, now time.Time, apiKeyCiphertexts map[string]string) StatsResponse {
	groups := make(map[Dimensions]Counters)
	series := make(map[int64]Counters)
	modelSeries := make(map[struct {
		Hour  int64
		Model string
	}]Counters)
	summary := Counters{}
	sources := make(map[string]struct{})
	apiKeyRefs := make(map[string]struct{})
	for key, counters := range data {
		bucketTime := time.Unix(key.Hour, 0).UTC()
		if !queryRange.Start.IsZero() && bucketTime.Before(queryRange.Start) {
			continue
		}
		if !queryRange.End.IsZero() && !bucketTime.Before(queryRange.End) {
			continue
		}
		dimensions := sanitizeDimensionsSource(key.Dimensions)
		if dimensions.Source != "" {
			sources[dimensions.Source] = struct{}{}
		}
		if ref := apiKeyRef(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
			apiKeyRefs[ref] = struct{}{}
		}
		if !filter.matches(dimensions) {
			continue
		}
		group := groups[dimensions]
		group.add(counters)
		groups[dimensions] = group

		point := series[key.Hour]
		point.add(counters)
		series[key.Hour] = point

		model := key.Dimensions.Model
		if model == "" {
			model = "未标记模型"
		}
		modelKey := struct {
			Hour  int64
			Model string
		}{Hour: key.Hour, Model: model}
		modelPoint := modelSeries[modelKey]
		modelPoint.add(counters)
		modelSeries[modelKey] = modelPoint

		summary.add(counters)
	}

	groupRows := make([]GroupStats, 0, len(groups))
	for dimensions, counters := range groups {
		if ref := apiKeyRef(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
			dimensions.APIKey = apiKeyCiphertexts[ref]
		}
		groupRows = append(groupRows, GroupStats{
			Dimensions:       dimensions,
			Counters:         counters,
			AverageLatencyNS: counters.averageLatencyNS(),
			AverageTTFTNS:    counters.averageTTFTNS(),
		})
	}
	sort.Slice(groupRows, func(i, j int) bool {
		if groupRows[i].TotalTokens != groupRows[j].TotalTokens {
			return groupRows[i].TotalTokens > groupRows[j].TotalTokens
		}
		return compareDimensions(groupRows[i].Dimensions, groupRows[j].Dimensions) < 0
	})

	hours := make([]int64, 0, len(series))
	for hour := range series {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	points := make([]SeriesPoint, 0, len(hours))
	for _, hour := range hours {
		points = append(points, SeriesPoint{
			Hour:     time.Unix(hour, 0).UTC().Format(time.RFC3339),
			Counters: series[hour],
		})
	}

	modelKeys := make([]struct {
		Hour  int64
		Model string
	}, 0, len(modelSeries))
	for key := range modelSeries {
		modelKeys = append(modelKeys, key)
	}
	sort.Slice(modelKeys, func(i, j int) bool {
		if modelKeys[i].Hour != modelKeys[j].Hour {
			return modelKeys[i].Hour < modelKeys[j].Hour
		}
		return modelKeys[i].Model < modelKeys[j].Model
	})
	modelPoints := make([]ModelSeriesPoint, 0, len(modelKeys))
	for _, key := range modelKeys {
		modelPoints = append(modelPoints, ModelSeriesPoint{
			Hour:     time.Unix(key.Hour, 0).UTC().Format(time.RFC3339),
			Model:    key.Model,
			Counters: modelSeries[key],
		})
	}
	sourceValues := make([]string, 0, len(sources))
	for value := range sources {
		sourceValues = append(sourceValues, value)
	}
	sort.Strings(sourceValues)
	apiKeys := make([]APIKeyOption, 0, len(apiKeyRefs))
	for ref := range apiKeyRefs {
		generation, hash, _ := parseAPIKeyRef(ref)
		apiKeys = append(apiKeys, APIKeyOption{Ref: ref, Hash: hash, Generation: generation, Key: apiKeyCiphertexts[ref]})
	}
	sort.Slice(apiKeys, func(i, j int) bool { return apiKeys[i].Ref < apiKeys[j].Ref })

	return StatsResponse{
		SchemaVersion: 1,
		GeneratedAt:   now.UTC(),
		Range:         queryRange.Name,
		RetainedSince: since.UTC(),
		LastUsed:      lastUsed.UTC(),
		Summary:       summary,
		Groups:        groupRows,
		Series:        points,
		ModelSeries:   modelPoints,
		Sources:       sourceValues,
		APIKeys:       apiKeys,
	}
}


func compactModelName(model string) string {
	if model == "" {
		return "未标记模型"
	}
	return model
}

func sortedCompactModels(models map[string]Counters) []ModelStats {
	rows := make([]ModelStats, 0, len(models))
	for model, counters := range models {
		rows = append(rows, ModelStats{Model: model, Counters: counters, AverageLatencyNS: counters.averageLatencyNS(), AverageTTFTNS: counters.averageTTFTNS()})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalTokens != rows[j].TotalTokens {
			return rows[i].TotalTokens > rows[j].TotalTokens
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}

func initialStatsFromFull(stats StatsResponse, queryRange usageRange) InitialStatsResponse {
	models := make(map[string]Counters)
	for _, group := range stats.Groups {
		model := compactModelName(group.Model)
		value := models[model]
		value.add(group.Counters)
		models[model] = value
	}
	return InitialStatsResponse{
		SchemaVersion: 2,
		GeneratedAt:   stats.GeneratedAt,
		Range:         stats.Range,
		RetainedSince: stats.RetainedSince,
		LastUsed:      stats.LastUsed,
		Summary:       stats.Summary,
		Models:        sortedCompactModels(models),
		Sources:       stats.Sources,
		APIKeys:       stats.APIKeys,
	}
}

func sortedStatsAPIKeys(refs map[string]struct{}, ciphertexts map[string]string) []APIKeyOption {
	values := make([]APIKeyOption, 0, len(refs))
	for ref := range refs {
		generation, hash, _ := parseAPIKeyRef(ref)
		values = append(values, APIKeyOption{Ref: ref, Hash: hash, Generation: generation, Key: ciphertexts[ref]})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Ref < values[j].Ref })
	return values
}

func buildInitialStatsForRange(data map[aggregateKey]Counters, since, lastUsed time.Time, queryRange usageRange, filter usageFilter, now time.Time, apiKeyCiphertexts map[string]string) InitialStatsResponse {
	models := make(map[string]Counters)
	sources := make(map[string]struct{})
	apiKeyRefs := make(map[string]struct{})
	summary := Counters{}
	for key, counters := range data {
		bucketTime := time.Unix(key.Hour, 0).UTC()
		if (!queryRange.Start.IsZero() && bucketTime.Before(queryRange.Start)) || (!queryRange.End.IsZero() && !bucketTime.Before(queryRange.End)) {
			continue
		}
		dimensions := sanitizeDimensionsSource(key.Dimensions)
		if dimensions.Source != "" {
			sources[dimensions.Source] = struct{}{}
		}
		if ref := apiKeyRef(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
			apiKeyRefs[ref] = struct{}{}
		}
		if !filter.matches(dimensions) {
			continue
		}
		model := compactModelName(dimensions.Model)
		modelCounters := models[model]
		modelCounters.add(counters)
		models[model] = modelCounters
		summary.add(counters)
	}
	sourceValues := make([]string, 0, len(sources))
	for source := range sources {
		sourceValues = append(sourceValues, source)
	}
	sort.Strings(sourceValues)
	return InitialStatsResponse{SchemaVersion: 2, GeneratedAt: now.UTC(), Range: queryRange.Name, RetainedSince: since.UTC(), LastUsed: lastUsed.UTC(), Summary: summary, Models: sortedCompactModels(models), Sources: sourceValues, APIKeys: sortedStatsAPIKeys(apiKeyRefs, apiKeyCiphertexts)}
}

func buildGroupsForRange(data map[aggregateKey]Counters, queryRange usageRange, filter usageFilter, now time.Time, apiKeyCiphertexts map[string]string) GroupStatsPage {
	groups := make(map[Dimensions]Counters)
	for key, counters := range data {
		bucketTime := time.Unix(key.Hour, 0).UTC()
		if (!queryRange.Start.IsZero() && bucketTime.Before(queryRange.Start)) || (!queryRange.End.IsZero() && !bucketTime.Before(queryRange.End)) {
			continue
		}
		dimensions := sanitizeDimensionsSource(key.Dimensions)
		if !filter.matches(dimensions) {
			continue
		}
		group := groups[dimensions]
		group.add(counters)
		groups[dimensions] = group
	}
	items := make([]GroupStats, 0, len(groups))
	for dimensions, counters := range groups {
		if ref := apiKeyRef(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
			dimensions.APIKey = apiKeyCiphertexts[ref]
		}
		items = append(items, GroupStats{Dimensions: dimensions, Counters: counters, AverageLatencyNS: counters.averageLatencyNS(), AverageTTFTNS: counters.averageTTFTNS()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TotalTokens != items[j].TotalTokens {
			return items[i].TotalTokens > items[j].TotalTokens
		}
		return compareDimensions(items[i].Dimensions, items[j].Dimensions) < 0
	})
	return GroupStatsPage{SchemaVersion: 2, GeneratedAt: now.UTC(), Range: queryRange.Name, Items: items, Total: len(items)}
}

func queryCutoff(value string, now time.Time) (string, time.Time, error) {
	queryRange, err := presetUsageRange(value, now)
	return queryRange.Name, queryRange.Start, err
}

func presetUsageRange(value string, now time.Time) (usageRange, error) {
	now = now.UTC()
	switch value {
	case "", "24h":
		return usageRange{Name: "24h", Start: now.Add(-24 * time.Hour).Truncate(time.Minute)}, nil
	case "7d":
		return usageRange{Name: "7d", Start: now.Add(-7 * 24 * time.Hour).Truncate(time.Minute)}, nil
	case "30d":
		return usageRange{Name: "30d", Start: now.Add(-30 * 24 * time.Hour).Truncate(time.Minute)}, nil
	case "retention":
		return usageRange{Name: "retention"}, nil
	default:
		return usageRange{}, withStatus(400, "unsupported range %q", value)
	}
}

func usageRangeFromQuery(rangeValue, startValue, endValue string, now time.Time) (usageRange, error) {
	if startValue == "" && endValue == "" {
		return presetUsageRange(rangeValue, now)
	}
	if rangeValue != "" && rangeValue != "custom" {
		return usageRange{}, withStatus(400, "range must be custom when start and end are provided")
	}
	if startValue == "" || endValue == "" {
		return usageRange{}, withStatus(400, "start and end must be provided together")
	}
	start, err := time.Parse(time.RFC3339, startValue)
	if err != nil {
		return usageRange{}, withStatus(400, "invalid start time: %v", err)
	}
	end, err := time.Parse(time.RFC3339, endValue)
	if err != nil {
		return usageRange{}, withStatus(400, "invalid end time: %v", err)
	}
	start = start.UTC()
	end = end.UTC()
	if !start.Before(end) {
		return usageRange{}, withStatus(400, "start must be before end")
	}
	return usageRange{Name: "custom", Start: start, End: end}, nil
}

func (r usageRange) validate() error {
	if r.Name == "" {
		return fmt.Errorf("range name is required")
	}
	if !r.End.IsZero() && (r.Start.IsZero() || !r.Start.Before(r.End)) {
		return fmt.Errorf("invalid time range")
	}
	return nil
}

func compareDimensions(left, right Dimensions) int {
	for _, comparison := range []int{
		cmp.Compare(left.Provider, right.Provider),
		cmp.Compare(left.ExecutorType, right.ExecutorType),
		cmp.Compare(left.Model, right.Model),
		cmp.Compare(left.Alias, right.Alias),
		cmp.Compare(left.Source, right.Source),
		cmp.Compare(left.APIKeyGeneration, right.APIKeyGeneration),
		cmp.Compare(left.APIKeyHash, right.APIKeyHash),
		cmp.Compare(left.AuthType, right.AuthType),
		cmp.Compare(left.SessionKey, right.SessionKey),
		cmp.Compare(left.ReasoningEffort, right.ReasoningEffort),
		cmp.Compare(boolInt(left.Failed), boolInt(right.Failed)),
		cmp.Compare(left.FailureStatus, right.FailureStatus),
	} {
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
