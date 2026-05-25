package parser

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/PuerkitoBio/goquery"
)

type CrawlerParser struct {
}

func NewCrawlerParser() *CrawlerParser {
	return &CrawlerParser{}
}

func (p *CrawlerParser) Parse(ctx context.Context, reader io.Reader, filename string) (string, map[string]interface{}, error) {
	rawHTML, err := io.ReadAll(reader)
	if err != nil {
		return "", nil, fmt.Errorf("读取HTML内容失败: %w", err)
	}

	metadata := make(map[string]interface{})
	metadata["file_name"] = filename
	metadata["url"] = filename

	pageURL, _ := url.Parse(filename)
	if pageURL == nil || !pageURL.IsAbs() {
		pageURL, _ = url.Parse("http://localhost")
	}

	article, err := readability.FromReader(strings.NewReader(string(rawHTML)), pageURL)
	if err == nil && article.Node != nil {
		return p.parseWithReadability(article, metadata)
	}

	return p.parseWithGoquery(string(rawHTML), metadata)
}

func (p *CrawlerParser) parseWithReadability(article readability.Article, metadata map[string]interface{}) (string, map[string]interface{}, error) {
	title := strings.TrimSpace(article.Title())
	if title != "" {
		metadata["title"] = title
	}
	if article.Byline() != "" {
		metadata["author"] = strings.TrimSpace(article.Byline())
	}
	if article.Excerpt() != "" {
		metadata["description"] = strings.TrimSpace(article.Excerpt())
	}
	if article.SiteName() != "" {
		metadata["site_name"] = strings.TrimSpace(article.SiteName())
	}

	var contentBuf strings.Builder
	if renderErr := article.RenderText(&contentBuf); renderErr != nil {
		return "", nil, fmt.Errorf("渲染readability内容失败: %w", renderErr)
	}
	result := contentBuf.String()

	if title != "" && !strings.HasPrefix(result, title) {
		result = "# " + title + "\n\n" + result
	}

	result = strings.ReplaceAll(result, "\u00a0", " ")
	result = compactWhitespace(result)

	metadata["size"] = len(result)
	metadata["lines"] = crawlerCountLines(result)
	metadata["words"] = len(strings.Fields(result))

	return result, metadata, nil
}

func (p *CrawlerParser) parseWithGoquery(html string, metadata map[string]interface{}) (string, map[string]interface{}, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", nil, fmt.Errorf("解析HTML失败: %w", err)
	}

	title := strings.TrimSpace(doc.Find("title").Text())
	if title != "" {
		metadata["title"] = title
	}
	if desc, exists := doc.Find("meta[name='description']").Attr("content"); exists {
		metadata["description"] = strings.TrimSpace(desc)
	}
	if keywords, exists := doc.Find("meta[name='keywords']").Attr("content"); exists {
		metadata["keywords"] = strings.TrimSpace(keywords)
	}
	if author, exists := doc.Find("meta[name='author']").Attr("content"); exists {
		metadata["author"] = strings.TrimSpace(author)
	}

	doc.Find("script, style, noscript, iframe, svg, nav, footer, header, aside, form").Remove()

	var bodyText strings.Builder

	if title != "" {
		bodyText.WriteString("# ")
		bodyText.WriteString(title)
		bodyText.WriteString("\n\n")
	}

	selection := doc.Find("body")
	if selection.Length() == 0 {
		selection = doc.Find("html")
	}

	selection.Find("h1, h2, h3, h4, h5, h6, p, li, td, th, blockquote, pre, dt, dd, figcaption").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			return
		}

		goName := goquery.NodeName(s)
		switch goName {
		case "h1":
			bodyText.WriteString("# ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n\n")
		case "h2":
			bodyText.WriteString("## ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n\n")
		case "h3":
			bodyText.WriteString("### ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n\n")
		case "h4", "h5", "h6":
			bodyText.WriteString("#### ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n\n")
		case "li":
			bodyText.WriteString("- ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n")
		case "p", "blockquote", "figcaption":
			bodyText.WriteString(text)
			bodyText.WriteString("\n\n")
		case "pre":
			bodyText.WriteString("```\n")
			bodyText.WriteString(text)
			bodyText.WriteString("\n```\n\n")
		case "td", "th":
			bodyText.WriteString(text)
			bodyText.WriteString("\t")
		case "dt":
			bodyText.WriteString(text)
			bodyText.WriteString("\n")
		case "dd":
			bodyText.WriteString("  ")
			bodyText.WriteString(text)
			bodyText.WriteString("\n")
		default:
			bodyText.WriteString(text)
			bodyText.WriteString("\n")
		}
	})

	result := bodyText.String()
	result = strings.ReplaceAll(result, "\u00a0", " ")
	result = compactWhitespace(result)

	metadata["size"] = len(result)
	metadata["lines"] = crawlerCountLines(result)
	metadata["words"] = len(strings.Fields(result))

	return result, metadata, nil
}

func compactWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	prevNewline := false
	prevSpace := false

	for _, r := range s {
		if r == '\n' {
			if prevNewline {
				continue
			}
			prevNewline = true
			prevSpace = false
			b.WriteRune(r)
		} else if r == ' ' || r == '\t' {
			if prevSpace {
				continue
			}
			prevSpace = true
			prevNewline = false
			b.WriteRune(' ')
		} else {
			prevNewline = false
			prevSpace = false
			b.WriteRune(r)
		}
	}

	return strings.TrimSpace(b.String())
}

func crawlerCountLines(text string) int {
	count := 0
	for _, c := range text {
		if c == '\n' {
			count++
		}
	}
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func (p *CrawlerParser) SupportedTypes() []string {
	return []string{"url", "webpage", "crawl"}
}
