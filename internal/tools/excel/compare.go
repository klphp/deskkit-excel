package excel

// 两张表比对：新增、删除、修改。
//
// 典型场景：这个月的表 vs 上个月的表，或者「我改过的版本」vs「同事改过的版本」。
// 用户要的不是「不一样」，而是「哪些行是新增的、哪些被删了、哪几行的哪一列变了」。

import (
	"fmt"
	"strings"
)

// Change 一处变更。
type Change struct {
	Key    string `json:"key"`
	Column string `json:"column,omitempty"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// CompareResult 比对结果。
type CompareResult struct {
	Added          int        `json:"added"`
	Removed        int        `json:"removed"`
	Changed        int        `json:"changed"`
	Same           int        `json:"same"`
	AddedSamples   [][]string `json:"addedSamples,omitempty"`
	RemovedSamples [][]string `json:"removedSamples,omitempty"`
	ChangedSamples []Change   `json:"changedSamples,omitempty"`
	// AddedTable / RemovedTable 供导出成文件。
	AddedTable   *Table `json:"-"`
	RemovedTable *Table `json:"-"`
	ChangedTable *Table `json:"-"`
}

// Compare 按 key 列比对两张表。
func Compare(oldT, newT *Table, keys []string, compareCols []string, limit int) (*CompareResult, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("比对要指定 key 列（比如工号或订单号）")
	}
	if limit <= 0 {
		limit = 20
	}
	ok, err := oldT.Cols(keys)
	if err != nil {
		return nil, fmt.Errorf("旧表: %w", err)
	}
	nk, err := newT.Cols(keys)
	if err != nil {
		return nil, fmt.Errorf("新表: %w", err)
	}

	// 要比对的列：默认取两表共有且不在 key 里的列
	var cmpOld, cmpNew []int
	if len(compareCols) == 0 {
		for i, h := range oldT.Headers {
			if contains(keys, h) {
				continue
			}
			if j, err := newT.Col(h); err == nil {
				cmpOld = append(cmpOld, i)
				cmpNew = append(cmpNew, j)
			}
		}
	} else {
		cmpOld, err = oldT.Cols(compareCols)
		if err != nil {
			return nil, fmt.Errorf("旧表: %w", err)
		}
		cmpNew, err = newT.Cols(compareCols)
		if err != nil {
			return nil, fmt.Errorf("新表: %w", err)
		}
	}

	keyOf := func(t *Table, idx []int, row []string) string {
		parts := make([]string, len(idx))
		for k, i := range idx {
			parts[k] = strings.TrimSpace(cellAt(row, i))
		}
		return strings.Join(parts, "\x00")
	}

	oldMap := map[string][]string{}
	for _, r := range oldT.Rows {
		oldMap[keyOf(oldT, ok, r)] = r
	}
	newMap := map[string][]string{}
	for _, r := range newT.Rows {
		newMap[keyOf(newT, nk, r)] = r
	}

	res := &CompareResult{
		AddedTable:   &Table{Path: newT.Path, Sheet: newT.Sheet, Headers: append([]string(nil), newT.Headers...)},
		RemovedTable: &Table{Path: oldT.Path, Sheet: oldT.Sheet, Headers: append([]string(nil), oldT.Headers...)},
		ChangedTable: &Table{Path: newT.Path, Sheet: newT.Sheet, Headers: []string{"key", "列", "原值", "新值"}},
	}

	for _, r := range newT.Rows {
		k := keyOf(newT, nk, r)
		oldRow, ok := oldMap[k]
		if !ok {
			res.Added++
			res.AddedTable.Rows = append(res.AddedTable.Rows, r)
			if len(res.AddedSamples) < limit {
				res.AddedSamples = append(res.AddedSamples, r)
			}
			continue
		}
		rowChanged := false
		displayKey := strings.ReplaceAll(k, "\x00", " / ")
		for i := range cmpOld {
			before := strings.TrimSpace(cellAt(oldRow, cmpOld[i]))
			after := strings.TrimSpace(cellAt(r, cmpNew[i]))
			if before == after {
				continue
			}
			rowChanged = true
			ch := Change{Key: displayKey, Column: newT.Headers[cmpNew[i]], Before: before, After: after}
			if len(res.ChangedSamples) < limit {
				res.ChangedSamples = append(res.ChangedSamples, ch)
			}
			res.ChangedTable.Rows = append(res.ChangedTable.Rows, []string{displayKey, ch.Column, before, after})
		}
		if rowChanged {
			res.Changed++
		} else {
			res.Same++
		}
	}

	for _, r := range oldT.Rows {
		k := keyOf(oldT, ok, r)
		if _, ok := newMap[k]; !ok {
			res.Removed++
			res.RemovedTable.Rows = append(res.RemovedTable.Rows, r)
			if len(res.RemovedSamples) < limit {
				res.RemovedSamples = append(res.RemovedSamples, r)
			}
		}
	}
	return res, nil
}
