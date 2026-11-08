package session

import (
	"fmt"
	"testing"
)

func TestListPagePaging(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 造 5 个会话，id 顺序与时间顺序一致
	for i := 0; i < 5; i++ {
		if err := s.Upsert(fmt.Sprintf("s%d", i), fmt.Sprintf("会话%d", i)); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(fmt.Sprintf("s%d", i), RoleUser, "hi"); err != nil {
			t.Fatal(err)
		}
	}

	page1, more, err := s.ListPage(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || !more {
		t.Fatalf("第一页应 2 条且有下一页，实际 %d 条 more=%v", len(page1), more)
	}
	// 最近的排最前
	if page1[0].ID != "s4" {
		t.Fatalf("第一页第一条应是最新的 s4，实际 %s", page1[0].ID)
	}

	page3, more3, err := s.ListPage(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 || more3 {
		t.Fatalf("最后一页应 1 条且没有下一页，实际 %d 条 more=%v", len(page3), more3)
	}
	if page3[0].ID != "s0" {
		t.Fatalf("最后一页应是 s0，实际 %s", page3[0].ID)
	}
}

func TestMessagesPageCursor(t *testing.T) {
	s, err := Open(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const total = 10
	for i := 0; i < total; i++ {
		if err := s.Append("c1", RoleUser, fmt.Sprintf("第%d条", i)); err != nil {
			t.Fatal(err)
		}
	}

	// 第一页：拿最近 4 条，且要按正序返回
	msgs, more, err := s.MessagesPage("c1", 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 || !more {
		t.Fatalf("应 4 条且有更早的，实际 %d more=%v", len(msgs), more)
	}
	if msgs[0].Content != "第6条" || msgs[3].Content != "第9条" {
		t.Fatalf("最新一页内容不对: %s … %s", msgs[0].Content, msgs[3].Content)
	}

	// 往前翻两页到底
	cursor := msgs[0].ID
	got := []string{}
	for {
		older, moreOlder, err := s.MessagesPage("c1", 4, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range older {
			got = append(got, m.Content)
		}
		if !moreOlder || len(older) == 0 {
			break
		}
		cursor = older[0].ID
	}

	// 补上第一页，应该正好是全部 10 条、不重不漏
	all := append(got, "第6条", "第7条", "第8条", "第9条")
	if len(all) != total {
		t.Fatalf("翻完应该是 %d 条，实际 %d 条: %v", total, len(all), all)
	}
	seen := map[string]bool{}
	for _, c := range all {
		if seen[c] {
			t.Fatalf("重复取到了 %s", c)
		}
		seen[c] = true
	}
}
