package templates

// Template describes a single story template (universe). Each template owns its
// own system prompt template, chapter layout, first-section seed, and the logic
// for deriving a lead character's personality from their name.
type Template struct {
	ID           string
	DisplayName  string
	LeadName     string   // default protagonist name for new users on this template
	ChapterNames [6]string // index 1..5; index 0 unused
	ChNums       [6]string // Chinese numerals: 一/二/三/四/五
	MaxChapters  int
	SystemTmpl   string // text/template body, receives LeadName, Personality, Chapter, Section
	FirstTmpl    string // text/template body for opening section seed
	SummaryTmpl  string // text/template body for chapter summary
	Personality  func(leadName string) string
}

// All returns the registry of all available templates keyed by template id.
func All() map[string]*Template {
	out := make(map[string]*Template, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}

// Get returns a template by id, or nil if not found.
func Get(id string) *Template {
	if t, ok := registry[id]; ok {
		return t
	}
	return nil
}

var registry = map[string]*Template{
	"campus":     campusTemplate,
	"ceo":        ceoTemplate,
	"xianxia":    xianxiaTemplate,
	"palace":     palaceTemplate,
	"esports":    esportsTemplate,
	"apocalypse": apocalypseTemplate,
	"ming":       mingTemplate,
	"wasteland":  wastelandTemplate,
	"academy":    academyTemplate,
}

// AllList returns all templates in stable display order.
// Useful for handlers that need to iterate in a predictable order.
func AllList() []*Template {
	return []*Template{
		campusTemplate,
		ceoTemplate,
		xianxiaTemplate,
		palaceTemplate,
		esportsTemplate,
		apocalypseTemplate,
		mingTemplate,
		wastelandTemplate,
		academyTemplate,
	}
}

// IsValid returns true if id matches a registered template.
func IsValid(id string) bool {
	_, ok := registry[id]
	return ok
}
