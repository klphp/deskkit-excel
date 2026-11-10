// Package xtext 判断纯文本文件的编码，并把它转成 UTF-8。
//
// 这里不自己实现识别算法：用 github.com/saintfish/chardet（Mozilla chardet 的
// Go 移植）做判断，用 golang.org/x/text/encoding/htmlindex 按名字取解码器。
// 自己按字节猜的结果是「只认得自己测过的那几种」，而用户导出的文件可能来自
// 任何系统（GBK / Big5 / Shift-JIS / EUC-KR 都见过）。
//
// 我们只保留三件 chardet 不做或做得不够的事：
//
//	· BOM 精确识别（要知道有没有 BOM —— 写到 Windows 上有没有它决定乱不乱码）
//	· UTF-8 快速通道（文件本身是合法 UTF-8 就直接用，不给误判留机会）
//	· 二进制判定（含 NUL 字节就不是文本）
//
// 分隔符判断不在这里：那是 csvd（github.com/jfyne/csvd）的事，
// 它按「切出来的列数分布」判断，比数逗号出现次数可靠。
package xtext

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
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

// minConfidence chardet 对短样本会给一堆候选；置信度低于这个值就当没认出来。
const minConfidence = 40

// Detect 判断编码并把内容转成 UTF-8。
// isText=false 表示看起来是二进制（含 NUL 字节，或所有解码都失败）。
func Detect(b []byte) (text string, enc string, isText bool) {
	// ① BOM 最可靠，先看
	if len(b) >= 3 && bytes.Equal(b[:3], []byte{0xEF, 0xBB, 0xBF}) {
		return string(b[3:]), EncUTF8BOM, true
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16(b[2:], binary.LittleEndian), EncUTF16LE, true
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16(b[2:], binary.BigEndian), EncUTF16BE, true
	}

	// ② 出现 NUL 基本就是二进制（UTF-16 已在上一步处理）
	if bytes.IndexByte(b, 0) >= 0 {
		return "", EncBinary, false
	}

	// ③ 本身合法 UTF-8 就直接用。「能通过 UTF-8 校验」是很强的证据，
	//    比让 chardet 在短样本上犹豫可靠。
	if utf8.Valid(b) {
		return string(b), EncUTF8, true
	}

	// ④ 交给 chardet：gb18030 / big5 / shift_jis / euc-kr 都在它的能力范围内
	if res, err := chardet.NewTextDetector().DetectBest(b); err == nil &&
		res != nil && res.Confidence >= minConfidence {
		if name := normalizeCharset(res.Charset); name != "" {
			if decoded, err := decodeWith(b, name); err == nil {
				return decoded, name, true
			}
		}
	}

	// ⑤ chardet 也没把握：按 GB18030 兜底。国内导出的报表绝大多数是它，
	//    解不动说明真不是文本。
	if decoded, err := GB18030ToUTF8(b); err == nil && utf8.ValidString(decoded) {
		return decoded, EncGB18030, true
	}

	return "", EncUnknown, false
}

// normalizeCharset 把 chardet 给的名字换成本项目里的统一叫法。
func normalizeCharset(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "":
		return ""
	case "utf-8", "utf8", "ascii", "us-ascii":
		return EncUTF8
	case "gb-18030", "gb18030":
		return EncGB18030
	case "gbk", "gb2312", "gb_2312-80", "windows-936", "cp936", "x-gbk":
		return EncGB18030 // 统一按超集解码，GBK 是它的子集
	case "utf-16le":
		return EncUTF16LE
	case "utf-16be":
		return EncUTF16BE
	}
	return n
}

// decodeWith 按名字取解码器转成 UTF-8。
func decodeWith(b []byte, name string) (string, error) {
	e, err := htmlindex.Get(name)
	if err != nil {
		return "", err
	}
	out, _, err := transform.Bytes(e.NewDecoder(), b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GB18030ToUTF8 转码。单独导出是因为写入 CSV 时也可能用到反向操作。
func GB18030ToUTF8(b []byte) (string, error) {
	out, _, err := transform.Bytes(htmlindexMust("gb18030").NewDecoder(), b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// UTF8ToGB18030 反向转码（导出给只认 GBK 的老系统时用）。
func UTF8ToGB18030(s string) ([]byte, error) {
	out, _, err := transform.Bytes(htmlindexMust("gb18030").NewEncoder(), []byte(s))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// htmlindexMust gb18030 一定存在，取不到说明 x/text 出了问题，早点炸掉比静默出错好。
func htmlindexMust(name string) encoding.Encoding {
	e, err := htmlindex.Get(name)
	if err != nil {
		panic("x/text is missing encoding " + name + ": " + err.Error())
	}
	return e
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
