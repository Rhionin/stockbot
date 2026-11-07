package help

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var headingSlug = regexp.MustCompile(`[^a-z0-9]+`)

// renderHelpMarkdown turns household-safe scanner help into an HTML fragment.
func renderHelpMarkdown(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	lines := strings.Split(source, "\n")
	var out strings.Builder
	var listKind string // "ul", "ol", or ""
	closeList := func() {
		if listKind != "" {
			out.WriteString("</" + listKind + ">")
			listKind = ""
		}
	}
	flushParagraph := func(p []string) {
		if len(p) == 0 {
			return
		}
		out.WriteString("<p>")
		out.WriteString(inlineMarkup(strings.Join(p, " ")))
		out.WriteString("</p>")
	}
	var paragraph []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			closeList()
			flushParagraph(paragraph)
			paragraph = nil
			continue
		}
		if strings.HasPrefix(line, "#") {
			closeList()
			flushParagraph(paragraph)
			paragraph = nil
			level := 0
			for level < len(line) && line[level] == '#' {
				level++
			}
			if level > len(line) || line[level] != ' ' {
				paragraph = append(paragraph, line)
				continue
			}
			text := strings.TrimSpace(line[level:])
			id := headingSlug.ReplaceAllString(strings.ToLower(text), "-")
			id = strings.Trim(id, "-")
			out.WriteString(fmt.Sprintf("<h%d id=%q>", level, html.EscapeString(id)))
			out.WriteString(inlineMarkup(text))
			out.WriteString(fmt.Sprintf("</h%d>", level))
			continue
		}
		if strings.HasPrefix(line, "---") {
			closeList()
			flushParagraph(paragraph)
			paragraph = nil
			out.WriteString("<hr>")
			continue
		}
		if strings.HasPrefix(line, "- ") {
			flushParagraph(paragraph)
			paragraph = nil
			if listKind != "ul" {
				closeList()
				out.WriteString("<ul>")
				listKind = "ul"
			}
			out.WriteString("<li>")
			out.WriteString(inlineMarkup(strings.TrimSpace(strings.TrimPrefix(line, "- "))))
			out.WriteString("</li>")
			continue
		}
		if len(line) > 2 && line[0] >= '0' && line[0] <= '9' {
			dot := strings.Index(line, ". ")
			if dot > 0 {
				flushParagraph(paragraph)
				paragraph = nil
				if listKind != "ol" {
					closeList()
					out.WriteString("<ol>")
					listKind = "ol"
				}
				out.WriteString("<li>")
				out.WriteString(inlineMarkup(strings.TrimSpace(line[dot+2:])))
				out.WriteString("</li>")
				continue
			}
		}
		if strings.HasPrefix(line, "[PHOTO PENDING") || strings.HasPrefix(line, "[SCREENSHOT PENDING") {
			closeList()
			flushParagraph(paragraph)
			paragraph = nil
			out.WriteString("<p class=muted>")
			out.WriteString(html.EscapeString(line))
			out.WriteString("</p>")
			continue
		}
		paragraph = append(paragraph, line)
	}
	closeList()
	flushParagraph(paragraph)
	return out.String()
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
			if strings.HasPrefix(href, "#") {
				b.WriteString(html.EscapeString(href))
			} else {
				b.WriteString(html.EscapeString(href))
			}
			b.WriteString("\">")
			b.WriteString(html.EscapeString(label))
			b.WriteString("</a>")
			remainder = rest[parenEnd+1:]
		}
	}
	return b.String()
}
