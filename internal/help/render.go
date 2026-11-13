package help

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
)

var headingSlug = regexp.MustCompile(`[^a-z0-9]+`)

type listFrame struct {
	kind  string // "ol" or "ul"
	depth int
}

type mdRenderer struct {
	out       strings.Builder
	frames    []listFrame
	paragraph []string
	liOpen    bool
}

// renderHelpMarkdown turns household-safe scanner help into an HTML fragment.
func renderHelpMarkdown(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	r := &mdRenderer{}
	for _, raw := range strings.Split(source, "\n") {
		r.renderLine(raw)
	}
	r.closeAllLists()
	r.flushParagraph()
	return r.out.String()
}

func (r *mdRenderer) renderLine(raw string) {
	depth, line := lineDepth(raw)
	if line == "" {
		r.closeAllLists()
		r.flushParagraph()
		r.paragraph = nil
		return
	}
	if strings.HasPrefix(line, "#") {
		r.closeAllLists()
		r.flushParagraph()
		r.paragraph = nil
		level := 0
		for level < len(line) && line[level] == '#' {
			level++
		}
		if level > len(line) || line[level] != ' ' {
			r.paragraph = append(r.paragraph, line)
			return
		}
		text := strings.TrimSpace(line[level:])
		id := headingSlug.ReplaceAllString(strings.ToLower(text), "-")
		id = strings.Trim(id, "-")
		r.out.WriteString(fmt.Sprintf("<h%d id=%q>", level, html.EscapeString(id)))
		r.out.WriteString(inlineMarkup(text))
		r.out.WriteString(fmt.Sprintf("</h%d>", level))
		return
	}
	if strings.HasPrefix(line, "---") {
		r.closeAllLists()
		r.flushParagraph()
		r.paragraph = nil
		r.out.WriteString("<hr>")
		return
	}
	if strings.HasPrefix(line, "- ") {
		r.flushParagraph()
		r.paragraph = nil
		text := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		r.writeListItem("ul", depth, text)
		return
	}
	if n, ok := orderedPrefix(line); ok {
		r.flushParagraph()
		r.paragraph = nil
		text := strings.TrimSpace(line[n:])
		r.writeListItem("ol", depth, text)
		return
	}
	if strings.HasPrefix(line, "[PHOTO PENDING") || strings.HasPrefix(line, "[SCREENSHOT PENDING") {
		r.closeAllLists()
		r.flushParagraph()
		r.paragraph = nil
		r.out.WriteString("<p class=muted>")
		r.out.WriteString(html.EscapeString(line))
		r.out.WriteString("</p>")
		return
	}
	r.closeAllLists()
	r.paragraph = append(r.paragraph, line)
}

func lineDepth(raw string) (int, string) {
	spaces := 0
	for spaces < len(raw) && raw[spaces] == ' ' {
		spaces++
	}
	line := strings.TrimSpace(raw)
	if line == "" {
		return 0, ""
	}
	// Nested sub-bullets under ordered steps use three spaces in the source copy.
	depth := spaces / 3
	return depth, line
}

