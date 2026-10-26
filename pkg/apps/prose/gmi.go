package prose

import (
	"fmt"
	"html"
	"io"
	"strings"
	"time"

	"github.com/araddon/dateparse"
)

type GemText struct {
	Html        string
	PublishAt   *time.Time
	Tags        []string
	Hidden      bool
	Title       string
	Description string
}

func parseGemText(text string) (*GemText, error) {
	rdr := strings.NewReader(text)
	var out strings.Builder
	hw := HTMLWriter{
		out: &out,
	}
	meta, err := parseGemLines(rdr, hw.Handle)
	if err != nil {
		return nil, err
	}
	hw.Finish()
	gem := &GemText{
		Html: out.String(),
	}

	for k, v := range meta {
		switch k {
		case "date":
			nextDate, err := dateparse.ParseStrict(v)
			if err != nil {
				return gem, err
			}
			gem.PublishAt = &nextDate
		case "tags":
			gem.Tags = strings.Split(v, " ")
		case "draft":
			if v == "true" {
				gem.Hidden = true
			} else {
				gem.Hidden = false
			}
		case "title":
			gem.Title = v
		case "description":
			gem.Description = v
		}
	}

	return gem, nil
}

type HTMLWriter struct {
	out  io.Writer
	pre  bool
	list bool
}

func (h *HTMLWriter) Handle(line Line) {
	if _, ok := line.(LineListItem); ok {
		if !h.list {
			h.list = true
			_, _ = fmt.Fprint(h.out, "<ul>\n")
		}
	} else if h.list {
		h.list = false
		_, _ = fmt.Fprint(h.out, "</ul>\n")
	}
	switch line := line.(type) {
	case LineLink:
		url := html.EscapeString(line.URL)
		name := html.EscapeString(line.Name)
		if name == "" {
			name = url
		}
		_, _ = fmt.Fprintf(h.out, "<p><a href='%s'>%s</a></p>\n", url, name)
	case LinePreformattingToggle:
		h.pre = !h.pre
		if h.pre {
			_, _ = fmt.Fprint(h.out, "<pre>\n")
		} else {
			_, _ = fmt.Fprint(h.out, "</pre>\n")
		}
	case LinePreformattedText:
		_, _ = fmt.Fprintf(h.out, "%s\n", html.EscapeString(string(line)))
	case LineHeading1:
		_, _ = fmt.Fprintf(h.out, "<h1>%s</h1>\n", html.EscapeString(string(line)))
	case LineHeading2:
		_, _ = fmt.Fprintf(h.out, "<h2>%s</h2>\n", html.EscapeString(string(line)))
	case LineHeading3:
		_, _ = fmt.Fprintf(h.out, "<h3>%s</h3>\n", html.EscapeString(string(line)))
	case LineListItem:
		_, _ = fmt.Fprintf(h.out, "<li>%s</li>\n", html.EscapeString(string(line)))
	case LineQuote:
		_, _ = fmt.Fprintf(h.out, "<blockquote>%s</blockquote>\n", html.EscapeString(string(line)))
	case LineText:
		if line == "" {
			// do nothing
		} else {
			_, _ = fmt.Fprintf(h.out, "<p>%s</p>\n", html.EscapeString(string(line)))
		}
	}
}

func (h *HTMLWriter) Finish() {
	if h.pre {
		_, _ = fmt.Fprint(h.out, "</pre>\n")
	}
	if h.list {
		_, _ = fmt.Fprint(h.out, "</ul>\n")
	}
}
