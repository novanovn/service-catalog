package api

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

// getTemplateDir finds the template directory regardless of where tests or binary is executed from
func getTemplateDir() string {
	candidates := []string{
		filepath.Join("internal", "templates"),
		filepath.Join("..", "templates"),
		filepath.Join("..", "..", "internal", "templates"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "layout.html")); err == nil {
			return c
		}
	}
	return filepath.Join("internal", "templates")
}

// parsePage loads the base layout, pagination partial, and the specific page content template.
// This prevents templates with the same {{ define "content" }} from overwriting each other.
func parsePage(pageFileName string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"hasEnv": func(list []string, target string) bool {
			target = strings.ToLower(strings.TrimSpace(target))
			for _, s := range list {
				if strings.EqualFold(strings.TrimSpace(s), target) {
					return true
				}
			}
			return false
		},
		"contains": func(list []string, item string) bool {
			for _, s := range list {
				if s == item {
					return true
				}
			}
			return false
		},
	}
	tmplDir := getTemplateDir()
	return template.New("base").Funcs(funcMap).ParseFiles(
		filepath.Join(tmplDir, "layout.html"),
		filepath.Join(tmplDir, "pagination.html"),
		filepath.Join(tmplDir, pageFileName),
	)
}
