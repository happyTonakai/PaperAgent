package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/happyTonakai/paperagent/internal/config"
	"github.com/happyTonakai/paperagent/internal/session"
)

const defaultTemplate = `---
title: "{{Title}}"
date: {{Date}}
{{- if .SourceURL}}
source_url: "{{.SourceURL}}"
{{- end}}
tags:
  - paper
  - reading
---

# {{Title}}

{{- if .InitialSummary}}

## 论文总结

{{.InitialSummary}}
{{- end}}

{{- if .Messages}}

---

## 问答记录
{{- range .Messages}}
{{- if eq .Role "user"}}

### 第 {{.RoundNumber}} 轮

**Q**: {{.Content}}

{{- else}}

**A**: {{.Content}}

{{- end}}
{{- end}}
{{- end}}
`

type TemplateData struct {
	Title          string
	Date           string
	SourceURL      string
	InitialSummary string
	Messages       []session.Message
	QnA            string
	Summary        string
}

func ExportToObsidian(cfg *config.Config, p *session.Paper) (string, error) {
	if cfg.Obsidian.ExportPath == "" {
		return "", fmt.Errorf("export path not configured")
	}

	exportDir := cfg.Obsidian.ExportPath
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return "", fmt.Errorf("creating export directory: %w", err)
	}

	title := p.Title
	if title == "" {
		title = fmt.Sprintf("Paper_%s", p.Ref())
	}

	filename := sanitizeFilename(title) + "_session.md"

	// Build Q&A string.
	// Skip round 0: Q is the full paper text and A is the initial summary,
	// both already covered by the title and 论文总结 sections.
	var qna strings.Builder
	currentRound := -1
	for _, msg := range p.Messages {
		if msg.RoundNumber <= 0 {
			continue
		}
		if msg.RoundNumber != currentRound {
			currentRound = msg.RoundNumber
			qna.WriteString(fmt.Sprintf("### 第 %d 轮\n\n", currentRound))
		}
		if msg.Role == "user" {
			qna.WriteString(fmt.Sprintf("**Q**: %s\n\n", msg.Content))
		} else {
			qna.WriteString(fmt.Sprintf("**A**: %s\n\n", msg.Content))
		}
	}

	data := TemplateData{
		Title:          title,
		Date:           time.Now().Format("2006-01-02"),
		SourceURL:      p.SourceURL,
		InitialSummary: p.InitialSummary,
		Messages:       p.Messages,
		QnA:            qna.String(),
		Summary:        p.InitialSummary,
	}

	// Try to load user template
	templatePath := filepath.Join(config.PromptsDir(), "export.md")
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		// Use default template
		return exportWithDefault(exportDir, filename, data)
	}

	return exportWithTemplate(exportDir, filename, string(templateContent), data)
}

func exportWithDefault(dir, filename string, data TemplateData) (string, error) {
	var b strings.Builder

	// YAML frontmatter
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: \"%s\"\n", escapeYAML(data.Title)))
	b.WriteString(fmt.Sprintf("date: %s\n", data.Date))
	if data.SourceURL != "" {
		b.WriteString(fmt.Sprintf("source_url: \"%s\"\n", data.SourceURL))
	}
	b.WriteString("tags:\n")
	b.WriteString("  - paper\n")
	b.WriteString("  - reading\n")
	b.WriteString("---\n\n")

	b.WriteString(fmt.Sprintf("# %s\n\n", data.Title))

	if data.InitialSummary != "" {
		b.WriteString("## 论文总结\n\n")
		b.WriteString(data.InitialSummary)
		b.WriteString("\n\n")
	}

	if len(data.Messages) > 0 {
		b.WriteString("---\n\n")
		b.WriteString("## 问答记录\n\n")
		b.WriteString(data.QnA)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", fmt.Errorf("writing export file: %w", err)
	}

	return path, nil
}

func exportWithTemplate(dir, filename, template string, data TemplateData) (string, error) {
	result := template

	// Simple variable replacement
	result = strings.ReplaceAll(result, "{{Title}}", data.Title)
	result = strings.ReplaceAll(result, "{{Date}}", data.Date)
	result = strings.ReplaceAll(result, "{{SourceURL}}", data.SourceURL)
	result = strings.ReplaceAll(result, "{{Summary}}", data.InitialSummary)
	result = strings.ReplaceAll(result, "{{QnA}}", data.QnA)

	// Handle conditionals (simple implementation)
	result = handleConditionals(result, data)

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return "", fmt.Errorf("writing export file: %w", err)
	}

	return path, nil
}

func handleConditionals(template string, data TemplateData) string {
	// Process each conditional with proper {{- if .X}} ... {{- end}} pairing.
	// Uses depth tracking so that opening/closing markers are correctly matched
	// even when conditionals appear out of order in user templates.
	type conditional struct {
		open string
		keep bool
	}
	items := []conditional{
		{"{{- if .SourceURL}}", data.SourceURL != ""},
		{"{{- if .InitialSummary}}", data.InitialSummary != ""},
		{"{{- if .Messages}}", len(data.Messages) > 0},
	}

	for _, item := range items {
		openIdx := strings.Index(template, item.open)
		if openIdx == -1 {
			continue
		}
		// Find matching {{- end}} with depth tracking (handles nested conditionals).
		depth := 1
		searchFrom := openIdx + len(item.open)
		closeIdx := -1
		for {
			nextOpen := strings.Index(template[searchFrom:], "{{- if ")
			nextEnd := strings.Index(template[searchFrom:], "{{- end}}")
			if nextEnd == -1 {
				break
			}
			if nextOpen != -1 && nextOpen < nextEnd {
				depth++
				searchFrom += nextOpen + len("{{- if ")
				continue
			}
			depth--
			if depth == 0 {
				closeIdx = searchFrom + nextEnd
				break
			}
			searchFrom += nextEnd + len("{{- end}}")
		}
		if closeIdx == -1 {
			continue
		}
		closeTag := "{{- end}}"

		if item.keep {
			// Keep the content, remove only the opening/closing markers.
			template = template[:openIdx] +
				template[openIdx+len(item.open):closeIdx] +
				template[closeIdx+len(closeTag):]
		} else {
			// Remove the entire block including markers.
			template = template[:openIdx] +
				template[closeIdx+len(closeTag):]
		}
	}

	return template
}

func sanitizeFilename(s string) string {
	replacements := map[string]string{
		"/":  "_",
		"\\": "_",
		":":  "_",
		"*":  "_",
		"?":  "_",
		"\"": "_",
		"<":  "_",
		">":  "_",
		"|":  "_",
	}
	result := s
	for old, new := range replacements {
		result = strings.ReplaceAll(result, old, new)
	}
	return result
}

func escapeYAML(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
