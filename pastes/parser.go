package pastes

import (
	"bytes"

	"github.com/alecthomas/chroma/formatters/html"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
)

func ParseText(filename string, text string) (string, error) {
	formatter := html.New(
		html.WithLineNumbers(true),
		html.LinkableLineNumbers(true, ""),
		html.WithClasses(true),
	)
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Get("plaintext")
	}
	iterator, err := lexer.Tokenise(nil, text)
	if err != nil {
		return text, err
	}
	var buf bytes.Buffer
	err = formatter.Format(&buf, styles.Dracula, iterator)
	if err != nil {
		return text, err
	}
	return buf.String(), nil
}
