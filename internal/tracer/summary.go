package tracer

import "sort"

// FnStat is the per-function aggregate over a run.
type FnStat struct {
	Fn    string `json:"fn"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Sym   string `json:"sym,omitempty"`
	Count int    `json:"count"`
	Total int64  `json:"total_ns"`
	Self  int64  `json:"self_ns"`
	Avg   int64  `json:"avg_ns"`
	P50   int64  `json:"p50_ns"`
	P95   int64  `json:"p95_ns"`
	Max   int64  `json:"max_ns"`
	First int    `json:"first_index"`
	Last  int    `json:"last_index"`
}

// CtxStat is the per-execution-context aggregate.
type CtxStat struct {
	Ctx      string `json:"ctx"`
	Root     string `json:"root"`
	Duration int64  `json:"duration_ns"`
	Children int    `json:"children"`
}

// EdgeStat is a dynamic parent->child call count, the join point with the
// static call graph.
type EdgeStat struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
	Count  int    `json:"count"`
}

// Anomaly flags something suspicious about a run.
type Anomaly struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Summary is cadr's per-run aggregate. It is the stable shape returned by
// `cadr trace summary --json` and the `trace_summary` MCP tool.
type Summary struct {
	Schema     int        `json:"schema"`
	EventCount int        `json:"event_count"`
	EnterCount int        `json:"enter_count"`
	ExitCount  int        `json:"exit_count"`
	DurationNs int64      `json:"duration_ns"`
	Functions  []FnStat   `json:"functions"`
	Contexts   []CtxStat  `json:"contexts"`
	Edges      []EdgeStat `json:"edges"`
	Anomalies  []Anomaly  `json:"anomalies,omitempty"`
}

type fnAgg struct {
	stat FnStat
	durs []int64
}

// Summarize builds per-function, per-context and dynamic-edge aggregates from
// an ordered event stream.
func Summarize(events []Event) *Summary {
	sum := &Summary{Schema: SchemaVersion, EventCount: len(events)}
	forest := BuildForest(events)

	for _, e := range events {
		if NormalizeEvent(e).Ev == EvExit {
			sum.ExitCount++
		} else {
			sum.EnterCount++
		}
	}

	aggs := make(map[string]*fnAgg)
	for idx, row := range forest.Flat {
		n := row.Node
		key := row.Event.Name + "\x00" + row.Event.File + "\x00" + itoa(row.Event.Line)
		agg, ok := aggs[key]
		if !ok {
			agg = &fnAgg{stat: FnStat{
				Fn:    row.Event.Name,
				File:  row.Event.File,
				Line:  row.Event.Line,
				Sym:   row.Event.Sym,
				First: idx,
			}}
			aggs[key] = agg
		}
		agg.stat.Count++
		agg.stat.Last = idx
		if n != nil {
			agg.stat.Total += n.Dur
			agg.stat.Self += n.Self
			agg.durs = append(agg.durs, n.Dur)
		}
	}

	keys := make([]string, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		agg := aggs[k]
		if agg.stat.Count > 0 {
			agg.stat.Avg = agg.stat.Total / int64(agg.stat.Count)
		}
		agg.stat.P50 = percentile(agg.durs, 50)
		agg.stat.P95 = percentile(agg.durs, 95)
		agg.stat.Max = maxOf(agg.durs)
		sum.Functions = append(sum.Functions, agg.stat)
	}

	nodeName := func(n *TreeNode) string {
		if n == nil {
			return ""
		}
		return n.Event.Name
	}
	edges := make(map[string]int)
	for _, ct := range forest.Contexts {
		var root *TreeNode
		var count int
		for _, r := range ct.Roots {
			if root == nil || r.Dur > root.Dur {
				root = r
			}
		}
		var rec func(*TreeNode)
		rec = func(n *TreeNode) {
			count++
			for _, c := range n.Children {
				edges[nodeName(n)+"\x00"+nodeName(c)]++
				rec(c)
			}
		}
		for _, r := range ct.Roots {
			rec(r)
		}
		cs := CtxStat{Ctx: ct.Ctx, Children: count}
		if root != nil {
			cs.Root = root.Event.Name
			cs.Duration = root.Dur
		}
		if cs.Duration > sum.DurationNs {
			sum.DurationNs = cs.Duration
		}
		sum.Contexts = append(sum.Contexts, cs)
	}
	edgeKeys := make([]string, 0, len(edges))
	for k := range edges {
		edgeKeys = append(edgeKeys, k)
	}
	sort.Strings(edgeKeys)
	for _, k := range edgeKeys {
		parts := splitNul(k)
		sum.Edges = append(sum.Edges, EdgeStat{Parent: parts[0], Child: parts[1], Count: edges[k]})
	}

	// Anomalies: open spans, dropped events (seq gaps), orphan exits.
	for _, n := range forest.OpenSpans() {
		sum.Anomalies = append(sum.Anomalies, Anomaly{
			Kind:   "open_span",
			Detail: n.Event.Name,
		})
	}
	sum.Anomalies = append(sum.Anomalies, droppedAnomalies(events)...)
	return sum
}

// emitterNamespaceBits separates seq spaces by emitter instance. The shared Go
// runtime and Python/legacy streams use namespace 0; the legacy per-package Go
// fallback seeds its counters above it (toolwrap.PackageNamespace).
const emitterNamespaceBits = 40

// droppedAnomalies reports missing seq values per (pid, emitter namespace).
func droppedAnomalies(events []Event) []Anomaly {
	n := droppedCount(events)
	if n == 0 {
		return nil
	}
	return []Anomaly{{Kind: "dropped_events", Detail: itoa(n) + " events missing"}}
}

// droppedCount returns the number of missing seq values per (pid, emitter
// namespace). It is set-based, not order-based, because concurrent emitters can
// interleave seq values across the channel. Legacy streams (seq==0) are skipped.
func droppedCount(events []Event) int {
	const mask = (int64(1) << emitterNamespaceBits) - 1
	type key struct {
		pid int
		ns  int64
	}
	sets := make(map[key]map[int64]struct{})
	rng := make(map[key][2]int64)
	for _, e := range events {
		e = NormalizeEvent(e)
		if e.Seq == 0 {
			continue
		}
		k := key{pid: e.PID, ns: e.Seq >> emitterNamespaceBits}
		low := e.Seq & mask
		if sets[k] == nil {
			sets[k] = make(map[int64]struct{})
			rng[k] = [2]int64{low, low}
		}
		sets[k][low] = struct{}{}
		if low < rng[k][0] {
			rng[k] = [2]int64{low, rng[k][1]}
		}
		if low > rng[k][1] {
			rng[k] = [2]int64{rng[k][0], low}
		}
	}
	total := 0
	for k, set := range sets {
		expected := rng[k][1] - rng[k][0] + 1
		if miss := expected - int64(len(set)); miss > 0 {
			total += int(miss)
		}
	}
	return total
}

// AddAnomaly appends an anomaly (used by the CLI/MCP to add static-graph checks).
func (s *Summary) AddAnomaly(kind, detail string) {
	s.Anomalies = append(s.Anomalies, Anomaly{Kind: kind, Detail: detail})
}

// FnStatFor returns the aggregate for fn, if present.
func (s *Summary) FnStatFor(fn string) (FnStat, bool) {
	for _, f := range s.Functions {
		if f.Fn == fn {
			return f, true
		}
	}
	return FnStat{}, false
}

// FnDelta is a per-function change between two runs.
type FnDelta struct {
	Fn     string `json:"fn"`
	CountA int    `json:"count_a"`
	CountB int    `json:"count_b"`
	TotalA int64  `json:"total_a_ns"`
	TotalB int64  `json:"total_b_ns"`
	SelfA  int64  `json:"self_a_ns"`
	SelfB  int64  `json:"self_b_ns"`
}

// DiffResult compares two runs.
type DiffResult struct {
	A               string    `json:"a"`
	B               string    `json:"b"`
	Added           []string  `json:"added"`
	Removed         []string  `json:"removed"`
	Functions       []FnDelta `json:"functions"`
	FirstDivergence int       `json:"first_divergence"` // enter index, -1 when identical
	FirstDivergeA   *Event    `json:"first_divergence_a,omitempty"`
	FirstDivergeB   *Event    `json:"first_divergence_b,omitempty"`
	OrderChanged    bool      `json:"order_changed"`
}

// Diff computes per-function deltas, added/removed functions and the first
// point at which the two enter sequences diverge.
func Diff(a, b []Event) *DiffResult {
	res := &DiffResult{FirstDivergence: -1}
	sa := Summarize(a)
	sb := Summarize(b)

	index := func(s *Summary) map[string]FnStat {
		m := make(map[string]FnStat)
		for _, f := range s.Functions {
			m[f.Fn] = f
		}
		return m
	}
	ma, mb := index(sa), index(sb)

	all := make(map[string]bool)
	for k := range ma {
		all[k] = true
	}
	for k := range mb {
		all[k] = true
	}
	names := make([]string, 0, len(all))
	for k := range all {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		fa, oka := ma[name]
		fb, okb := mb[name]
		switch {
		case !okb:
			res.Removed = append(res.Removed, name)
		case !oka:
			res.Added = append(res.Added, name)
		}
		res.Functions = append(res.Functions, FnDelta{
			Fn:     name,
			CountA: fa.Count, CountB: fb.Count,
			TotalA: fa.Total, TotalB: fb.Total,
			SelfA: fa.Self, SelfB: fb.Self,
		})
	}

	ea := EnterEvents(a)
	eb := EnterEvents(b)
	min := len(ea)
	if len(eb) < min {
		min = len(eb)
	}
	for i := 0; i < min; i++ {
		if ea[i].Name != eb[i].Name {
			res.FirstDivergence = i
			ae, be := ea[i], eb[i]
			res.FirstDivergeA = &ae
			res.FirstDivergeB = &be
			break
		}
	}
	if res.FirstDivergence == -1 && len(ea) != len(eb) {
		res.FirstDivergence = min
		if min < len(ea) {
			ae := ea[min]
			res.FirstDivergeA = &ae
		} else {
			be := eb[min]
			res.FirstDivergeB = &be
		}
	}
	if res.FirstDivergence >= 0 && len(res.Added) == 0 && len(res.Removed) == 0 {
		res.OrderChanged = true
	}
	return res
}

func percentile(sortedDurs []int64, p int) int64 {
	if len(sortedDurs) == 0 {
		return 0
	}
	d := make([]int64, len(sortedDurs))
	copy(d, sortedDurs)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	idx := (p * len(d)) / 100
	if idx >= len(d) {
		idx = len(d) - 1
	}
	return d[idx]
}

func maxOf(v []int64) int64 {
	var m int64
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

func splitNul(s string) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
