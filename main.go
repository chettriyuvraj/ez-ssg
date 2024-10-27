package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
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
	Slug string `json:"slug"`
	Name string `json:"name"`
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
	Posts        []PostMetadata  `json:"posts"`
}

type Post struct {
	Metadata PostMetadata `json:"metadata"`
	Markdown []byte       `json:"markdown"`
	HTML     []byte       `json:"html"`
}

type PostMetadata struct {
	Layout      string   `json:"layout"`
	Title       string   `json:"title"`
	Date        string   `json:"date"`
	Description string   `json:"description"`
	URL         string   `json:"URL"` /* URL will be the {filenameRoot}/html */
	Subtitle    string   `json:"subtitle"`
	Tags        []string `json:"tags"`
}

type IncludesContent struct {
	Site Config
	Page PostMetadata
}

type LayoutContent struct {
	Includes map[string]template.HTML
	Content  template.HTML
	Site     Config
	Page     PostMetadata
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
	SITE_DIR     = "site"
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

var ErrNoDataInFile = errors.New("no data in file")

//go:embed includes/*
var includesEFS embed.FS

//go:embed layouts/*
var layoutsEFS embed.FS

//go:embed assets/*.css
var assetsEFS embed.FS

func format() {

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

	postsDir := filepath.Join(MARKDOWN_DIR, "posts")
	postsFS := os.DirFS(postsDir)
	postsMetadataFilenames, err := fs.Glob(postsFS, "*.md.json")
	if err != nil {
		log.Fatalf("error finding posts metadata files: %v", err)
	}
	for _, n := range postsMetadataFilenames {
		f, err := os.Open(filepath.Join(postsDir, n))
		if err != nil {
			log.Fatalf("error reading posts metadata file: %v", err)
		}
		raw, err := io.ReadAll(f)
		if err != nil {
			log.Fatalf("error reading posts metadata file: %v", err)
		}

		var metadata PostMetadata
		if err = json.Unmarshal(raw, &metadata); err != nil {
			log.Fatalf("error unmarshaling posts metadata file: %v", err)
		}
		rootName := strings.Split(n, ".")[0]
		metadata.URL = fmt.Sprintf("%s.html", rootName)
		cfg.Posts = append(cfg.Posts, metadata)
	}

	tagsDir := filepath.Join(MARKDOWN_DIR, "tags")
	tagsFS := os.DirFS(tagsDir)
	tagsMetadataFilenames, err := fs.Glob(tagsFS, "*.md.json")
	if err != nil {
		log.Fatalf("error finding tags metadata files: %v", err)
	}
	for _, n := range tagsMetadataFilenames {
		f, err := os.Open(filepath.Join(tagsDir, n))
		if err != nil {
			log.Fatalf("error reading tags metadata file: %v", err)
		}
		raw, err := io.ReadAll(f)
		if err != nil {
			log.Fatalf("error reading tags metadata file: %v", err)
		}

		var tag Tag
		if err = json.Unmarshal(raw, &tag); err != nil {
			log.Fatalf("error unmarshaling tags metadata file: %v", err)
		}
		cfg.Tags = append(cfg.Tags, tag)
	}

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

	/* Delete old site directory and create new one + copy over assets folder */
	if err = os.RemoveAll(SITE_DIR); err != nil {
		log.Fatalf("error deleting old site/ folder to create new one: %v", err)
	}
	if err = os.MkdirAll(filepath.Join(SITE_DIR, "blog"), 0750); err != nil {
		log.Fatalf("error creating site/blog folder: %v", err)
	}
	if err = os.MkdirAll(filepath.Join(SITE_DIR, "tagged"), 0750); err != nil {
		log.Fatalf("error creating site/tagged folder: %v", err)
	}
	if err = os.CopyFS(SITE_DIR, assetsEFS); err != nil {
		log.Fatalf("error copying site/assets folder: %v", err)
	}
	assetsFS := os.DirFS(ASSETS_DIR)
	if err = os.CopyFS(SITE_DIR, assetsFS); err != nil {
		log.Fatalf("error copying your assets folder: %v", err)
	}

	/* First render special pages */
	for _, name := range specialFiles {
		destDir := SITE_DIR
		err = render(name, filepath.Join(MARKDOWN_DIR, name), destDir, cfg, includes, Tag{})
		if err != nil {
			log.Fatalf("error rendering special pages: %v", err)
		}
	}

	/* Render posts */
	postsFilenames, err := fs.Glob(postsFS, "*.md")
	for _, name := range postsFilenames {
		destDir := filepath.Join(SITE_DIR, "blog")
		err = render(name, filepath.Join(postsDir, name), destDir, cfg, includes, Tag{})
		if err != nil {
			log.Fatalf("error rendering posts: %v", err)
		}
	}

	/* Render tags */
	tagsFilenames, err := fs.Glob(tagsFS, "*.md")
	for _, name := range tagsFilenames {
		rootName := strings.Split(name, ".")[0]
		destDir := filepath.Join(SITE_DIR, "tagged", rootName)
		var tag Tag
		for _, t := range cfg.Tags {
			if t.Slug == rootName {
				tag = t
				break
			}
		}
		if tag.Slug == "" {
			log.Fatalf("tag not found: %s", rootName)
		}

		if err = os.MkdirAll(filepath.Join(SITE_DIR, "tagged", rootName), 0750); err != nil {
			log.Fatalf("error creating site/tagged/%s folder: %v", rootName, err)
		}

		err = render(name, filepath.Join(tagsDir, name), destDir, cfg, includes, tag)
		if err != nil {
			log.Fatalf("error rendering tags: %v", err)
		}
	}
}

func render(filename string, path string, destDir string, cfg Config, includes *template.Template, tag Tag) error {
	page, err := parsePost(path)
	rootName := strings.Split(filename, ".")[0]
	if err != nil {
		return fmt.Errorf("error parsing %s: %v", rootName, err)
	}

	/* Generate includes using page and site info*/
	includesContent := IncludesContent{
		Site: cfg,
		Page: page.Metadata,
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
		Content:  template.HTML(page.HTML),
		Site:     cfg,
		Page:     page.Metadata,
		Includes: includesRender,
		Tag:      tag,
	}
	layoutFilename := page.Metadata.Layout
	layoutTempl, err := template.ParseFS(layoutsEFS, fmt.Sprintf("layouts/%s.html", layoutFilename))

	if err != nil {
		return fmt.Errorf("error parsing layout template file %s: %v", layoutFilename, err)
	}
	render := bytes.Buffer{}
	layoutTempl.Execute(&render, layoutContent)

	/* Create final HTML file */
	f, err := os.Create(filepath.Join(destDir, fmt.Sprintf("%s.html", rootName)))
	if err != nil {
		return fmt.Errorf("error creating HTML file for %s: %v", rootName, err)
	}

	_, err = io.Copy(f, &render)
	if err != nil {
		return fmt.Errorf("error rendering HTML for %s: %v", rootName, err)
	}

	return nil
}

/* Returns post markdown + metadata, HTML remaining */
func parsePost(path string) (post Post, err error) {
	metadataF, err := os.Open(fmt.Sprintf("%s.json", path))
	if err != nil {
		return post, fmt.Errorf("error opening metadata file: %v", err)
	}
	metadataRaw, err := io.ReadAll(metadataF)
	if err != nil {
		return post, fmt.Errorf("error reading metadata file: %v", err)
	}
	err = json.Unmarshal(metadataRaw, &post.Metadata)
	if err != nil {
		return post, fmt.Errorf("error unmarshalling metadata: %v", err)
	}

	mdF, err := os.Open(path)
	if err != nil {
		return post, fmt.Errorf("error opening post markdown file: %v", err)
	}
	mdRaw, err := io.ReadAll(mdF)
	if err != nil {
		return post, fmt.Errorf("error reading post markdown file: %v", err)
	}
	post.Markdown = mdRaw
	post.HTML = mdToHTML(mdRaw)

	return post, nil
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

func (p PostMetadata) ContainsTag(tag string) bool {
	return slices.Contains(p.Tags, tag)
}
