package openai

import "strings"

const (
	openTag  = "<think>"
	closeTag = "</think>"
)

// thinkFilter drops <think>...</think> blocks from a stream of deltas, even
// when a tag is split across two deltas. Some reasoning models (e.g. Qwen3 on
// Groq) put their reasoning inline in the content.
type thinkFilter struct {
	in   bool
	buf  string
	trim bool // strip whitespace right after a closed block
}

// Push returns the part of the stream that is safe to show.
func (f *thinkFilter) Push(s string) string {
	f.buf += s
	var out strings.Builder
	for {
		if f.in {
			i := strings.Index(f.buf, closeTag)
			if i < 0 {
				f.buf = f.buf[len(f.buf)-partial(f.buf, closeTag):]
				break
			}
			f.buf = f.buf[i+len(closeTag):]
			f.in = false
			f.trim = true
			continue
		}
		i := strings.Index(f.buf, openTag)
		if i < 0 {
			k := partial(f.buf, openTag) // might be the start of "<think>"
			f.write(&out, f.buf[:len(f.buf)-k])
			f.buf = f.buf[len(f.buf)-k:]
			break
		}
		f.write(&out, f.buf[:i])
		f.buf = f.buf[i+len(openTag):]
		f.in = true
	}
	return out.String()
}

// Flush returns held-back text at the end of the stream. An unterminated
// think block is dropped.
func (f *thinkFilter) Flush() string {
	if f.in {
		f.buf = ""
		return ""
	}
	var out strings.Builder
	f.write(&out, f.buf)
	f.buf = ""
	return out.String()
}

func (f *thinkFilter) write(out *strings.Builder, s string) {
	if f.trim && s != "" {
		s = strings.TrimLeft(s, " \t\r\n")
		if s != "" {
			f.trim = false
		}
	}
	out.WriteString(s)
}

// partial is the length of the longest proper prefix of tag that s ends with.
func partial(s, tag string) int {
	for k := min(len(tag)-1, len(s)); k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
