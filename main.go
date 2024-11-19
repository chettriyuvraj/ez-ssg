package main

import (
	"bufio"
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
	"time"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/jroimartin/gocui"
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
	Layout string `json:"layout,omitempty"`
}

type Config struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	URL          string          `json:"URL"`
	SpecialLinks []Link          `json:"special_links"`
	Paths        Paths           `json:"paths"`
	Analytics    GoogleAnalytics `json:"google_analytics"`
	Tags         []Tag           `json:"tags,omitempty"`
	Posts        []Post          `json:"posts,omitempty"`
}

type Post struct {
	Markdown    []byte   `json:"markdown,omitempty"`
	HTML        []byte   `json:"html,omitempty"`
	Layout      string   `json:"layout,omitempty"`
	Title       string   `json:"title,omitempty"`
	Date        string   `json:"date,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags"`
	RootName    string   `json:"root_name,omitempty"` /* If post is abc.md, root name is abc */
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

const (
	/* Files and directories */
	CONFIG_FILE  = "config.json"
	INDEX_FILE   = "index.md"
	BLOG_FILE    = "blog.md"
	MARKDOWN_DIR = "markdown"
	INCLUDES_DIR = "includes"
	LAYOUTS_DIR  = "layouts"
	SITE_DIR     = "docs"
	ASSETS_DIR   = "assets"

	/* Includes keywords */
	INCLUDES_HEAD       = "Head"
	INCLUDES_HEADER     = "Header"
	INCLUDES_FOOTER     = "Footer"
	INCLUDES_FOOTERPOST = "FooterPost"

	/* Frontmatter boundary */
	FRONTMATTER_BOUNDARY = "------------------"
)

// type Command struct {
// 	instruction string
// 	exec        func() error
// }

// var commands map[string]Command = map[string]Command{

//		"init":     Command{exec: initDirs, instruction: "Initializes content directories for creating blog posts and adding tags. Use the absolute first time you are running this app."},
//		"generate": Command{exec: generate, instruction: "Generates the static site. Use it when you have all the content ready to generate HTML."},
//		"post":     Command{exec: initPost, instruction: "Creates a new post"},
//		"tag":      Command{exec: createTag, instruction: "Creates one/multiple new tag under which posts can be classified."},
//	}
var commands map[string]string = map[string]string{
	"init":     "Initializes content directories for creating blog posts and adding tags. Use the absolute first time you are running this app.",
	"generate": "Generates the static site. Use it when you have all the content ready to generate HTML.",
	"post":     "Creates a new post",
	"tag":      "Creates one/multiple new tags under which posts can be classified.",
}

/* Fully rendered html for header, footer, etc */
var includesRender map[string]template.HTML = map[string]template.HTML{}
var specialFiles []string = []string{INDEX_FILE, BLOG_FILE}

//go:embed includes/*
var includesEFS embed.FS

//go:embed layouts/*
var layoutsEFS embed.FS

//go:embed assets/*
var assetsEFS embed.FS

/* Samples */
var sampleCfg Config = Config{
	Title:       "chettriyuvraj",
	Description: "Yuvraj Chettri's personal blog",
	URL:         "https://chettriyuvraj.github.io",
	SpecialLinks: []Link{
		{
			URL:         "https://www.linkedin.com/in/yuvraj-chettri/",
			DisplayText: "Linkedin",
		},
		{
			URL:         "https://www.github.com/chettriyuvraj",
			DisplayText: "Github",
		},
	},
	Paths: Paths{
		Blog: "/blog",
	},
	Analytics: GoogleAnalytics{
		TrackingID: "1234567",
	},
}

/* GUI Related variables */
var (
	viewArr = []string{"side", "input1", "input2"}
	active  = 0
)

func main() {
	logger := log.New(os.Stderr, "", 0)

	if len(os.Args) == 1 {
		interactive(logger)
	}

	var err error

	cmd := os.Args[1]
	if _, exists := commands[cmd]; !exists {
		logger.Fatalf(help())
	}

	switch cmd {
	case "init":
		err = initDirs()
	case "generate":
		err = generate()
	case "post":
		if len(os.Args) < 3 {
			logger.Fatalf(help())
		}

		title := os.Args[2]
		tags := []string{}
		if len(os.Args) < 5 || os.Args[3] != "-t" {
			initPost(title, tags)
			return
		}

		tags = os.Args[4:]
		err = initPost(title, tags)
	case "tag":
		if len(os.Args) < 3 {
			logger.Fatalf(help())
		}

		tags := os.Args[2:]
		err = createTag(tags) // TODO: add args
	}

	if err != nil {
		log.Fatal(err)
	}
}

func initDirs() error {
	if err := os.MkdirAll(filepath.Join(MARKDOWN_DIR, "posts"), 0750); err != nil {
		return fmt.Errorf("error creating markdown/posts folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(MARKDOWN_DIR, "tags"), 0750); err != nil {
		return fmt.Errorf("error creating markdown/tags folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(MARKDOWN_DIR, "assets", "images"), 0750); err != nil {
		return fmt.Errorf("error creating markdown/assets/images folder: %w", err)
	}

	/* Create sample data for default files */
	/* Sample config */
	cfg, err := json.MarshalIndent(sampleCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling sample config to json: %w", err)
	}

	/* Index file metadata */
	index := Post{
		Title:       "(enter title for homepage - this is what is displayed when you hover over your browser page tab)",
		Description: "(enter description for home page - this is metadata and not website displayable content)",
	}
	indexMeta, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling index file metadata to json: %w", err)
	}

	/* Blog file metadata */
	blog := Post{
		Title:       "(enter title for blog page - this is what is displayed when you hover over your browser page tab)",
		Description: "(enter description for blog page - this is metadata and not website displayable content)",
	}
	blogMeta, err := json.MarshalIndent(blog, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling blog file metadata to json: %w", err)
	}

	/* Create default files */
	indexFilepath := filepath.Join(MARKDOWN_DIR, INDEX_FILE)
	if err := addFrontmatter(indexFilepath, indexMeta); err != nil {
		return fmt.Errorf("error creating file %s: %w", indexFilepath, err)
	}

	blogFilepath := filepath.Join(MARKDOWN_DIR, BLOG_FILE)
	if err := addFrontmatter(blogFilepath, blogMeta); err != nil {
		return fmt.Errorf("error creating file %s: %w", blogFilepath, err)
	}

	configFilepath := CONFIG_FILE
	if err := os.WriteFile(configFilepath, []byte{}, 0755); err != nil {
		return fmt.Errorf("error creating file %s: %w", configFilepath, err)
	}
	if err := os.WriteFile(configFilepath, cfg, 0755); err != nil {
		return fmt.Errorf("error creating file %s: %w", configFilepath, err)
	}

	return nil
}

func initPost(title string, tags []string) error {
	if title == "" {
		return fmt.Errorf("no title provided")
	}

	filename := strings.ReplaceAll(title, " ", "_")
	filepath := filepath.Join(MARKDOWN_DIR, "posts", fmt.Sprintf("%s.md", filename))

	meta := Post{
		Title: title,
		Tags:  tags,
		Date:  formatDate(time.Now()),
	}
	metaRaw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling post metadata to json: %w", err)
	}

	if err := addFrontmatter(filepath, metaRaw); err != nil {
		return fmt.Errorf("error creating post file %s: %w", filepath, err)
	}

	return nil
}

func createTag(tags []string) error {
	for _, t := range tags {
		slug := strings.ToLower(t)
		filename := fmt.Sprintf("%s.json", slug)
		filepath := filepath.Join(MARKDOWN_DIR, "tags", filename)

		raw, err := json.MarshalIndent(Tag{Slug: slug}, "", "  ")
		if err != nil {
			return fmt.Errorf("error marshaling tag data to json: %w", err)
		}

		if err := os.WriteFile(filepath, raw, 0755); err != nil {
			return fmt.Errorf("error creating tag file %s: %w", filepath, err)
		}
	}

	return nil
}

func generate() error {
	if err := reset(); err != nil {
		return fmt.Errorf("error resetting site directory: %w", err)
	}

	/* Copy over assets folder */
	assetsFS := os.DirFS(filepath.Join(MARKDOWN_DIR, ASSETS_DIR))
	if err := os.CopyFS(SITE_DIR, assetsFS); err != nil {
		return fmt.Errorf("error copying markdown/assets folder: %w", err)
	}

	/* Parse config */
	f, err := os.Open(CONFIG_FILE)
	if err != nil {
		return fmt.Errorf("error opening config file: %w", err)
	}
	defer f.Close()

	cfgRaw, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	var cfg Config
	err = json.Unmarshal(cfgRaw, &cfg)
	if err != nil {
		return fmt.Errorf("error unmarshaling config file: %w", err)
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
			return fmt.Errorf("error rendering posts: %w", err)
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
		return fmt.Errorf("error finding tags metadata files: %w", err)
	}
	for _, name := range tagsFilenames {
		path := filepath.Join(tagsDir, name)
		metadata, err := read(path)
		if err != nil {
			return fmt.Errorf("error reading tags metadata: %w", err)
		}

		var tag Tag
		if err = json.Unmarshal(metadata, &tag); err != nil {
			return fmt.Errorf("error unmarshaling tags metadata: %w", err)
		}
		tags = append(tags, tag)
	}
	cfg.Tags = tags

	/* We have to execute includes template for each page */
	includesFilenames, err := fs.Glob(includesEFS, "includes/*.html")
	if err != nil {
		return fmt.Errorf("error finding includes filenames: %s", err)
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
			return fmt.Errorf("error parsing special file %s: %w", post.RootName, err)
		}
		switch name {
		case INDEX_FILE:
			post.Layout = "default"
		case BLOG_FILE:
			post.Layout = "blog"
		}

		/* Render post */
		destDir := SITE_DIR
		err = render(post, destDir, cfg, includes, Tag{})
		if err != nil {
			return fmt.Errorf("error rendering special pages: %w", err)
		}
	}

	/* Render blog posts */
	postsFilenames, err = fs.Glob(postsFS, "*.md")
	for _, name := range postsFilenames {

		/* Parse post */
		path := filepath.Join(postsDir, name)
		post, err := parse(path)
		if err != nil {
			return fmt.Errorf("error parsing blog post %s: %w", post.RootName, err)
		}
		post.Layout = "post"

		/* Render post */
		destDir := filepath.Join(SITE_DIR, "blog")
		err = render(post, destDir, cfg, includes, Tag{})
		if err != nil {
			return fmt.Errorf("error rendering posts: %w", err)
		}
	}

	/* Render tags pages */
	for _, t := range cfg.Tags {
		/* Each tag page is stored in tagged/<tag>/<tag_page>.html - first create this directory tree + file */
		if err = os.MkdirAll(filepath.Join(SITE_DIR, "tagged", t.Slug), 0750); err != nil {
			return fmt.Errorf("error creating docs/tagged/%s folder: %w", t.Slug, err)
		}

		/* Parse tag as a post */
		post := Post{Layout: "tagged", RootName: t.Slug}

		/* Render post */
		destDir := filepath.Join(SITE_DIR, "tagged", t.Slug)
		err = render(post, destDir, cfg, includes, t)
		if err != nil {
			return fmt.Errorf("error rendering tags: %w", err)
		}
	}

	return nil
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
		return fmt.Errorf("error parsing layout template file %s: %w", layoutFilename, err)
	}
	render := bytes.Buffer{}
	layoutTempl.Execute(&render, layoutContent)

	/* Create final HTML file */
	f, err := os.Create(filepath.Join(destDir, fmt.Sprintf("%s.html", post.RootName)))
	if err != nil {
		return fmt.Errorf("error creating HTML file for %s: %w", post.RootName, err)
	}

	_, err = io.Copy(f, &render)
	if err != nil {
		return fmt.Errorf("error rendering HTML for %s: %w", post.RootName, err)
	}

	return nil
}

