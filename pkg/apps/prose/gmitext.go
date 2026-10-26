package prose

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Line represents a line of a Gemini text response.
type Line interface {
	// String formats the line for use in a Gemini text response.
	String() string
	line() // private function to prevent other packages from implementing Line
}

// LineLink is a link line.
type LineLink struct {
	URL  string
	Name string
}

// LinePreformattingToggle is a preformatting toggle line.
type LinePreformattingToggle string

// LinePreformattedText is a preformatted text line.
type LinePreformattedText string

// LineHeading1 is a first-level heading line.
type LineHeading1 string

// LineHeading2 is a second-level heading line.
type LineHeading2 string

// LineHeading3 is a third-level heading line.
type LineHeading3 string

// LineListItem is an unordered list item line.
type LineListItem string

// LineQuote is a quote line.
type LineQuote string

// LineText is a text line.
type LineText string

func (l LineLink) String() string {
	if l.Name != "" {
		return fmt.Sprintf("=> %s %s", l.URL, l.Name)
	}
	return fmt.Sprintf("=> %s", l.URL)
}
func (l LinePreformattingToggle) String() string {
	return fmt.Sprintf("```%s", string(l))
}
func (l LinePreformattedText) String() string {
	return string(l)
}
func (l LineHeading1) String() string {
	return fmt.Sprintf("# %s", string(l))
}
func (l LineHeading2) String() string {
	return fmt.Sprintf("## %s", string(l))
}
func (l LineHeading3) String() string {
	return fmt.Sprintf("### %s", string(l))
}
func (l LineListItem) String() string {
	return fmt.Sprintf("* %s", string(l))
}
func (l LineQuote) String() string {
	return fmt.Sprintf("> %s", string(l))
}
func (l LineText) String() string {
	return string(l)
}

func (l LineLink) line()                {}
func (l LinePreformattingToggle) line() {}
func (l LinePreformattedText) line()    {}
func (l LineHeading1) line()            {}
func (l LineHeading2) line()            {}
func (l LineHeading3) line()            {}
func (l LineListItem) line()            {}
func (l LineQuote) line()               {}
func (l LineText) line()                {}

// Text represents a Gemini text response.
type Text []Line

// // ParseText parses Gemini text from the provided io.Reader.
// func ParseGemText(r io.Reader) (Text, error) {
// 	var t Text
// 	err := parseGemLines(r, func(line Line) {
// 		t = append(t, line)
// 	})
// 	return t, err
// }

// ParseLines parses Gemini text from the provided io.Reader.
// It calls handler with each line that it parses.
func parseGemLines(r io.Reader, handler func(Line)) (map[string]string, error) {
	const spacetab = " \t"
	var pre bool
	meta := map[string]string{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		var line Line
		text := scanner.Text()
		if strings.HasPrefix(text, "```") {
			pre = !pre
			text = text[3:]
			line = LinePreformattingToggle(text)
		} else if pre {
			line = LinePreformattedText(text)
		} else if strings.HasPrefix(text, "=>") {
			text = text[2:]
			text = strings.TrimLeft(text, spacetab)
			split := strings.IndexAny(text, spacetab)
			if split == -1 {
				// text is a URL
				line = LineLink{URL: text}
			} else {
				url := text[:split]
				name := text[split:]
				name = strings.TrimLeft(name, spacetab)
				line = LineLink{url, name}
			}
		} else if strings.HasPrefix(text, "* ") {
			text = text[2:]
			text = strings.TrimLeft(text, spacetab)
			line = LineListItem(text)
		} else if strings.HasPrefix(text, "###") {
			text = text[3:]
			text = strings.TrimLeft(text, spacetab)
			line = LineHeading3(text)
		} else if strings.HasPrefix(text, "##") {
			text = text[2:]
			text = strings.TrimLeft(text, spacetab)
			line = LineHeading2(text)
		} else if strings.HasPrefix(text, "#") {
			text = text[1:]
			text = strings.TrimLeft(text, spacetab)
			line = LineHeading1(text)
		} else if strings.HasPrefix(text, ">") {
			text = text[1:]
			text = strings.TrimLeft(text, spacetab)
			line = LineQuote(text)
		} else if strings.HasPrefix(text, "=: ") { // extension to gemtext spec to support metadata
			text = text[3:]
			kv := strings.SplitN(text, " ", 2)
			if len(kv) <= 1 {
				continue
			}
			meta[kv[0]] = kv[1]
		} else {
			line = LineText(text)
		}
		handler(line)
	}

	return meta, scanner.Err()
}

// String writes the Gemini text response to a string and returns it.
func (t Text) String() string {
	var b strings.Builder
	for _, l := range t {
		b.WriteString(l.String())
		b.WriteByte('\n')
	}
	return b.String()
}
