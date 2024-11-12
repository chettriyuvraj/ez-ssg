package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddReadFrontmatter(t *testing.T) {
	/* Create test file */
	filename := "test.md"
	f, err := os.Create(filename)
	require.NoError(t, err)
	defer f.Close()
	defer os.Remove(filename)

	/* Add frontmatter */
	raw := []byte(`{
"title": "chettriyuvraj",
"description": "Yuvraj Chettri's personal blog",
"URL": "https://chettriyuvraj.github.io",
"special_links": [
	{
	"URL": "https://www.linkedin.com/in/yuvraj-chettri/",
	"display_text": "Linkedin"
	},
	{
	"URL": "https://www.github.com/chettriyuvraj",
	"display_text": "Github"
	}
],
"paths": {
	"blog": "/blog"
},
"google_analytics": {
	"tracking_id": "1234567"
}
}`)
	err = addFrontmatter(filename, raw)
	require.NoError(t, err)

	/* This is what we want */
	var cfgWant Config
	err = json.Unmarshal(raw, &cfgWant)
	require.NoError(t, err)

	/* Read frontmatter */
	fm, _, err := readFull(filename)
	require.NoError(t, err)

	/* Parse to config */
	var cfgGot Config
	err = json.Unmarshal(fm, &cfgGot)
	require.NoError(t, err)

	require.Equal(t, cfgWant, cfgGot)

}
