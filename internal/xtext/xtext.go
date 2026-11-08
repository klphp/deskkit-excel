// Package xtext 判断纯文本文件的编码与列分隔符。
//
// 为什么单独抽出来：这判断有两处要用 —— 文件工具的 fs_peek（告诉模型
// 「这个文件是什么」）和表格工具的 CSV 读取（决定怎么解码、按什么切列）。
// 两处各写一遍必然走偏：一处认 GBK 另一处不认，就会出现「看一眼是正常的、
// 真读进来全是乱码」这种最难查的问题。
package xtext

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 识别结果（作为编码名返回，界面上直接显示）。
const (
	EncUTF8    = "utf-8"
	EncUTF8BOM = "utf-8-bom"
	EncUTF16LE = "utf-16le"
	EncUTF16BE = "utf-16be"
	EncGB18030 = "gb18030"
	EncBinary  = "binary"
	EncUnknown = "unknown"
)

// Detect 判断编码并把内容转成 UTF-8。
// isText=false 表示看起来是二进制（含 NUL 字节，或两种解码都失败）。
func Detect(b []byte) (text string, enc string, isText bool) {
	// ① BOM 最可靠
	if len(b) >= 3 && bytes.Equal(b[:3], []byte{0xEF, 0xBB, 0xBF}) {
		return string(b[3:]), EncUTF8BOM, true
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16(b[2:], binary.LittleEndian), EncUTF16LE, true
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16(b[2:], binary.BigEndian), EncUTF16BE, true
	}

	// ② 出现 NUL 基本就是二进制
	if bytes.IndexByte(b, 0) >= 0 {
		return "", EncBinary, false
	}

	// ③ 合法 UTF-8 就当 UTF-8
	if utf8.Valid(b) {
		return string(b), EncUTF8, true
	}

	// ④ 剩下的按 GB18030 解（GBK 是它的子集）。
	//    国内导出的报表、银行流水绝大多数是这种。
	if decoded, err := GB18030ToUTF8(b); err == nil {
		return decoded, EncGB18030, true
	}

	return "", EncUnknown, false
}

// GB18030ToUTF8 转码。单独导出是因为写入 CSV 时也可能用到反向操作。
func GB18030ToUTF8(b []byte) (string, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// UTF8ToGB18030 反向转码（导出给只认 GBK 的老系统时用）。
func UTF8ToGB18030(s string) ([]byte, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(s))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GuessDelimiter 在前几行里数候选分隔符，取「每行出现次数一致且最多」的那个。
//
// 一致性是关键：Markdown 的竖线、正文里的逗号都可能出现一次，
// 但表格的每一行出现次数是稳定的。
func GuessDelimiter(lines []string) string {
	cands := []string{",", ";", "\t", "|"}
	var sample []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		sample = append(sample, l)
		if len(sample) == 5 {
			break
		}
	}
	if len(sample) < 1 {
		return ""
	}

	best, bestCount, bestStable := "", 0, false
	for _, d := range cands {
		counts := make([]int, 0, len(sample))
		for _, l := range sample {
			counts = append(counts, strings.Count(l, d))
		}
		minC, maxC := counts[0], counts[0]
		for _, c := range counts[1:] {
			if c < minC {
				minC = c
			}
			if c > maxC {
				maxC = c
			}
		}
		stable := minC >= 1 && minC == maxC
		if (stable && !bestStable) || (stable == bestStable && maxC > bestCount) {
			best, bestCount, bestStable = d, maxC, stable
		}
	}
	if bestCount == 0 {
		return ""
	}
	return best
}

func decodeUTF16(b []byte, order binary.ByteOrder) string {
	n := len(b) / 2
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		u[i] = order.Uint16(b[i*2:])
	}
	var sb strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) {
			low := u[i+1]
			if low >= 0xDC00 && low <= 0xDFFF {
				sb.WriteRune(rune(0x10000 + (int32(c)-0xD800)<<10 + (int32(low) - 0xDC00)))
				i++
				continue
			}
		}
		sb.WriteRune(rune(c))
	}
	return strings.TrimPrefix(sb.String(), "\uFEFF")
}
