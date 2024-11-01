package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

type Paths struct {
	Blog string `json:"blog"`
}

type GoogleAnalytics struct {
	TrackingID string `json:"tracking_id"`
}
type Link struct {
	URL         string `json:"URL"`
	DisplayText string `json:"display_text"`
}
type Tag struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Layout string `json:"layout"`
}

type Config struct {
	Domain       string          `json:"domain"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	URL          string          `json:"URL"`
	BaseURL      string          `json:"base_URL"`
	SpecialLinks []Link          `json:"special_links"`
	Paths        Paths           `json:"paths"`
	Analytics    GoogleAnalytics `json:"google_analytics"`
	Tags         []Tag           `json:"tags"`
	Posts        []Post          `json:"posts"`
}

type Post struct {
	Markdown    []byte   `json:"markdown"`
	HTML        []byte   `json:"html"`
	Layout      string   `json:"layout"`
	Title       string   `json:"title"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	URL         string   `json:"URL"` /* URL will be the {filenameRoot}/html */
	Subtitle    string   `json:"subtitle"`
	Tags        []string `json:"tags"`
	RootName    string   `json:"root_name"` /* If post is abc.md, root name is abc */
}

type IncludesContent struct {
	Site Config
	Post Post
}

type LayoutContent struct {
	Includes map[string]template.HTML
	Content  template.HTML
	Site     Config
	Post     Post
	Tag      Tag
}

func main() {
	format()
}

const (
	/* Files and directories */
	CONFIG_FILE  = "config.json"
	INDEX_FILE   = "index.md"
	BLOG_FILE    = "blog.md"
	MARKDOWN_DIR = "markdown"
	INCLUDES_DIR = "includes"
	LAYOUTS_DIR  = "layouts"
	SITE_DIR     = "doc"
	ASSETS_DIR   = "assets"

	/* Includes keywords */
	INCLUDES_HEAD       = "Head"
	INCLUDES_HEADER     = "Header"
	INCLUDES_FOOTER     = "Footer"
	INCLUDES_FOOTERPOST = "FooterPost"
)

/* Fully rendered html for header, footer, etc */
var includesRender map[string]template.HTML = map[string]template.HTML{}
var specialFiles []string = []string{INDEX_FILE, BLOG_FILE}

//go:embed includes/*
var includesEFS embed.FS

//go:embed layouts/*
var layoutsEFS embed.FS

//go:embed assets/*
var assetsEFS embed.FS

func format() {

	if err := reset(); err != nil {
		log.Fatalf("error resetting site directory: %v", err)
	}

	/* Parse config */
	f, err := os.Open(CONFIG_FILE)
	if err != nil {
		log.Fatalf("error opening config file: %v", err)
	}

	cfgRaw, err := io.ReadAll(f)
	if err != nil {
		log.Fatalf("error reading config file: %v", err)
	}

	var cfg Config
	err = json.Unmarshal(cfgRaw, &cfg)
	if err != nil {
		log.Fatalf("error unmarshaling config file: %v", err)
	}

	/* Parse posts */
	var posts []Post
	postsDir := filepath.Join(MARKDOWN_DIR, "posts")
	postsFS := os.DirFS(postsDir)
	postsFilenames, err := fs.Glob(postsFS, "*.md")
	for _, name := range postsFilenames {
		path := filepath.Join(postsDir, name)
		post, err := parse(path)
		if err != nil {
			log.Fatalf("error rendering posts: %v", err)
		}

		posts = append(posts, post)
	}
	cfg.Posts = posts

	/* Parse tags */
	var tags []Tag
	tagsDir := filepath.Join(MARKDOWN_DIR, "tags")
	tagsFS := os.DirFS(tagsDir)
	tagsFilenames, err := fs.Glob(tagsFS, "*.json")
	if err != nil {
		log.Fatalf("error finding tags metadata files: %v", err)
	}
	for _, name := range tagsFilenames {
		path := filepath.Join(tagsDir, name)
		metadata, err := read(path)
		if err != nil {
			log.Fatalf("error reading tags metadata: %v", err)
		}

		var tag Tag
		if err = json.Unmarshal(metadata, &tag); err != nil {
			log.Fatalf("error unmarshaling tags metadata: %v", err)
		}
		tags = append(tags, tag)
	}
	cfg.Tags = tags

	/* We have to execute includes template for each page */
	includesFilenames, err := fs.Glob(includesEFS, "includes/*.html")
	if err != nil {
		log.Fatalf("error finding includes filenames: %s", err)
	}
	includes := template.Must(template.ParseFS(includesEFS, includesFilenames...))
	for _, name := range includesFilenames {
		root := strings.Split(name, "/")[1]
		includesRender[root] = ""
	}

	/* First render special pages */
	for _, name := range specialFiles {

		/* Parse post */
		path := filepath.Join(MARKDOWN_DIR, name)
		post, err := parse(path)
		if err != nil {
			log.Fatalf("error parsing special file %s: %v", post.RootName, err)
		}

		/* Render post */
		destDir := SITE_DIR
		err = render(post, destDir, cfg, includes, Tag{})
		if err != nil {
			log.Fatalf("error rendering special pages: %v", err)
		}
	}

	/* Render blog posts */
	postsFilenames, err = fs.Glob(postsFS, "*.md")
	for _, name := range postsFilenames {

		/* Parse post */
		path := filepath.Join(postsDir, name)
		post, err := parse(path)
		if err != nil {
			log.Fatalf("error parsing blog post %s: %v", post.RootName, err)
		}

		/* Render post */
		destDir := filepath.Join(SITE_DIR, "blog")
		err = render(post, destDir, cfg, includes, Tag{})
		if err != nil {
			log.Fatalf("error rendering posts: %v", err)
		}
	}

	/* Render tags pages */
	for _, t := range cfg.Tags {
		/* Each tag page is stored in tagged/<tag>/<tag_page>.html - first create this directory tree + file */
		if err = os.MkdirAll(filepath.Join(SITE_DIR, "tagged", t.Slug), 0750); err != nil {
			log.Fatalf("error creating site/tagged/%s folder: %v", t.Slug, err)
		}

		/* Parse tag as a post */
		post := Post{Layout: t.Layout, RootName: t.Slug}

		/* Render post */
		destDir := filepath.Join(SITE_DIR, "tagged", t.Slug)
		err = render(post, destDir, cfg, includes, t)
		if err != nil {
			log.Fatalf("error rendering tags: %v", err)
		}
	}
}

