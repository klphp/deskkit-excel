package packs

import (
	"context"
	"strings"
	"testing"

	"github.com/klphp/deskkit-excel/internal/ai"
)

// v1 只开放表格处理：配置写什么，注册和界面就只看到什么。
func TestSetEnabledFilters(t *testing.T) {
	defer SetEnabled(nil)

	SetEnabled([]string{"excel"})
	got := Enabled()
	if len(got) != 1 || got[0].ID != "excel" {
		t.Fatalf("只应启用 excel，实际 %+v", got)
	}

	reg := ai.NewRegistry()
	loaded, err := Register(reg, []string{"excel"})
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != "excel" {
		t.Fatalf("注册的能力包不对: %+v", loaded)
	}
	names := map[string]bool{}
	for _, d := range reg.Definitions() {
		names[d.Function.Name] = true
	}
	if !names["inspect_file"] || !names["write_file_result"] {
		t.Error("表格工具没注册上")
	}
	if names["fs_delete"] || names["fs_list"] {
		t.Error("文件操作不该出现在 v1 的工具表里")
	}
	// 元工具始终在，模型才有办法查用法
	if !names["describe_capability"] {
		t.Error("缺少 describe_capability")
	}
}

// 空清单 = 全部（开发期用），别让配置里漏写一项就什么功能都没有。
func TestEmptyEnabledMeansAll(t *testing.T) {
	defer SetEnabled(nil)
	SetEnabled(nil)
	if len(Enabled()) != len(All()) {
		t.Fatalf("空清单应等于全部：%d vs %d", len(Enabled()), len(All()))
	}
}

// 配置里写错的包名忽略即可，不能让应用起不来。
func TestUnknownPackIgnored(t *testing.T) {
	defer SetEnabled(nil)
	reg := ai.NewRegistry()
	loaded, err := Register(reg, []string{"excel", "根本不存在"})
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("只有 excel 会生效，实际 %+v", loaded)
	}
}

func TestMetaToolDescribesUsage(t *testing.T) {
	defer SetEnabled(nil)
	reg := ai.NewRegistry()
	if _, err := Register(reg, []string{"excel"}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	out, err := reg.Execute(context.Background(), "describe_capability", `{"id":"excel"}`)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	for _, want := range []string{"Spreadsheets", "inspect_file", "Parameters"} {
		if !strings.Contains(out, want) {
			t.Errorf("用法说明缺少 %q", want)
		}
	}

	// 查不到时要把可用的 id 列出来，模型才知道该怎么改
	out, _ = reg.Execute(context.Background(), "describe_capability", `{"id":"nope"}`)
	if !strings.Contains(out, "excel") {
		t.Errorf("应提示可用能力包: %s", out)
	}
}

func TestAllPacksDeclareIdentity(t *testing.T) {
	for _, p := range All() {
		if p.ID == "" || p.Name == "" || p.Description == "" {
			t.Errorf("能力包缺少自我描述: %+v", p)
		}
		if p.Register == nil {
			t.Errorf("%s 没有注册函数", p.ID)
		}
	}
}
