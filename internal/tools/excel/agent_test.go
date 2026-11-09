package excel

// 这一组测试要回答一个问题：AI 到底能不能真的指挥这些工具？
//
// 不接真模型（没有 key、结果不确定、跑一次要几十秒），而是起一个假的
// OpenAI 兼容服务，按真实协议返回 tool_calls。这样验证的是我们这边
// 最容易出错的环节：工具定义能不能被模型读懂、参数能不能传进来、
// 执行结果能不能回灌、循环能不能收敛。
//
// 假服务按顺序回三步：list_tables → query_sheets → 最终结论。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/ai"
	"github.com/klphp/deskkit-excel/internal/config"
)

// mockModel 一个会按剧本调用工具的假模型。
type mockModel struct {
	step  int
	seen  []map[string]any // 每次收到的请求体，用来断言工具定义与回灌
	dir   string
	final string
}

func (m *mockModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.seen = append(m.seen, body)

	reply := func(message map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "mock",
			"choices": []any{map[string]any{
				"message":       message,
				"finish_reason": "stop",
			}},
		})
	}

	toolCall := func(name, args string) map[string]any {
		return map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []any{map[string]any{
				"id":   fmt.Sprintf("call_%d", m.step),
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": args,
				},
			}},
		}
	}

	m.step++
	switch m.step {
	case 1:
		reply(toolCall("list_tables", fmt.Sprintf(`{"dir":%q}`, m.dir)))
	case 2:
		// 根据上一步真实返回的表名写 SQL —— 假模型也得"看一眼"再写，
		// 否则测试会掩盖「表名拿不到」这类问题。
		reply(toolCall("query_sheets", fmt.Sprintf(
			`{"dir":%q,"sql":"SELECT 部门, SUM(金额) AS 合计 FROM \"销售部_10月_Sheet1\" GROUP BY 部门"}`, m.dir)))
	default:
		reply(map[string]any{"role": "assistant", "content": m.final})
	}
}

func TestAgentDrivesExcelTools(t *testing.T) {
	dir := queryFixture(t)
	model := &mockModel{
		dir:   dir,
		final: "销售部合计 3214.5，来自 3 条记录。",
	}
	srv := httptest.NewServer(model)
	defer srv.Close()

	// 客户端直接指向假服务
	client := ai.NewClient("mock", config.Provider{
		BaseURL: srv.URL,
		Model:   "mock",
	}, "test-key")
	if client == nil {
		t.Fatal("客户端创建失败")
	}

	registry := ai.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}
	// 至少要有：扫描、合并、清洗、SQL 三类能力
	if registry.Len() < 8 {
		t.Fatalf("工具太少（%d 个），AI 能做的事就被限死了", registry.Len())
	}

	loop := ai.NewLoop(client, registry)
	var events []ai.Event
	resp, aiErr := loop.Run(context.Background(), []ai.Message{
		{Role: ai.RoleSystem, Content: "当前输入目录：" + dir},
		{Role: ai.RoleUser, Content: "销售部的金额合计是多少？"},
	}, func(ev ai.Event) { events = append(events, ev) })

	if aiErr != nil {
		t.Fatalf("循环失败: %v", aiErr.Message)
	}
	if resp == nil || !strings.Contains(resp.Content, "3214.5") {
		t.Fatalf("最终回答不对: %+v", resp)
	}

	// ① 请求里必须带工具定义 —— 不带的话模型根本不知道有这些能力
	if len(model.seen) < 3 {
		t.Fatalf("应至少请求 3 次（列表→查询→结论），实际 %d", len(model.seen))
	}
	tools, _ := model.seen[0]["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("第一次请求没有带 tools 定义")
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		fn := tool["function"].(map[string]any)
		names[fn["name"].(string)] = true
		params, _ := fn["parameters"].(map[string]any)
		if params == nil || params["type"] != "object" {
			t.Errorf("工具 %v 的参数不是合法 JSON Schema", fn["name"])
		}
	}
	for _, want := range []string{"scan_excel_folder", "preview_merge", "run_merge", "preview_clean", "run_clean", "list_tables", "query_sheets", "write_query_result"} {
		if !names[want] {
			t.Errorf("缺少工具 %q", want)
		}
	}

	// ② 工具结果必须回灌给模型，否则它就是在瞎猜
	last := model.seen[len(model.seen)-1]
	msgs, _ := last["messages"].([]any)
	var sawToolResult bool
	for _, raw := range msgs {
		msg := raw.(map[string]any)
		if msg["role"] == "tool" {
			sawToolResult = true
			if content, _ := msg["content"].(string); content == "" {
				t.Error("工具结果为空，模型拿不到数据")
			}
		}
	}
	if !sawToolResult {
		t.Error("历史里没有 role=tool 的回灌消息")
	}

	// ③ 事件流要让界面能显示「它在干什么」
	var calls, results int
	for _, ev := range events {
		switch ev.Type {
		case ai.EventToolCall:
			calls++
			if ev.Tool == "" {
				t.Error("tool_call 事件没有工具名")
			}
		case ai.EventToolResult:
			results++
		}
	}
	if calls < 2 || results < 2 {
		t.Fatalf("步骤事件不全：call=%d result=%d", calls, results)
	}
}

