package config

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 这个测试盯的是「Windows 上双击有没有反应」。
//
// 事故：exe 内嵌的 manifest 里 assemblyIdentity 写成 version="0.1.0"（三段），
// Windows 要求四段（1.0.0.0），激活上下文生成失败 → 双击毫无反应、也没有任何提示，
// 事件日志里才有 "assemblyIdentity 的 version 无效"。发了三个版本都中招。
//
// 这类错误编译不报、测试不报、CI 不报，只有用户双击时才暴露。
// 所以在这里守住源头文件。
func TestWindowsManifestIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "build", "windows", "wails.exe.manifest")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 manifest（路径变了就更新这个测试）：%v", err)
	}
	src := string(raw)

	var doc struct {
		AssemblyIdentity struct {
			Name    string `xml:"name,attr"`
			Version string `xml:"version,attr"`
		} `xml:"assemblyIdentity"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("manifest 不是合法 XML：%v", err)
	}

	ver := doc.AssemblyIdentity.Version
	// 四段：数字.数字.数字.数字
	fourPart := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	if !fourPart.MatchString(ver) {
		t.Fatalf("assemblyIdentity 的 version=%q 不是四段格式。\n"+
			"Windows 会拒绝生成激活上下文，exe 双击毫无反应（事件日志报 version 无效）。\n"+
			"改成 1.0.0.0 这种四段写法。", ver)
	}

	if doc.AssemblyIdentity.Name == "" {
		t.Fatal("assemblyIdentity 缺少 name")
	}
	// 模块名别再带模板痕迹
	if strings.Contains(doc.AssemblyIdentity.Name, "wails3base") {
		t.Errorf("manifest 的模块名还是模板默认值 %q，改成自己的", doc.AssemblyIdentity.Name)
	}

	// DPI 与提权级别：都影响真实显示效果，缺了会出现模糊或不该弹的 UAC
	if !strings.Contains(src, "dpiAwareness") {
		t.Error("manifest 缺 dpiAwareness，高分屏下界面会糊")
	}
	if !strings.Contains(src, "asInvoker") {
		t.Error("manifest 缺 requestedExecutionLevel asInvoker（不写时部分系统会按默认策略处理）")
	}
}