func render(post Post, destDir string, cfg Config, includes *template.Template, tag Tag) error {

	/* Generate includes using page and site info*/
	includesContent := IncludesContent{
		Site: cfg,
		Post: post,
	}
	for k := range includesRender {
		b := bytes.Buffer{}
		includes.ExecuteTemplate(&b, k, includesContent)
		switch k {
		case "header.html":
			includesRender[INCLUDES_HEADER] = template.HTML((b.String()))
		case "footer.html":
			includesRender[INCLUDES_FOOTER] = template.HTML((b.String()))
		case "head.html":
			includesRender[INCLUDES_HEAD] = template.HTML((b.String()))
		case "footer-post.html":
			includesRender[INCLUDES_FOOTERPOST] = template.HTML((b.String()))
		}
	}

	/* Generate layout using page and includes info */
	layoutContent := LayoutContent{
		Content:  template.HTML(post.HTML),
		Site:     cfg,
		Post:     post,
		Includes: includesRender,
		Tag:      tag,
	}
	layoutFilename := post.Layout
	layoutTempl, err := template.ParseFS(layoutsEFS, fmt.Sprintf("layouts/%s.html", layoutFilename))

	if err != nil {
		return fmt.Errorf("error parsing layout template file %s: %v", layoutFilename, err)
	}
	render := bytes.Buffer{}
	layoutTempl.Execute(&render, layoutContent)

	/* Create final HTML file */
	f, err := os.Create(filepath.Join(destDir, fmt.Sprintf("%s.html", post.RootName)))
	if err != nil {
		return fmt.Errorf("error creating HTML file for %s: %v", post.RootName, err)
	}

	_, err = io.Copy(f, &render)
	if err != nil {
		return fmt.Errorf("error rendering HTML for %s: %v", post.RootName, err)
	}

	return nil
}

func parse(path string) (post Post, err error) {
	metadata, err := read(fmt.Sprintf("%s.json", path))
	if err != nil {
		return post, fmt.Errorf("error reading metadata: %v", err)
	}
	err = json.Unmarshal(metadata, &post)
	if err != nil {
		return post, fmt.Errorf("error unmarshalling metadata: %v", err)
	}

	markdown, err := read(path)
	if err != nil {
		return post, fmt.Errorf("error reading post markdown: %v", err)
	}
	post.Markdown = markdown
	post.HTML = mdToHTML(markdown)
	post.RootName = rootName(path)

	return post, nil
}

/*
If path is bbc/cbc/abc.md, root name is abc.
We assume the path always ends with file extension i.e '.md', '.json', etc.
*/
func rootName(path string) string {
	_, filename := filepath.Split(path)
	return strings.Split(filename, ".")[0]
}

func read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening metadata file: %v", err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("error reading metadata file: %v", err)
	}

	return b, nil
}

/* https://github.com/gomarkdown/markdown/blob/master/examples/basic.go */
func mdToHTML(md []byte) []byte {
	/* Create markdown parser with extensions */
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock | parser.FencedCode
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(md)

	/* Create HTML renderer with extensions */
	renderer := newCustomizedRender()

	return markdown.Render(doc, renderer)
}

func renderCodeBlock(w io.Writer, c *ast.CodeBlock, entering bool) {
	if entering {
		io.WriteString(w, "<div class='highlight'><pre class='highlight'><code>")
		io.WriteString(w, string(c.Literal))     // Write the code content
		io.WriteString(w, "</code></pre></div>") // Immediately close tags
	}
}

func myRenderHook(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	if codeBlock, ok := node.(*ast.CodeBlock); ok {
		renderCodeBlock(w, codeBlock, entering)
		return ast.GoToNext, true
	}
	return ast.GoToNext, false
}

func newCustomizedRender() *html.Renderer {
	opts := html.RendererOptions{
		Flags:          html.CommonFlags | html.HrefTargetBlank,
		RenderNodeHook: myRenderHook,
	}
	return html.NewRenderer(opts)
}

func (p Post) ContainsTag(tag string) bool {
	return slices.Contains(p.Tags, tag)
}

/* Delete old site directory and create new one + copy over assets folder */
func reset() error {
	if err := os.RemoveAll(SITE_DIR); err != nil {
		return fmt.Errorf("error deleting old site/ folder to create new one: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(SITE_DIR, "blog"), 0750); err != nil {
		return fmt.Errorf("error creating site/blog folder: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(SITE_DIR, "tagged"), 0750); err != nil {
		return fmt.Errorf("error creating site/tagged folder: %v", err)
	}
	if err := os.CopyFS(SITE_DIR, assetsEFS); err != nil {
		return fmt.Errorf("error copying site/assets folder: %v", err)
	}
	return nil
}