// 工具失败时循环不能崩，要把错误回灌让模型自己解释。
func TestAgentSurvivesToolError(t *testing.T) {
	dir := t.TempDir()
	model := &mockModel{
		dir:   dir,
		final: "这个目录里没有表格。",
	}
	srv := httptest.NewServer(model)
	defer srv.Close()

	client := ai.NewClient("mock", config.Provider{BaseURL: srv.URL, Model: "mock"}, "k")
	registry := ai.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}

	resp, aiErr := ai.NewLoop(client, registry).Run(context.Background(), []ai.Message{
		{Role: ai.RoleUser, Content: "看看有哪些表"},
	}, nil)
	if aiErr != nil {
		t.Fatalf("目录为空不应报错: %v", aiErr.Message)
	}
	if resp == nil {
		t.Fatal("没有返回")
	}
}

// 表名要能被模型从 list_tables 读到 —— 中文、破折号都要能对上。
func TestCatalogNamesUsableInSQL(t *testing.T) {
	dir := queryFixture(t)
	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	for _, ti := range cat.Tables {
		q := fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, ti.Table)
		if _, err := Query(ctx, db, q, 1); err != nil {
			t.Errorf("表名 %q 无法直接用于 SQL: %v", ti.Table, err)
		}
	}
}

// 现场造一份"用户真实会遇到"的脏数据，跑一遍"AI 会怎么用它"：
// 先看结构，再分组汇总，最后写文件。
func TestEndToEndAgentStyleWorkflow(t *testing.T) {
	dir := t.TempDir()
	mkxlsx(t, dir, "明细-1月.xlsx", []string{"日期", "客户", "金额"}, [][]string{
		{"2026-01-05", "甲公司", "¥1,000"},
		{"2026-01-20", "乙公司", "500"},
	})
	mkxlsx(t, dir, "明细-2月.xlsx", []string{"日期", "客户", "金额"}, [][]string{
		{"2026-02-03", "甲公司", "2,000"},
		{"2026-02-11", "丙公司", "800"},
	})

	ctx := context.Background()
	db, cat, err := LoadCatalog(ctx, dir)
	if err != nil {
		t.Fatalf("载入失败: %v", err)
	}
	defer db.Close()

	if len(cat.Tables) != 2 {
		t.Fatalf("应看到 2 张表，实际 %d", len(cat.Tables))
	}

	// 跨文件汇总，正是用户口中"这些表加起来"的意思
	union := fmt.Sprintf(
		`SELECT 客户, SUM(金额) AS 合计 FROM (SELECT 客户, 金额 FROM "%s" UNION ALL SELECT 客户, 金额 FROM "%s") GROUP BY 客户 ORDER BY 合计 DESC`,
		cat.Tables[0].Table, cat.Tables[1].Table)
	res, err := Query(ctx, db, union, 0)
	if err != nil {
		t.Fatalf("跨表查询失败: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("应有 3 个客户，实际 %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "甲公司" || res.Rows[0][1] != "3000" {
		t.Fatalf("甲公司应为 3000，实际 %v", res.Rows[0])
	}

	outDir := filepath.Join(dir, "deskkit-out")
	path, rows, err := WriteQuery(ctx, db, union, outDir, "客户汇总.xlsx")
	if err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if rows != 3 {
		t.Fatalf("应写 3 行，实际 %d", rows)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("输出不存在: %v", err)
	}
}
