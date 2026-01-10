package shared

import (
	"reflect"
	"testing"
)

func TestListParseText(t *testing.T) {
	t.Run("TestEmptyList", func(t *testing.T) {
		text := ""
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(parsed.Items))
		}
		if parsed.ListMetaData == nil {
			t.Fatal("ListMetaData should not be nil")
		}
	})

	t.Run("TestSimpleListItems", func(t *testing.T) {
		text := "First item\nSecond item"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}
		if parsed.Items[0].Value != "First item" {
			t.Errorf("expected 'First item', got '%s'", parsed.Items[0].Value)
		}
		if !parsed.Items[0].IsText {
			t.Error("expected IsText to be true")
		}
		if parsed.Items[1].Value != "Second item" {
			t.Errorf("expected 'Second item', got '%s'", parsed.Items[1].Value)
		}
		if !parsed.Items[1].IsText {
			t.Error("expected IsText to be true")
		}
	})

	t.Run("TestListWithEmptyLines", func(t *testing.T) {
		text := "First item\n\nSecond item"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}
		if parsed.Items[0].Value != "First item" {
			t.Errorf("expected 'First item', got '%s'", parsed.Items[0].Value)
		}
		if parsed.Items[1].Value != "Second item" {
			t.Errorf("expected 'Second item', got '%s'", parsed.Items[1].Value)
		}
	})

	t.Run("TestHyperlinks", func(t *testing.T) {
		text := "=> https://pico.sh\n=> https://prose.sh blog platform"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsURL {
			t.Error("expected IsURL to be true")
		}
		if string(parsed.Items[0].URL) != "https://pico.sh" {
			t.Errorf("expected URL 'https://pico.sh', got '%s'", parsed.Items[0].URL)
		}
		if parsed.Items[0].Value != "https://pico.sh" {
			t.Errorf("expected Value 'https://pico.sh', got '%s'", parsed.Items[0].Value)
		}

		if !parsed.Items[1].IsURL {
			t.Error("expected IsURL to be true")
		}
		if string(parsed.Items[1].URL) != "https://prose.sh" {
			t.Errorf("expected URL 'https://prose.sh', got '%s'", parsed.Items[1].URL)
		}
		if parsed.Items[1].Value != "blog platform" {
			t.Errorf("expected Value 'blog platform', got '%s'", parsed.Items[1].Value)
		}
	})

	t.Run("TestImages", func(t *testing.T) {
		text := "=< https://i.imgur.com/iXMNUN5.jpg\n=< https://i.imgur.com/iXMNUN5.jpg I use arch, btw"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsImg {
			t.Error("expected IsImg to be true")
		}
		if string(parsed.Items[0].URL) != "https://i.imgur.com/iXMNUN5.jpg" {
			t.Errorf("expected URL 'https://i.imgur.com/iXMNUN5.jpg', got '%s'", parsed.Items[0].URL)
		}
		if parsed.Items[0].Value != "https://i.imgur.com/iXMNUN5.jpg" {
			t.Errorf("expected Value 'https://i.imgur.com/iXMNUN5.jpg', got '%s'", parsed.Items[0].Value)
		}

		if !parsed.Items[1].IsImg {
			t.Error("expected IsImg to be true")
		}
		if string(parsed.Items[1].URL) != "https://i.imgur.com/iXMNUN5.jpg" {
			t.Errorf("expected URL 'https://i.imgur.com/iXMNUN5.jpg', got '%s'", parsed.Items[1].URL)
		}
		if parsed.Items[1].Value != "I use arch, btw" {
			t.Errorf("expected Value 'I use arch, btw', got '%s'", parsed.Items[1].Value)
		}
	})

	t.Run("TestHeaders", func(t *testing.T) {
		text := "# Header One\n## Header Two"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsHeaderOne {
			t.Error("expected IsHeaderOne to be true")
		}
		if parsed.Items[0].Value != " Header One" {
			t.Errorf("expected ' Header One', got '%s'", parsed.Items[0].Value)
		}

		if !parsed.Items[1].IsHeaderTwo {
			t.Error("expected IsHeaderTwo to be true")
		}
		if parsed.Items[1].Value != " Header Two" {
			t.Errorf("expected ' Header Two', got '%s'", parsed.Items[1].Value)
		}
	})

	t.Run("TestBlockquotes", func(t *testing.T) {
		text := "> This is a blockquote."
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsBlock {
			t.Error("expected IsBlock to be true")
		}
		if parsed.Items[0].Value != " This is a blockquote." {
			t.Errorf("expected ' This is a blockquote.', got '%s'", parsed.Items[0].Value)
		}
	})

	t.Run("TestPreformattedText", func(t *testing.T) {
		text := "```\nsimple preformatted\nline 2\n```"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsPre {
			t.Error("expected IsPre to be true")
		}
		expected := "\nsimple preformatted\nline 2"
		if expected != parsed.Items[0].Value {
			t.Errorf("expected '%s', got '%s'", expected, parsed.Items[0].Value)
		}
	})

	t.Run("TestUnclosedPreformattedText", func(t *testing.T) {
		text := "```\nsimple unclosed"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(parsed.Items))
		}
		if !parsed.Items[0].IsPre {
			t.Error("expected IsPre to be true")
		}
		expected := "\nsimple unclosed"
		if expected != parsed.Items[0].Value {
			t.Errorf("expected '%s', got '%s'", expected, parsed.Items[0].Value)
		}
	})

	t.Run("TestVariableParsing", func(t *testing.T) {
		text := "=: title Hello World\n=: tags one, two, three\nAn actual list item"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 3 {
			t.Fatalf("expected 3 items, got %d", len(parsed.Items))
		}
		if parsed.Items[0].Value != "=: title Hello World" {
			t.Errorf("expected '=: title Hello World', got '%s'", parsed.Items[0].Value)
		}
		if parsed.Items[1].Value != "=: tags one, two, three" {
			t.Errorf("expected '=: tags one, two, three', got '%s'", parsed.Items[1].Value)
		}
		if parsed.Items[2].Value != "An actual list item" {
			t.Errorf("expected 'An actual list item', got '%s'", parsed.Items[2].Value)
		}

		if parsed.Title != "Hello World" {
			t.Errorf("expected Title 'Hello World', got '%s'", parsed.Title)
		}
		expectedTags := []string{"one", "two", "three"}
		if !reflect.DeepEqual(expectedTags, parsed.Tags) {
			t.Errorf("expected Tags '%v', got '%v'", expectedTags, parsed.Tags)
		}
	})

	t.Run("TestNestedListWithSpaces", func(t *testing.T) {
		text := "first item\n  second item\n    third item\nlast item"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 4 {
			t.Fatalf("expected 4 items, got %d", len(parsed.Items))
		}

		if parsed.Items[0].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[0].Indent)
		}
		if parsed.Items[1].Indent != 1 {
			t.Errorf("expected Indent 1, got %d", parsed.Items[1].Indent)
		}
		if parsed.Items[2].Indent != 2 {
			t.Errorf("expected Indent 2, got %d", parsed.Items[2].Indent)
		}
		if parsed.Items[3].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[3].Indent)
		}
	})

	t.Run("TestNestedListWithTabs", func(t *testing.T) {
		text := "first item\n\tsecond item\n\t\tthird item\nlast item"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 4 {
			t.Fatalf("expected 4 items, got %d", len(parsed.Items))
		}

		if parsed.Items[0].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[0].Indent)
		}
		if parsed.Items[1].Indent != 1 {
			t.Errorf("expected Indent 1, got %d", parsed.Items[1].Indent)
		}
		if parsed.Items[2].Indent != 2 {
			t.Errorf("expected Indent 2, got %d", parsed.Items[2].Indent)
		}
		if parsed.Items[3].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[3].Indent)
		}
	})

	t.Run("TestDeeplyNestedList", func(t *testing.T) {
		text := "1\n  2\n    3\n      4\n        5"
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if len(parsed.Items) != 5 {
			t.Fatalf("expected 5 items, got %d", len(parsed.Items))
		}

		if parsed.Items[0].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[0].Indent)
		}
		if parsed.Items[1].Indent != 1 {
			t.Errorf("expected Indent 1, got %d", parsed.Items[1].Indent)
		}
		if parsed.Items[2].Indent != 2 {
			t.Errorf("expected Indent 2, got %d", parsed.Items[2].Indent)
		}
		if parsed.Items[3].Indent != 3 {
			t.Errorf("expected Indent 3, got %d", parsed.Items[3].Indent)
		}
		if parsed.Items[4].Indent != 4 {
			t.Errorf("expected Indent 4, got %d", parsed.Items[4].Indent)
		}
	})

	t.Run("TestMixedSpaceAndTabIndentation", func(t *testing.T) {
		// spec advises against this, but we test the behavior
		text := "first\n\tsecond\n  third"
		parsed := ListParseText(text)

		if len(parsed.Items) != 3 {
			t.Fatalf("expected 3 items, got %d", len(parsed.Items))
		}
		if parsed.Items[0].Indent != 0 {
			t.Errorf("expected Indent 0, got %d", parsed.Items[0].Indent)
		}
		if parsed.Items[1].Indent != 1 {
			t.Errorf("expected Indent 1, got %d", parsed.Items[1].Indent)
		}
		if parsed.Items[2].Indent != 2 {
			t.Errorf("expected Indent 2, got %d", parsed.Items[2].Indent)
		}
	})

	t.Run("TestComprehensiveList", func(t *testing.T) {
		text := `=: title My Awesome List
# Big Header
Here is a normal item.
=> https://pico.sh
  * nested item 1
> A wise man once said...
`
		parsed := ListParseText(text)
		if parsed == nil {
			t.Fatal("parsed should not be nil")
		}
		if parsed.Title != "My Awesome List" {
			t.Errorf("expected Title 'My Awesome List', got '%s'", parsed.Title)
		}

		if len(parsed.Items) != 6 {
			t.Fatalf("expected 6 items, got %d", len(parsed.Items))
		}

		if parsed.Items[0].Value != "=: title My Awesome List" {
			t.Errorf("expected '=: title My Awesome List', got '%s'", parsed.Items[0].Value)
		}
		if !parsed.Items[1].IsHeaderOne {
			t.Error("expected IsHeaderOne to be true")
		}
		if parsed.Items[1].Value != " Big Header" {
			t.Errorf("expected ' Big Header', got '%s'", parsed.Items[1].Value)
		}
		if !parsed.Items[2].IsText {
			t.Error("expected IsText to be true")
		}
		if parsed.Items[2].Value != "Here is a normal item." {
			t.Errorf("expected 'Here is a normal item.', got '%s'", parsed.Items[2].Value)
		}
		if !parsed.Items[3].IsURL {
			t.Error("expected IsURL to be true")
		}
		if parsed.Items[4].Value != "* nested item 1" {
			t.Errorf("expected '* nested item 1', got '%s'", parsed.Items[4].Value)
		}
		if !parsed.Items[5].IsBlock {
			t.Error("expected IsBlock to be true")
		}
	})

	t.Run("TestMalformedLines", func(t *testing.T) {
		text := "= > malformed url\n= < malformed image"
		parsed := ListParseText(text)
		if len(parsed.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(parsed.Items))
		}

		if !parsed.Items[0].IsText {
			t.Error("expected IsText to be true")
		}
		if parsed.Items[0].Value != "= > malformed url" {
			t.Errorf("expected '= > malformed url', got '%s'", parsed.Items[0].Value)
		}

		if !parsed.Items[1].IsText {
			t.Error("expected IsText to be true")
		}
		if parsed.Items[1].Value != "= < malformed image" {
			t.Errorf("expected '= < malformed image', got '%s'", parsed.Items[1].Value)
		}
	})
}
