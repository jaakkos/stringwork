package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseFile reads a file and returns a Document ready for indexing.
// The category is determined by the file extension and path.
func ParseFile(path, workspaceRoot string) (Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read file %s: %w", path, err)
	}

	relPath, err := filepath.Rel(workspaceRoot, path)
	if err != nil {
		relPath = path
	}

	title := filepath.Base(path)
	category := categorizeFile(path, relPath)

	var parsed string
	switch category {
	case "go_source":
		parsed = parseGoSource(string(content), relPath)
	default:
		parsed = string(content)
	}

	return Document{
		Path:     relPath,
		Title:    title,
		Content:  parsed,
		Category: category,
	}, nil
}

// categorizeFile determines the document category based on file extension and path.
func categorizeFile(absPath, relPath string) string {
	ext := strings.ToLower(filepath.Ext(absPath))
	base := strings.ToLower(filepath.Base(absPath))

	switch {
	case ext == ".go":
		return "go_source"
	case ext == ".md":
		// Config-level docs get the "config" category
		if base == "claude.md" || base == "agents.md" {
			return "config"
		}
		return "markdown"
	case ext == ".yaml" || ext == ".yml":
		return "config"
	default:
		return "markdown"
	}
}

// parseGoSource extracts structured information from Go source code:
// package declaration, imports, function/method signatures, type declarations,
// and doc comments. This produces a text representation optimized for search.
func parseGoSource(content, relPath string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("File: %s\n", relPath))

	scanner := bufio.NewScanner(strings.NewReader(content))
	var (
		inComment   bool
		commentBuf  strings.Builder
		lastComment string
	)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Track block comments
		if strings.HasPrefix(trimmed, "/*") {
			inComment = true
			commentBuf.Reset()
			commentBuf.WriteString(strings.TrimPrefix(trimmed, "/*"))
			if strings.Contains(trimmed, "*/") {
				inComment = false
				lastComment = strings.TrimSuffix(commentBuf.String(), "*/")
			}
			continue
		}
		if inComment {
			if strings.Contains(trimmed, "*/") {
				inComment = false
				commentBuf.WriteString(" " + strings.TrimSuffix(trimmed, "*/"))
				lastComment = commentBuf.String()
			} else {
				commentBuf.WriteString(" " + trimmed)
			}
			continue
		}

		// Track line comments
		if strings.HasPrefix(trimmed, "//") {
			comment := strings.TrimPrefix(trimmed, "//")
			comment = strings.TrimSpace(comment)
			if lastComment != "" {
				lastComment += " " + comment
			} else {
				lastComment = comment
			}
			continue
		}

		// Package declaration
		if strings.HasPrefix(trimmed, "package ") {
			b.WriteString(trimmed)
			b.WriteString("\n\n")
			lastComment = ""
			continue
		}

		// Type declarations (structs, interfaces)
		if strings.HasPrefix(trimmed, "type ") {
			if lastComment != "" {
				b.WriteString("// " + lastComment + "\n")
			}
			b.WriteString(trimmed)
			b.WriteString("\n\n")
			lastComment = ""
			continue
		}

		// Function and method signatures
		if strings.HasPrefix(trimmed, "func ") {
			if lastComment != "" {
				b.WriteString("// " + lastComment + "\n")
			}
			// Extract just the signature (up to the opening brace)
			sig := extractFuncSignature(trimmed)
			b.WriteString(sig)
			b.WriteString("\n\n")
			lastComment = ""
			continue
		}

		// Const/var blocks
		if strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "var ") {
			if lastComment != "" {
				b.WriteString("// " + lastComment + "\n")
			}
			b.WriteString(trimmed)
			b.WriteString("\n")
			lastComment = ""
			continue
		}

		// Any other non-empty line resets the comment tracker
		if trimmed != "" {
			lastComment = ""
		}
	}

	return b.String()
}

// extractFuncSignature extracts the function signature up to the opening brace.
func extractFuncSignature(line string) string {
	if idx := strings.Index(line, "{"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

// FormatSessionNote formats a session note for indexing.
func FormatSessionNote(id int, author, content, category string) Document {
	path := fmt.Sprintf("session_note:%d", id)
	title := fmt.Sprintf("Note by %s [%s]", author, category)
	return Document{
		Path:     path,
		Title:    title,
		Content:  content,
		Category: "session_note",
	}
}

// FormatTaskSummary formats a completed task for indexing.
func FormatTaskSummary(id int, title, description, assignedTo, resultSummary string) Document {
	path := fmt.Sprintf("task:%d", id)

	var content strings.Builder
	content.WriteString(fmt.Sprintf("Task: %s\n", title))
	if description != "" {
		content.WriteString(fmt.Sprintf("Description: %s\n", description))
	}
	if assignedTo != "" {
		content.WriteString(fmt.Sprintf("Assigned to: %s\n", assignedTo))
	}
	if resultSummary != "" {
		content.WriteString(fmt.Sprintf("Result: %s\n", resultSummary))
	}

	return Document{
		Path:     path,
		Title:    title,
		Content:  content.String(),
		Category: "task_summary",
	}
}

// ShouldIndex returns true if the file at the given path should be indexed.
func ShouldIndex(path string, indexGoSource bool) bool {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))

	// Skip hidden files and directories
	if strings.HasPrefix(base, ".") {
		return false
	}

	// Skip common non-content directories
	dir := filepath.Dir(path)
	for _, skip := range []string{"vendor", "node_modules", ".git", ".stringwork", "testdata"} {
		sep := string(filepath.Separator)
		if strings.Contains(dir, sep+skip+sep) || strings.HasSuffix(dir, sep+skip) || strings.HasPrefix(dir, skip+sep) || dir == skip {
			return false
		}
	}

	switch ext {
	case ".md":
		return true
	case ".go":
		return indexGoSource && !strings.HasSuffix(base, "_test.go")
	case ".yaml", ".yml":
		return base == "config.yaml" || base == "config.yml"
	default:
		return false
	}
}