func parse(path string) (post Post, err error) {
	metadata, markdown, err := readFull(path)
	if err != nil {
		return post, fmt.Errorf("error reading post: %s, %w", path, err)
	}

	err = json.Unmarshal(metadata, &post)
	if err != nil {
		return post, fmt.Errorf("error unmarshaling metadata: %w", err)
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
		return nil, fmt.Errorf("error opening metadata file: %w", err)
	}
	defer f.Close()

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("error reading metadata file: %w", err)
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
		return fmt.Errorf("error deleting old docs/ folder to create new one: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(SITE_DIR, "blog"), 0750); err != nil {
		return fmt.Errorf("error creating docs/blog folder: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(SITE_DIR, "tagged"), 0750); err != nil {
		return fmt.Errorf("error creating docs/tagged folder: %w", err)
	}
	if err := os.CopyFS(SITE_DIR, assetsEFS); err != nil {
		return fmt.Errorf("error copying docs/assets folder: %w", err)
	}
	return nil
}

func help() string {
	return `
ez-ssg		Create a static website like chettriyuvraj.github.io in 5 minutes.

Usage: ez-ssg <command> [argument]

Options:
	-h	Specify this flag anywhere in the command and we'll show you this help screen.

Commands:

  init		Initializes content directories for creating blog posts and adding tags. Use the absolute first time you are running this app.
  generate	Generates the static site.
  post		Creates a new post
  tag		Creates one/multiple new tag under which posts can be classified.

Commands Usage:

  init

  Usage: ez-ssg init


  generate

  Usage: ez-ssg generate


  post

  Usage: ez-ssg post <title> [options]

  Options:
    -t	Specify space-separated tags for the post. You must create the tag beforehand using the tag command.


  tag

  Usage: ez-ssg tag <tag 1> <tag2> ..

`
}

func formatDate(t time.Time) string {
	/* Get the day of the month */
	day := t.Day()

	/* Determine the appropriate suffix (st, nd, rd, or th) */
	var suffix string
	switch day {
	case 1, 21, 31:
		suffix = "st"
	case 2, 22:
		suffix = "nd"
	case 3, 23:
		suffix = "rd"
	default:
		suffix = "th"
	}

	/* Format the date as "Feb 21st, 2024" */
	return t.Format("Jan") + fmt.Sprintf(" %d%s, %d", day, suffix, t.Year())
}

/* We are expecting data to be in json */
/* File will be truncated to write frontmatter */
func addFrontmatter(filepath string, data []byte) error {
	var buf bytes.Buffer

	if _, err := buf.WriteString(FRONTMATTER_BOUNDARY + "\n"); err != nil {
		return fmt.Errorf("error writing opening boundary to buffer: %w", err)
	}
	if _, err := buf.Write(data); err != nil {
		return fmt.Errorf("error writing frontmatter to buffer: %w", err)
	}
	if _, err := buf.WriteString("\n" + FRONTMATTER_BOUNDARY + "\n"); err != nil {
		return fmt.Errorf("error writing opening boundary to buffer: %w", err)
	}

	if err := os.WriteFile(filepath, buf.Bytes(), 0755); err != nil {
		return fmt.Errorf("error writing frontmatter to file: %w", err)
	}

	return nil
}

func readFull(filepath string) (frontmatter []byte, content []byte, err error) {
	var bufFrontMatter, bufContent bytes.Buffer

	f, err := os.Open(filepath)
	if err != nil {
		return nil, nil, fmt.Errorf("error opening file: %w", err)
	}
	defer f.Close()

	boundaryCount := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		b := scanner.Bytes()
		/* If we haven't encountered frontmatter boundary twice (open/close) we are still parsing frontmatter */
		if string(b) == FRONTMATTER_BOUNDARY && boundaryCount < 2 {
			boundaryCount += 1
			continue
		}

		/* If frontmatter */
		if boundaryCount < 2 {
			if _, err := bufFrontMatter.Write(b); err != nil {
				return nil, nil, fmt.Errorf("error reading frontmatter: %w", err)
			}
			continue
		}

		/* Content */
		if _, err := bufContent.Write(b); err != nil {
			return nil, nil, fmt.Errorf("error reading content: %w", err)
		}
		if _, err := bufContent.Write([]byte("\n")); err != nil {
			return nil, nil, fmt.Errorf("error reading content: %w", err)
		}
		continue
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("error post reading file: %w", err)
	}

	return bufFrontMatter.Bytes(), bufContent.Bytes(), nil
}

