package docs

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// TOCItem represents a Table of Contents entry for Markdown headings (#, ##, ###)
type TOCItem struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

// RenderMarkdownToHTML takes raw markdown bytes and converts it to safe, styled HTML with GFM extensions and auto heading IDs
func RenderMarkdownToHTML(markdown []byte) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(),
		),
	)

	var buf bytes.Buffer
	if err := md.Convert(markdown, &buf); err != nil {
		return "", fmt.Errorf("failed to parse markdown: %v", err)
	}

	return buf.String(), nil
}

// ExtractTOC parses raw markdown and extracts #, ##, ### headings into structured TOC items with anchor IDs
func ExtractTOC(markdown []byte) []TOCItem {
	md := goldmark.New(
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)

	reader := text.NewReader(markdown)
	doc := md.Parser().Parse(reader)

	var items []TOCItem
	seenIDs := make(map[string]int)

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && n.Kind() == ast.KindHeading {
			h := n.(*ast.Heading)
			if h.Level >= 1 && h.Level <= 3 {
				title := strings.TrimSpace(extractNodeText(h, markdown))
				if title != "" {
					var id string
					if rawID, ok := h.AttributeString("id"); ok {
						switch v := rawID.(type) {
						case []byte:
							id = string(v)
						case string:
							id = v
						}
					}
					if id == "" {
						id = Slugify(title)
					}

					// Ensure unique IDs if duplicate heading titles exist
					if count, exists := seenIDs[id]; exists {
						seenIDs[id] = count
						id = fmt.Sprintf("%s-%d", id, count)
						seenIDs[id] = count + 1
					} else {
						seenIDs[id] = 1
					}

					items = append(items, TOCItem{
						Level: h.Level,
						ID:    id,
						Title: title,
					})
				}
			}
		}
		return ast.WalkContinue, nil
	})

	return items
}

// extractNodeText recursively extracts plain text from Goldmark AST nodes
func extractNodeText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() == ast.KindText {
			t := child.(*ast.Text)
			buf.Write(t.Segment.Value(source))
		} else if child.Kind() == ast.KindCodeSpan {
			for c := child.FirstChild(); c != nil; c = c.NextSibling() {
				if t, ok := c.(*ast.Text); ok {
					buf.Write(t.Segment.Value(source))
				}
			}
		} else {
			buf.WriteString(extractNodeText(child, source))
		}
	}
	return buf.String()
}

var nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z0-9\s\-_]`)
var multiSpaceRegex = regexp.MustCompile(`[\s\_]+`)

// Slugify generates URL-safe heading IDs
func Slugify(text string) string {
	clean := strings.ToLower(text)
	clean = nonAlphanumericRegex.ReplaceAllString(clean, "")
	clean = multiSpaceRegex.ReplaceAllString(clean, "-")
	return strings.Trim(clean, "-")
}

// FetchMarkdownFromGit simulates or delegates fetching markdown from Git repository
func FetchMarkdownFromGit(repoPath string) ([]byte, error) {
	return FetchTechDoc(context.Background(), repoPath, "README.md")
}
