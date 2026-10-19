package web

import (
	"bytes"
	"html"
	"net/http"
	"regexp"
	"strings"
)

// PageMeta 是服务端写入 user SPA 入口 <head> 的站点级元信息。
//
// 前台的标题、描述等原本只由 JS（unhead）在运行时设置，微信 / QQ / Telegram 的
// 链接预览和不执行 JS 的爬虫只能看到空白页面；这里在返回 index.html 时直接写入。
// 所有字段都是未转义的原始文本，转义由 injectPageMeta 统一处理。
type PageMeta struct {
	// Lang 写入 <html lang>，为空时保留原值。
	Lang string
	// Title 写入 <title> 与 og:title。
	Title string
	// SiteName 写入 og:site_name；为空时回落到 Title。
	SiteName    string
	Description string
	Keywords    string
	// Image 分享图，可以是绝对 URL 或以 / 开头的站内路径（会基于 BaseURL 补全）。
	Image string
	// OGType 写入 og:type，为空时为 website。
	OGType string
	// BaseURL 站点根地址（不带结尾斜杠），用于拼接 og:url / og:image；
	// 为空时根据当前请求的协议与 Host 推导。
	BaseURL string
}

// PageMetaProvider 按请求返回站点级元信息。返回错误时入口页原样输出。
type PageMetaProvider func(*http.Request) (PageMeta, error)

var (
	htmlOpenTagPattern = regexp.MustCompile(`(?i)<html\b[^>]*>`)
	htmlLangAttr       = regexp.MustCompile(`(?i)\slang\s*=\s*("[^"]*"|'[^']*'|[^\s>]*)`)
	titleTagPattern    = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	headCloseTag       = []byte("</head>")
)

// renderUserIndex 生成写入元信息后的入口页；provider 缺失或出错时返回原始字节。
func renderUserIndex(req *http.Request, raw []byte, provider PageMetaProvider) []byte {
	if provider == nil || req == nil {
		return raw
	}
	meta, err := provider(req)
	if err != nil {
		return raw
	}
	meta.BaseURL = strings.TrimRight(strings.TrimSpace(meta.BaseURL), "/")
	if meta.BaseURL == "" {
		meta.BaseURL = requestBaseURL(req)
	}
	pageURL := meta.BaseURL + req.URL.EscapedPath()
	if req.URL.EscapedPath() == "" {
		pageURL = meta.BaseURL + "/"
	}
	return injectPageMeta(raw, meta, pageURL)
}

// injectPageMeta 把元信息写入 HTML。找不到 </head> 时不做任何修改。
func injectPageMeta(raw []byte, meta PageMeta, pageURL string) []byte {
	headEnd := bytes.Index(bytes.ToLower(raw), headCloseTag)
	if headEnd < 0 {
		return raw
	}

	title := strings.TrimSpace(meta.Title)
	siteName := strings.TrimSpace(meta.SiteName)
	if siteName == "" {
		siteName = title
	}
	description := strings.TrimSpace(meta.Description)
	keywords := strings.TrimSpace(meta.Keywords)
	image := absoluteURL(strings.TrimSpace(meta.Image), meta.BaseURL)

	var tags strings.Builder
	writeMeta := func(attr, key, value string) {
		if value == "" {
			return
		}
		tags.WriteString(`<meta ` + attr + `="` + key + `" content="` + html.EscapeString(value) + `" />` + "\n")
	}
	writeMeta("name", "description", description)
	writeMeta("name", "keywords", keywords)
	ogType := strings.TrimSpace(meta.OGType)
	if ogType == "" {
		ogType = "website"
	}
	twitterCard := "summary"
	if image != "" {
		twitterCard = "summary_large_image"
	}
	writeMeta("property", "og:type", ogType)
	writeMeta("property", "og:site_name", siteName)
	writeMeta("property", "og:title", title)
	writeMeta("property", "og:description", description)
	writeMeta("property", "og:url", pageURL)
	writeMeta("property", "og:image", image)
	writeMeta("name", "twitter:card", twitterCard)

	titleTag := ""
	if title != "" {
		titleTag = "<title>" + html.EscapeString(title) + "</title>"
	}

	head := raw[:headEnd]
	rest := raw[headEnd:]
	out := make([]byte, 0, len(raw)+tags.Len()+len(titleTag)+32)

	if loc := titleTagPattern.FindIndex(head); loc != nil {
		if titleTag == "" {
			out = append(out, head...)
		} else {
			out = append(out, head[:loc[0]]...)
			out = append(out, titleTag...)
			out = append(out, head[loc[1]:]...)
		}
	} else {
		out = append(out, head...)
		if titleTag != "" {
			out = append(out, titleTag...)
			out = append(out, '\n')
		}
	}
	out = append(out, tags.String()...)
	out = append(out, rest...)

	if lang := strings.TrimSpace(meta.Lang); lang != "" {
		out = setHTMLLang(out, lang)
	}
	return out
}

// setHTMLLang 替换（或补上）首个 <html> 标签的 lang 属性。
func setHTMLLang(doc []byte, lang string) []byte {
	loc := htmlOpenTagPattern.FindIndex(doc)
	if loc == nil {
		return doc
	}
	tag := doc[loc[0]:loc[1]]
	attr := ` lang="` + html.EscapeString(lang) + `"`
	var newTag []byte
	if attrLoc := htmlLangAttr.FindIndex(tag); attrLoc != nil {
		newTag = append(append(append([]byte{}, tag[:attrLoc[0]]...), attr...), tag[attrLoc[1]:]...)
	} else {
		// "<html" 之后插入
		newTag = append(append(append([]byte{}, tag[:5]...), attr...), tag[5:]...)
	}
	out := make([]byte, 0, len(doc)+len(attr))
	out = append(out, doc[:loc[0]]...)
	out = append(out, newTag...)
	out = append(out, doc[loc[1]:]...)
	return out
}

// absoluteURL 把站内路径补全为绝对地址；已是 http(s) 绝对地址的原样返回。
func absoluteURL(raw, baseURL string) string {
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return baseURL + raw
}

// requestBaseURL 根据请求推导站点根地址。
func requestBaseURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	} else if proto := strings.ToLower(strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-Proto"), ",")[0])); proto == "https" || proto == "http" {
		scheme = proto
	}
	return scheme + "://" + req.Host
}