func cursorDown(g *gocui.Gui, v *gocui.View) error {
	// Clear any messages from previous command execution
	msgView, err := g.View("msg")
	if err != nil {
		return fmt.Errorf("error checking views: %w", err)
	}
	msgView.Clear()

	// Check if next line is a command
	cx, cy := v.Cursor()
	nextCmd, err := v.Line(cy + 1)
	if err != nil {
		return fmt.Errorf("error checking for existence of next line: %w", err)
	}

	// If nothing in the next line don't move down
	if nextCmd == "" {
		return nil
	}

	// Set cursor to next pos
	if v != nil {
		if err := v.SetCursor(cx, cy+1); err != nil {
			ox, oy := v.Origin()
			if err := v.SetOrigin(ox, oy+1); err != nil {
				return err
			}
		}
	}

	// Change view to main screen
	mainView, err := g.SetCurrentView("main")
	if err != nil {
		return err
	}

	// Since we already have the command, simply display it on main screen
	if err := displayCmdInstruction(mainView, nextCmd); err != nil {
		return err
	}

	// Set view back to side screen
	if _, err := g.SetCurrentView("side"); err != nil {
		return err
	}
	return nil
}

func cursorUp(g *gocui.Gui, v *gocui.View) error {
	// Clear any messages from previous command execution
	msgView, err := g.View("msg")
	if err != nil {
		return fmt.Errorf("error checking views: %w", err)
	}
	msgView.Clear()

	if v != nil {
		ox, oy := v.Origin()
		cx, cy := v.Cursor()
		if err := v.SetCursor(cx, cy-1); err != nil && oy > 0 {
			if err := v.SetOrigin(ox, oy-1); err != nil {
				return err
			}
		}
	}
	if err := SetCurrentCmdInstruction(g, v); err != nil {
		return err
	}
	return nil
}

