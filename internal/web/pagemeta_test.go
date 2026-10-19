package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

const viteIndex = `<!doctype html>
<html lang="en">

<head>
  <meta charset="UTF-8" />
  <title></title>
  <script type="module" src="/assets/index-abc.js"></script>
</head>

<body>
  <div id="app"></div>
</body>

</html>
`

func TestInjectPageMeta_WritesTitleMetaAndLang(t *testing.T) {
	out := string(injectPageMeta([]byte(viteIndex), PageMeta{
		Lang:        "zh-CN",
		Title:       "独角数卡",
		SiteName:    "Dujiao",
		Description: "自动发货",
		Keywords:    "卡密,发卡",
		Image:       "/uploads/logo.png",
		BaseURL:     "https://shop.example.com",
	}, "https://shop.example.com/products/a"))

	for _, want := range []string{
		`<html lang="zh-CN">`,
		`<title>独角数卡</title>`,
		`<meta name="description" content="自动发货" />`,
		`<meta name="keywords" content="卡密,发卡" />`,
		`<meta property="og:type" content="website" />`,
		`<meta property="og:site_name" content="Dujiao" />`,
		`<meta property="og:title" content="独角数卡" />`,
		`<meta property="og:description" content="自动发货" />`,
		`<meta property="og:url" content="https://shop.example.com/products/a" />`,
		`<meta property="og:image" content="https://shop.example.com/uploads/logo.png" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `lang="en"`) || strings.Contains(out, "<title></title>") {
		t.Fatalf("original lang/title not replaced:\n%s", out)
	}
	if strings.Count(out, "<title>") != 1 {
		t.Fatalf("want exactly one title:\n%s", out)
	}
	if idx := strings.Index(out, "twitter:card"); idx > strings.Index(out, "</head>") {
		t.Fatalf("meta must be inside head:\n%s", out)
	}
}

func TestInjectPageMeta_EscapesValues(t *testing.T) {
	out := string(injectPageMeta([]byte(viteIndex), PageMeta{
		Lang:        `zh"><script>`,
		Title:       `</title><script>alert(1)</script>`,
		Description: `a" onload="x`,
		Image:       `https://cdn.example.com/a.png?x=1&y="2"`,
	}, `https://h/"><script>`))

	if strings.Contains(out, "<script>alert(1)</script>") || strings.Contains(out, `zh"><script>`) || strings.Contains(out, `"><script>`) {
		t.Fatalf("unescaped value leaked:\n%s", out)
	}
	for _, want := range []string{
		`<title>&lt;/title&gt;&lt;script&gt;alert(1)&lt;/script&gt;</title>`,
		`content="a&#34; onload=&#34;x"`,
		`content="https://cdn.example.com/a.png?x=1&amp;y=&#34;2&#34;"`,
		`lang="zh&#34;&gt;&lt;script&gt;"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInjectPageMeta_MissingTitleTagIsInserted(t *testing.T) {
	raw := "<html><head><meta charset=\"UTF-8\"></head><body></body></html>"
	out := string(injectPageMeta([]byte(raw), PageMeta{Title: "Shop", Lang: "en-US"}, "http://h/"))
	if !strings.Contains(out, "<title>Shop</title>") {
		t.Fatalf("title not inserted:\n%s", out)
	}
	if !strings.HasPrefix(out, `<html lang="en-US">`) {
		t.Fatalf("lang not added:\n%s", out)
	}
	if strings.Index(out, "<title>") > strings.Index(out, "</head>") {
		t.Fatalf("title outside head:\n%s", out)
	}
}

func TestInjectPageMeta_EmptyFieldsKeepOriginalTitle(t *testing.T) {
	out := string(injectPageMeta([]byte(viteIndex), PageMeta{}, "http://h/"))
	if !strings.Contains(out, "<title></title>") || !strings.Contains(out, `<html lang="en">`) {
		t.Fatalf("original title/lang should be kept:\n%s", out)
	}
	if strings.Contains(out, `name="description"`) || strings.Contains(out, "og:title") || strings.Contains(out, "og:image") {
		t.Fatalf("empty values must not emit tags:\n%s", out)
	}
	if !strings.Contains(out, `<meta name="twitter:card" content="summary" />`) || !strings.Contains(out, `<meta property="og:type" content="website" />`) {
		t.Fatalf("want summary card and website type without image:\n%s", out)
	}
}

func TestInjectPageMeta_NoHeadReturnsOriginal(t *testing.T) {
	raw := []byte("<html>user-spa</html>")
	out := injectPageMeta(raw, PageMeta{Title: "x"}, "http://h/")
	if string(out) != string(raw) {
		t.Fatalf("got %s", out)
	}
}

func TestRegisterUser_InjectsPageMeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fsys := fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte(viteIndex)},
		"assets/u.js": &fstest.MapFile{Data: []byte("x")},
	}
	var calls int
	provider := func(req *http.Request) (PageMeta, error) {
		calls++
		return PageMeta{Title: "Shop", Lang: "zh-CN", Image: "/uploads/l.png"}, nil
	}
	if err := RegisterUser(r, fsys, provider); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/products/abc?ref=1", nil)
	req.Host = "shop.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != indexCacheControl {
		t.Fatalf("status=%d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	for _, want := range []string{
		"<title>Shop</title>",
		`<html lang="zh-CN">`,
		`<meta property="og:url" content="https://shop.example.com/products/abc" />`,
		`<meta property="og:image" content="https://shop.example.com/uploads/l.png" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}

	// 静态资源不应触发元信息读取。
	calls = 0
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/u.js", nil))
	if calls != 0 || w.Body.String() != "x" {
		t.Fatalf("asset request: calls=%d body=%q", calls, w.Body.String())
	}
}

func TestRegisterUser_ProviderErrorServesOriginal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fsys := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(viteIndex)}}
	provider := func(*http.Request) (PageMeta, error) {
		return PageMeta{Title: "should not appear"}, errors.New("db down")
	}
	if err := RegisterUser(r, fsys, provider); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || w.Body.String() != viteIndex {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
