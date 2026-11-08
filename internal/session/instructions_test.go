package session

import "testing"

func TestInstructionsPerSession(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 两个会话各写各的，互不影响 —— 这正是「按会话存」的意义
	if err := s.SetInstructions("a", "处理结果存到 D:\\报表\\"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstructions("b", "下载存到 D:\\视频\\"); err != nil {
		t.Fatal(err)
	}

	gotA, err := s.Instructions("a")
	if err != nil || gotA != "处理结果存到 D:\\报表\\" {
		t.Fatalf("a 的指令不对: %q err=%v", gotA, err)
	}
	gotB, err := s.Instructions("b")
	if err != nil || gotB != "下载存到 D:\\视频\\" {
		t.Fatalf("b 的指令不对: %q err=%v", gotB, err)
	}

	// 没写过的会话返回空串，不报错
	empty, err := s.Instructions("c")
	if err != nil {
		t.Fatalf("读不存在的会话不该报错: %v", err)
	}
	if empty != "" {
		t.Fatalf("应为空串，实际 %q", empty)
	}
}

func TestInstructionsTooLong(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	long := make([]rune, MaxInstructions+1)
	for i := range long {
		long[i] = 'あ'
	}
	if err := s.SetInstructions("x", string(long)); err == nil {
		t.Fatal("超长指令应该被拒绝")
	}
}