func SetCurrentCmdInstruction(g *gocui.Gui, v *gocui.View) error {
	var cmd string
	var err error

	// Grab current highlighted line
	_, cy := v.Cursor()
	if cmd, err = v.Line(cy); err != nil {
		cmd = ""
	}

	// Get main view
	mainView, err := g.View("main")
	if err != nil {
		return err
	}

	// Display command instruction
	err = displayCmdInstruction(mainView, cmd)
	if err != nil {
		return err
	}

	// Get input views
	inp1View, err := g.View("input1")
	if err != nil {
		// View not yet defined
		if errors.Is(err, gocui.ErrUnknownView) {
			return nil
		}
		return err
	}

	inp2View, err := g.View("input2")
	if err != nil {
		// View not yet defined
		if errors.Is(err, gocui.ErrUnknownView) {
			return nil
		}
		return err
	}

	// Show inputs according to the command
	switch cmd {
	case "init", "generate":
		inp1View.Frame = false
		inp2View.Frame = false
		inp1View.Clear()
		inp2View.Clear()

	case "post":
		inp1View.Frame = true
		inp2View.Frame = true
		// inp1View.Editable = true
		// inp2View.Editable = true
		// inp1View.Clear()
		// inp2View.Clear()
		// if _, err := inp1View.Write([]byte("<enter post title>")); err != nil {
		// 	return fmt.Errorf("unable to show title input view: %w", err)
		// }
		// if _, err := inp2View.Write([]byte("<enter any tags for post - space separated>")); err != nil {
		// 	return fmt.Errorf("unable to show tag input view: %w", err)
		// }
	case "tag":
		inp1View.Frame = true
		inp2View.Frame = false
		// inp1View.Editable = true
		// inp2View.Editable = true
		// inp1View.Clear()
		// inp2View.Clear()
		// if _, err := inp1View.Write([]byte("<enter the tags you want to create - space separated>")); err != nil {
		// 	return fmt.Errorf("unable to show tag input view: %w", err)
		// }
		// if _, err := inp2View.Write([]byte("-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-x-")); err != nil {
		// 	return fmt.Errorf("unable to show tag input view: %w", err)
		// }

	default:
		panic("this command does not exist")
	}

	return nil
}