func orderedPrefix(line string) (int, bool) {
	i := 0
	for i < len(line) && unicode.IsDigit(rune(line[i])) {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != '.' {
		return 0, false
	}
	if i+1 >= len(line) || line[i+1] != ' ' {
		return 0, false
	}
	return i + 2, true
}

func (r *mdRenderer) closeLi() {
	if r.liOpen {
		r.out.WriteString("</li>")
		r.liOpen = false
	}
}

func (r *mdRenderer) closeListsAbove(depth int) {
	for len(r.frames) > 0 && r.frames[len(r.frames)-1].depth > depth {
		r.closeLi()
		frame := r.frames[len(r.frames)-1]
		r.out.WriteString("</" + frame.kind + ">")
		r.frames = r.frames[:len(r.frames)-1]
	}
}

func (r *mdRenderer) closeAllLists() {
	for len(r.frames) > 0 {
		r.closeLi()
		frame := r.frames[len(r.frames)-1]
		r.out.WriteString("</" + frame.kind + ">")
		r.frames = r.frames[:len(r.frames)-1]
	}
}

func (r *mdRenderer) ensureList(kind string, depth int) {
	r.closeListsAbove(depth)
	if len(r.frames) == 0 {
		r.out.WriteString("<" + kind + ">")
		r.frames = append(r.frames, listFrame{kind: kind, depth: depth})
		return
	}
	top := r.frames[len(r.frames)-1]
	if top.depth < depth {
		r.out.WriteString("<" + kind + ">")
		r.frames = append(r.frames, listFrame{kind: kind, depth: depth})
		return
	}
	if top.depth == depth && top.kind != kind {
		r.closeLi()
		r.out.WriteString("</" + top.kind + ">")
		r.frames[len(r.frames)-1].kind = kind
		r.out.WriteString("<" + kind + ">")
	}
	if top.depth == depth && top.kind == kind {
		r.closeLi()
	}
}

func (r *mdRenderer) writeListItem(kind string, depth int, text string) {
	r.ensureList(kind, depth)
	r.out.WriteString("<li>")
	r.out.WriteString(inlineMarkup(text))
	r.liOpen = true
}

func (r *mdRenderer) flushParagraph() {
	if len(r.paragraph) == 0 {
		return
	}
	r.out.WriteString("<p>")
	r.out.WriteString(inlineMarkup(strings.Join(r.paragraph, " ")))
	r.out.WriteString("</p>")
}

func inlineMarkup(s string) string {
	var b strings.Builder
	remainder := s
	for len(remainder) > 0 {
		idxBacktick := strings.Index(remainder, "`")
		idxBold := strings.Index(remainder, "**")
		idxLink := strings.Index(remainder, "[")
		next := len(remainder)
		kind := -1 // 0 backtick, 1 bold, 2 link
		if idxBacktick >= 0 && idxBacktick < next {
			next = idxBacktick
			kind = 0
		}
		if idxBold >= 0 && idxBold < next {
			next = idxBold
			kind = 1
		}
		if idxLink >= 0 && idxLink < next {
			kind = 2
			next = idxLink
		}
		if kind < 0 {
			b.WriteString(html.EscapeString(remainder))
			break
		}
		b.WriteString(html.EscapeString(remainder[:next]))
		remainder = remainder[next:]
		switch kind {
		case 0:
			end := strings.Index(remainder[1:], "`")
			if end < 0 {
				b.WriteString(html.EscapeString(remainder))
				remainder = ""
				break
			}
			end++
			code := remainder[1:end]
			b.WriteString("<code>")
			b.WriteString(html.EscapeString(code))
			b.WriteString("</code>")
			remainder = remainder[end+1:]
		case 1:
			end := strings.Index(remainder[2:], "**")
			if end < 0 {
				b.WriteString(html.EscapeString(remainder))
				remainder = ""
				break
			}
			inner := remainder[2 : 2+end]
			b.WriteString("<strong>")
			b.WriteString(html.EscapeString(inner))
			b.WriteString("</strong>")
			remainder = remainder[2+end+2:]
		case 2:
			close := strings.Index(remainder, "]")
			if close < 0 {
				b.WriteString(html.EscapeString(remainder))
				remainder = ""
				break
			}
			label := remainder[1:close]
			rest := remainder[close+1:]
			if !strings.HasPrefix(rest, "(") {
				b.WriteString(html.EscapeString(remainder[:close+1]))
				remainder = rest
				break
			}
			parenEnd := strings.Index(rest, ")")
			if parenEnd < 0 {
				b.WriteString(html.EscapeString(remainder))
				remainder = ""
				break
			}
			href := strings.TrimSpace(rest[1:parenEnd])
			b.WriteString("<a href=\"")
			b.WriteString(html.EscapeString(href))
			b.WriteString("\">")
			b.WriteString(html.EscapeString(label))
			b.WriteString("</a>")
			remainder = rest[parenEnd+1:]
		}
	}
	return b.String()
}
