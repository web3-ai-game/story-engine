package story

import "1001universe/internal/story/templates"

// AllTemplates returns all registered story templates in stable display order.
// Re-exported from internal/story/templates so the main package only depends on
// internal/story (not internal/story/templates) — keeps the public surface tight.
func AllTemplates() []*templates.Template {
	return templates.AllList()
}

// IsValidTemplate returns true if id matches a registered story template.
func IsValidTemplate(id string) bool {
	return templates.IsValid(id)
}