func execCurCmd(g *gocui.Gui, v *gocui.View) error {
	var cmd string
	var err error

	// Grab current highlighted line
	_, cy := v.Cursor()
	if cmd, err = v.Line(cy); err != nil {
		cmd = ""
	}

	// Exec command instruction
	msg := exec(g, cmd)

	// Set view to msg screen
	msgView, err := g.SetCurrentView("msg")
	if err != nil {
		return err
	}

	// Display exec result
	msgView.Clear()
	if _, err := msgView.Write([]byte(msg)); err != nil {
		return fmt.Errorf("error writing command result message: %w", err)
	}

	// Set view back to side screen
	if _, err := g.SetCurrentView("side"); err != nil {
		return err
	}

	return nil
}

func exec(g *gocui.Gui, cmd string) (msg string) {
	var err error
	var v1, v2 *gocui.View

	switch cmd {
	case "init":
		err = initDirs()
	case "generate":
		err = generate()
	case "post":
		v1, err = g.View("input1")
		if err != nil {
			return err.Error()
		}
		tags := []string{}
		tagsBuffer := strings.TrimSpace(v1.Buffer())
		if tagsBuffer != "" {
			tags = strings.Split(tagsBuffer, " ")
		}

		v2, err = g.View("input2")
		if err != nil {
			return err.Error()
		}
		title := strings.TrimSpace(v2.Buffer())

		err = initPost(title, tags)

	case "tag":
		v1, err = g.View("input1")
		if err != nil {
			return err.Error()
		}

		tags := []string{}
		tagsBuffer := strings.TrimSpace(v1.Buffer())

		if tagsBuffer == "" {
			return errors.New("no tag values provided").Error()
		}

		tags = strings.Split(tagsBuffer, " ")
		err = createTag(tags)

	default:
		err = fmt.Errorf("command does not exist: %s", cmd)
	}

	if err != nil {
		return fmt.Sprintf("error executing %s command: %s", cmd, err.Error())
	}

	return "successfully executed"
}

