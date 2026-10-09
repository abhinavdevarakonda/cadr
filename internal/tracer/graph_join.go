package tracer

import (
	"fmt"
	"strings"
)

// StaticFunc is a function as the static analyzer sees it: a name and a
// file:line. The dynamic side joins to it by line + file suffix, which is
// robust to relative/absolute path differences and to method-name formatting.
type StaticFunc struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// AddGraphAnomalies joins the dynamic summary to the static graph and appends
// anomalies: functions that ran but are absent from the graph, and (as a count)
// graph functions that never ran in this trace. maxSamples bounds the listed
// dynamic-only names.
func (s *Summary) AddGraphAnomalies(static []StaticFunc, maxSamples int) {
	if len(static) == 0 {
		return
	}
	if maxSamples <= 0 {
		maxSamples = 10
	}
	byLine := make(map[int][]string)
	for _, sf := range static {
		byLine[sf.Line] = append(byLine[sf.Line], sf.File)
	}
	matches := func(f FnStat) bool {
		for _, sf := range byLine[f.Line] {
			if sf != "" && strings.HasSuffix(f.File, sf) {
				return true
			}
		}
		return false
	}

	var dynamicOnly []string
	usedStatic := 0
	for _, f := range s.Functions {
		if f.File != "" && f.Line != 0 && matches(f) {
			usedStatic++
		} else if f.Fn != "" {
			dynamicOnly = append(dynamicOnly, f.Fn)
		}
	}
	for i, fn := range dynamicOnly {
		if i >= maxSamples {
			s.Anomalies = append(s.Anomalies, Anomaly{Kind: "dynamic_only", Detail: fmt.Sprintf("... and %d more functions not in the static graph", len(dynamicOnly)-maxSamples)})
			break
		}
		s.Anomalies = append(s.Anomalies, Anomaly{Kind: "dynamic_only", Detail: fn})
	}

	if unused := len(static) - usedStatic; unused > 0 {
		s.Anomalies = append(s.Anomalies, Anomaly{
			Kind:   "static_unused",
			Detail: fmt.Sprintf("%d of %d static functions never ran", unused, len(static)),
		})
	}
}