func displayCmdInstruction(v *gocui.View, cmd string) error {
	v.Clear()

	// Print command instruction
	cmdInstruction := commands[cmd]
	if cmdInstruction == "" {
		return fmt.Errorf("invalid command string")
	}
	if _, err := v.Write([]byte(cmdInstruction)); err != nil {
		return fmt.Errorf("error writing command instruction for %s cmd: %w", cmd, err)
	}

	return nil
}

func quit(g *gocui.Gui, v *gocui.View) error {
	return gocui.ErrQuit
}

func setCurrentViewOnTop(g *gocui.Gui, name string, highlight, background bool) (*gocui.View, error) {
	var v *gocui.View
	var err error

	if v, err = g.SetCurrentView(name); err != nil {
		return nil, err
	}
	if highlight {
		v.Highlight = true
	}
	if background {
		v.BgColor = gocui.ColorGreen
	}
	return g.SetViewOnTop(name)
}

func removeBgColor(v *gocui.View) {
	v.BgColor = gocui.ColorDefault
}

func removeHighlight(v *gocui.View) {
	v.Highlight = false
}

func nextView(g *gocui.Gui, v *gocui.View) error {
	// Check current command
	sideView, err := g.View("side")
	if err != nil {
		return fmt.Errorf("error checking side view: %w", err)
	}
	_, cy := sideView.Cursor()
	cmd, err := sideView.Line(cy)
	if err != nil {
		return fmt.Errorf("error checking current command: %w", err)
	}

	// No view switching for these commands
	if cmd == "generate" || cmd == "init" {
		return nil
	}

	// Set current view on top and set background color
	nextIndex := (active + 1) % len(viewArr)
	curViewName := viewArr[nextIndex]

	// If command is tags, skip input2 (title)
	// Avoid changing colors highlights and move ahead
	if cmd == "tag" && curViewName == "input2" {
		active = nextIndex
		return nextView(g, v)
	}

	var toColorBackground, toHighlight bool
	if curViewName == "side" {
		toHighlight = true
	} else {
		toColorBackground = true
	}
	if _, err := setCurrentViewOnTop(g, curViewName, toHighlight, toColorBackground); err != nil {
		return err
	}
	active = nextIndex

	// Remove background and highlight from previous view
	removeBgColor(v)
	removeHighlight(v)

	return nil
}

func keybindings(g *gocui.Gui) error {

	if err := g.SetKeybinding("side", gocui.KeyArrowDown, gocui.ModNone, cursorDown); err != nil {
		return err
	}
	if err := g.SetKeybinding("side", gocui.KeyArrowUp, gocui.ModNone, cursorUp); err != nil {
		return err
	}
	if err := g.SetKeybinding("side", gocui.KeyEnter, gocui.ModNone, execCurCmd); err != nil {
		return err
	}
	if err := g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		return err
	}
	if err := g.SetKeybinding("", gocui.KeyTab, gocui.ModNone, nextView); err != nil {
		return err
	}

	return nil
}

func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	var v *gocui.View
	var err error

	if v, err = g.SetView("main", (maxX/2)-27, 0, (maxX/2)+30, (maxY / 8)); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Editable = false
		v.Wrap = true
		v.Frame = false
	}

	if v, err = g.SetView("side", -1, -1, 30, maxY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.SelFgColor = gocui.ColorBlack
		for cmd := range commands {
			v.Write([]byte(cmd + "\n"))
		}

		if _, err := g.SetCurrentView("side"); err != nil {
			return err
		}
	}

	if err := SetCurrentCmdInstruction(g, v); err != nil {
		return err
	}

	if v, err = g.SetView("msg", (maxX / 3), 10, (maxX/2)+15, (maxY/5)+5); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.SelFgColor = gocui.ColorBlack
		v.Editable = true
		v.Frame = false
	}

	if v, err := g.SetView("input1", maxX/3+5, 15, maxX/2+10, maxY/3+5); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Autoscroll = true
		v.Title = "Tags"
		v.Editable = true
	}
	if v, err := g.SetView("input2", maxX/3+5, 20, maxX/2+10, maxY/3+10); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = true
		v.Autoscroll = true
		v.Editable = true
		v.Title = "Title"
	}

	return nil
}

func interactive(logger *log.Logger) {
	g, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		logger.Panicln(err)
	}
	defer g.Close()

	g.SetManagerFunc(layout)

	if err := keybindings(g); err != nil {
		logger.Panicln(err)
	}

	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		logger.Panicln(err)
	}
}
